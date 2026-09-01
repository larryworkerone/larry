package agent

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"sync"
	"time"
)

// ProbePort is the default TCP port the agent listens on for mesh probes.
// Other agents dial it to measure pairwise network latency.
const ProbePort = "36510"

// probeTimeout caps a single probe dial+round-trip. Anything beyond this is
// reported as a failure rather than making the mesh round drag on.
const probeTimeout = 3 * time.Second

// probeNonceLen is the number of random hex bytes echoed back; long enough to
// distinguish a real Larry peer from an arbitrary open TCP port.
const probeNonceLen = 8

// ProbeServer listens on a TCP port and answers probe handshakes from other
// agents. The handshake is intentionally minimal: the dialer sends
// "PING <nonce>\n", the server echoes "<nonce>\n". The dialer measures the
// time between sending the nonce and reading the echo back — that round-trip
// is the pairwise latency the mesh grid is built from.
type ProbeServer struct {
	ln net.Listener
	wg sync.WaitGroup
}

// ListenProbe opens the probe TCP port on the given address. Pass ":36510"
// for all interfaces. If addr is empty, ProbePort on all interfaces is used.
func ListenProbe(addr string) (*ProbeServer, error) {
	if addr == "" {
		addr = ":" + ProbePort
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &ProbeServer{ln: ln}
	s.wg.Add(1)
	go s.acceptLoop()
	return s, nil
}

// Addr returns the address the probe server is listening on.
func (s *ProbeServer) Addr() string {
	if s == nil || s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Close stops accepting new probe connections.
func (s *ProbeServer) Close() {
	if s != nil && s.ln != nil {
		s.ln.Close()
		s.wg.Wait()
	}
}

func (s *ProbeServer) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *ProbeServer) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(probeTimeout))
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return
	}
	// Expect "PING <nonce>\n"; echo the nonce back.
	var nonce string
	if _, err := fmt.Sscanf(line, "PING %s", &nonce); err != nil || len(nonce) == 0 {
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(probeTimeout))
	_, _ = fmt.Fprintf(conn, "%s\n", nonce)
}

// ProbeTarget dials one peer, performs the nonce handshake, and returns the
// round-trip latency in milliseconds. A non-nil error means the target was
// unreachable or did not speak the probe protocol.
func ProbeTarget(addr string) (float64, error) {
	conn, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return -1, err
	}
	defer conn.Close()
	nonce, err := randomNonce()
	if err != nil {
		return -1, err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(probeTimeout))
	if _, err := fmt.Fprintf(conn, "PING %s\n", nonce); err != nil {
		return -1, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(probeTimeout))
	br := bufio.NewReader(conn)
	start := time.Now()
	line, err := br.ReadString('\n')
	if err != nil {
		return -1, err
	}
	latency := float64(time.Since(start).Microseconds()) / 1000.0
	var echo string
	if _, err := fmt.Sscanf(line, "%s", &echo); err != nil || echo != nonce {
		return -1, fmt.Errorf("probe echo mismatch (not a Larry peer?)")
	}
	return round1(latency), nil
}

func randomNonce() (string, error) {
	b := make([]byte, probeNonceLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
