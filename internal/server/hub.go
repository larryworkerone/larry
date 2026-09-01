package server

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/larry-probe/larry/internal/protocol"
)

// agentConn wraps one live agent WebSocket so the mesh scheduler can push
// MeshPing messages to it. Each conn has its own write mutex because the read
// pump (agent -> server) and the mesh writer (server -> agent) share the conn.
type agentConn struct {
	id   int
	conn *websocket.Conn
	mu   sync.Mutex
}

func (ac *agentConn) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	_ = ac.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return ac.conn.WriteMessage(websocket.TextMessage, data)
}

// Hub owns all live connections (agents + browser dashboards) and is the
// bridge between the protocol layer and the in-memory store.
type Hub struct {
	registry *Registry
	store    *Store

	mu       sync.Mutex
	agents   map[int]*agentConn
	browsers map[*browserConn]struct{}

	upgrader websocket.Upgrader
}

type browserConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

// NewHub wires the registry and store into a connection manager.
func NewHub(registry *Registry, store *Store) *Hub {
	return &Hub{
		registry: registry,
		store:    store,
		agents:   make(map[int]*agentConn),
		browsers: make(map[*browserConn]struct{}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}
}

// RunBroadcaster pushes a fresh snapshot to every browser every refresh period.
// Slow browsers are dropped: a dashboard viewer that stalls should not block
// the others. This goroutine runs for the lifetime of the server.
func (h *Hub) RunBroadcaster(period time.Duration) {
	t := time.NewTicker(period)
	defer t.Stop()
	for range t.C {
		snap := h.store.Snapshot()
		data, err := json.Marshal(snap)
		if err != nil {
			continue
		}
		h.mu.Lock()
		dead := make([]*browserConn, 0)
		for bc := range h.browsers {
			bc.mu.Lock()
			_ = bc.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
			if err := bc.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				dead = append(dead, bc)
			}
			bc.mu.Unlock()
		}
		for _, bc := range dead {
			delete(h.browsers, bc)
			bc.conn.Close()
		}
		h.mu.Unlock()
	}
}

// RunSweeper periodically marks stale agents offline so a crashed agent does
// not keep a green badge forever.
func (h *Hub) RunSweeper(period, ttl time.Duration) {
	t := time.NewTicker(period)
	defer t.Stop()
	for range t.C {
		h.store.SweepOffline(ttl)
	}
}

// HandleBrowser upgrades a browser WebSocket and keeps it subscribed until it
// closes. On connect we also push an immediate snapshot so the dashboard paints
// before the next broadcast tick.
func (h *Hub) HandleBrowser(w http.ResponseWriter, r *http.Request) {
	c, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	bc := &browserConn{conn: c}
	h.mu.Lock()
	h.browsers[bc] = struct{}{}
	h.mu.Unlock()

	// immediate snapshot so the page is not blank until the next tick
	snap, _ := json.Marshal(h.store.Snapshot())
	bc.mu.Lock()
	_ = bc.conn.WriteMessage(websocket.TextMessage, snap)
	bc.mu.Unlock()

	// read pump: browsers only send tiny pings/control; just drain until close.
	defer func() {
		h.mu.Lock()
		delete(h.browsers, bc)
		h.mu.Unlock()
		c.Close()
	}()
	for {
		if _, _, err := c.ReadMessage(); err != nil {
			return
		}
	}
}

// HandleAgent upgrades an agent WebSocket, authenticates the Hello, then runs
// the report/mesh_result read pump until the connection drops.
func (h *Hub) HandleAgent(w http.ResponseWriter, r *http.Request) {
	c, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	remoteAddr := r.RemoteAddr
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		// trust a single hop (the reverse proxy in front of Larry)
		remoteAddr = fwd
	}
	defer c.Close()

	// 1. Hello / auth
	var hello protocol.Hello
	if err := readMsg(c, &hello); err != nil {
		_ = writeMsg(c, protocol.ServerError{Type: "error", Msg: "expected hello: " + err.Error()})
		return
	}
	if hello.Type != "hello" {
		_ = writeMsg(c, protocol.ServerError{Type: "error", Msg: "first message must be hello"})
		return
	}
	rec, ok := h.registry.Lookup(hello.Token)
	if !ok {
		_ = writeMsg(c, protocol.ServerError{Type: "error", Msg: "unknown token"})
		return
	}

	// 2. Register
	ac := &agentConn{id: rec.ID, conn: c}
	h.mu.Lock()
	h.agents[rec.ID] = ac
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		// only remove if it's still us (a reconnect may have replaced it)
		if cur, ok := h.agents[rec.ID]; ok && cur == ac {
			delete(h.agents, rec.ID)
		}
		h.mu.Unlock()
		h.store.OnDisconnect(rec.ID)
	}()

	h.store.OnHello(rec.ID, rec.Name, hello, remoteAddr)
	if err := ac.writeJSON(protocol.Welcome{Type: "welcome", AgentID: rec.ID, Name: rec.Name, Interval: 5}); err != nil {
		return
	}
	log.Printf("server: agent id=%d name=%q connected from %s", rec.ID, rec.Name, remoteAddr)

	// 3. Read pump
	for {
		_, data, err := c.ReadMessage()
		if err != nil {
			return
		}
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &head); err != nil {
			continue
		}
		switch head.Type {
		case "report":
			var rep protocol.Report
			if err := json.Unmarshal(data, &rep); err == nil {
				h.store.OnReport(rec.ID, rep)
			}
		case "mesh_result":
			var mr protocol.MeshResult
			if err := json.Unmarshal(data, &mr); err == nil {
				h.store.OnMeshResult(rec.ID, mr.Results)
			}
		}
	}
}

// SendMeshPing pushes a probe request to one agent. Returns false if the agent
// is not currently connected (the mesh scheduler skips absent agents).
func (h *Hub) SendMeshPing(agentID int, mp protocol.MeshPing) bool {
	h.mu.Lock()
	ac, ok := h.agents[agentID]
	h.mu.Unlock()
	if !ok {
		return false
	}
	return ac.writeJSON(mp) == nil
}

// readMsg/writeMsg are tiny helpers for the one-off hello/error exchange.
func readMsg(c *websocket.Conn, v any) error {
	_, data, err := c.ReadMessage()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func writeMsg(c *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.WriteMessage(websocket.TextMessage, data)
}
