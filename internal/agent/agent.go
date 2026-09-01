package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/larry-probe/larry/internal/protocol"
)

// Version is the agent build version, overridable via -ldflags.
var Version = "dev"

// Config configures one agent run.
type Config struct {
	ServerURL string // ws://host:port/api/agent
	Token     string // pre-registered agent token
	ProbeAddr string // probe listen addr, default ":36510"
	ProbeHost string // override host others use to dial the probe port
	Interval  int    // report interval seconds (server may override)
	Once      bool   // single session, exit after disconnect (for self-test)
	Name      string // override reported hostname
}

// Agent wires the collector, probe server, and the WebSocket session loop.
type Agent struct {
	cfg       Config
	collector *Collector
	probe     *ProbeServer
}

// Run drives the connect-report-reconnect loop. It returns only in --once mode
// or on unrecoverable misconfiguration; otherwise it reconnects forever with
// exponential backoff.
func Run(cfg Config) {
	if err := cfg.validate(); err != nil {
		log.Fatalf("agent: %v", err)
	}
	collector, err := NewCollector()
	if err != nil {
		log.Fatalf("agent: collector: %v", err)
	}
	probe, err := ListenProbe(cfg.ProbeAddr)
	if err != nil {
		log.Printf("agent: probe listener disabled (%v) — mesh row for this node will be empty", err)
	} else {
		defer probe.Close()
	}

	a := &Agent{cfg: cfg, collector: collector, probe: probe}
	log.Printf("agent: probe listening on %s, server=%s", probe.Addr(), cfg.ServerURL)

	backoff := time.Second
	for {
		err := a.runSession()
		if cfg.Once {
			return
		}
		if err != nil {
			log.Printf("agent: session ended: %v; reconnect in %s", err, backoff)
		}
		time.Sleep(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (c *Config) validate() error {
	if c.ServerURL == "" {
		return fmt.Errorf("--server is required")
	}
	if c.Token == "" {
		return fmt.Errorf("--token is required")
	}
	if c.Interval <= 0 {
		c.Interval = 5
	}
	if !strings.HasPrefix(c.ServerURL, "ws://") && !strings.HasPrefix(c.ServerURL, "wss://") {
		// be forgiving: accept bare host:port and http(s):// prefixes
		if strings.HasPrefix(c.ServerURL, "http://") {
			c.ServerURL = "ws://" + strings.TrimPrefix(c.ServerURL, "http://")
		} else if strings.HasPrefix(c.ServerURL, "https://") {
			c.ServerURL = "wss://" + strings.TrimPrefix(c.ServerURL, "https://")
		} else {
			c.ServerURL = "ws://" + c.ServerURL
		}
	}
	return nil
}

func (a *Agent) runSession() error {
	u, err := url.Parse(a.cfg.ServerURL)
	if err != nil {
		return err
	}
	// Operators pass the dashboard host:port; the agent always speaks the
	// agent protocol on the /api/agent endpoint. Allow an explicit override
	// only if a non-root path was given.
	if u.Path == "" || u.Path == "/" {
		u.Path = "/api/agent"
	}
	hdr := http.Header{}
	hdr.Set("X-Larry-Token", a.cfg.Token) // also sent as header for cheap server-side rejection
	c, _, err := websocket.DefaultDialer.Dial(u.String(), hdr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer c.Close()

	// 1. Hello
	hostname, kernel := a.collector.HostInfo()
	if a.cfg.Name != "" {
		hostname = a.cfg.Name
	}
	osName, arch := RuntimeInfo()
	probeAddr := a.probeAddr()
	hello := protocol.Hello{
		Type:      "hello",
		Token:     a.cfg.Token,
		Hostname:  hostname,
		OS:        osName,
		Arch:      arch,
		Kernel:    kernel,
		Version:   Version,
		ProbeAddr: probeAddr,
	}
	if err := writeJSON(c, hello); err != nil {
		return fmt.Errorf("hello: %w", err)
	}

	// 2. Await welcome (or error) before reporting.
	var welcome protocol.Welcome
	if err := readJSON(c, &welcome); err != nil {
		return fmt.Errorf("welcome: %w", err)
	}
	switch welcome.Type {
	case "welcome":
		log.Printf("agent: registered as id=%d name=%q interval=%ds", welcome.AgentID, welcome.Name, welcome.Interval)
	case "error":
		return fmt.Errorf("server rejected: %s", welcome.Name) // Name field reused as error msg
	default:
		return fmt.Errorf("unexpected first message type %q", welcome.Type)
	}
	interval := welcome.Interval
	if interval <= 0 {
		interval = a.cfg.Interval
	}

	// 3. Reporting ticker + inbound message reader.
	var mu sync.Mutex
	prevInterval := interval
	reportTick := time.NewTicker(time.Duration(interval) * time.Second)
	defer reportTick.Stop()
	stop := make(chan struct{})

	// reader
	go func() {
		defer close(stop)
		for {
			_, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			var msg struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			switch msg.Type {
			case "mesh_ping":
				a.handleMeshPing(c, data)
			case "config":
				var cfg protocol.Welcome
				_ = json.Unmarshal(data, &cfg)
				if cfg.Interval > 0 {
					mu.Lock()
					interval = cfg.Interval
					mu.Unlock()
				}
			}
		}
	}()

	// initial report immediately, then on interval
	a.sendReport(c)
	for {
		select {
		case <-stop:
			return nil
		case <-reportTick.C:
			mu.Lock()
			iv := interval
			mu.Unlock()
			// if interval changed via a "config" message, rebuild the ticker
			if iv > 0 && iv != prevInterval {
				prevInterval = iv
				reportTick.Stop()
				reportTick = time.NewTicker(time.Duration(iv) * time.Second)
			}
			if err := a.sendReport(c); err != nil {
				return err
			}
		}
	}
}

// sendReport collects and sends one metric sample.
func (a *Agent) sendReport(c *websocket.Conn) error {
	rep, err := a.collector.Collect()
	if err != nil {
		return err
	}
	rep.Type = "report"
	return writeJSON(c, rep)
}

// handleMeshPing probes all targets concurrently and replies with one result.
func (a *Agent) handleMeshPing(c *websocket.Conn, raw []byte) {
	var mp protocol.MeshPing
	if err := json.Unmarshal(raw, &mp); err != nil {
		return
	}
	type rp struct {
		i int
		r protocol.ProbeResult
	}
	res := make([]rp, len(mp.Targets))
	var wg sync.WaitGroup
	for i, t := range mp.Targets {
		wg.Add(1)
		go func(i int, t protocol.Target) {
			defer wg.Done()
			lat, err := ProbeTarget(t.Addr)
			pr := protocol.ProbeResult{TargetID: t.ID, Addr: t.Addr}
			if err != nil {
				pr.Latency = -1
				pr.Err = err.Error()
			} else {
				pr.Latency = lat
			}
			res[i] = rp{i: i, r: pr}
		}(i, t)
	}
	wg.Wait()

	out := protocol.MeshResult{Type: "mesh_result", ReqID: mp.ReqID}
	for _, r := range res {
		out.Results = append(out.Results, r.r)
	}
	_ = writeJSON(c, out)
}

// probeAddr returns the address other agents should dial to reach this agent's
// probe port. If ProbeHost is set it is "host:port"; otherwise the host is left
// empty (":port") and the server fills it from the connection's remote address.
func (a *Agent) probeAddr() string {
	port := ProbePort
	if a.probe != nil {
		if addr := a.probe.Addr(); addr != "" {
			// net.Listener Addr like [::]:36510 or 0.0.0.0:36510
			if _, p, err := net.SplitHostPort(addr); err == nil && p != "" {
				port = p
			}
		}
	}
	if a.cfg.ProbeHost != "" {
		return a.cfg.ProbeHost + ":" + port
	}
	return ":" + port
}

// --- small WS helpers ---

func writeJSON(c *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.WriteMessage(websocket.TextMessage, data)
	return nil
}

func readJSON(c *websocket.Conn, v any) error {
	_, data, err := c.ReadMessage()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
