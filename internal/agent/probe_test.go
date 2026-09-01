package agent

import (
	"net"
	"strings"
	"testing"
	"time"
)

// TestProbeHandshake is the one self-check for the mesh feature: it starts a
// ProbeServer on an ephemeral port and verifies that ProbeTarget completes the
// nonce handshake and returns a non-negative latency. If this passes, the
// signature pairwise-latency primitive works; everything above it (mesh
// scheduler, grid rendering) is wiring.
func TestProbeHandshake(t *testing.T) {
	// ":0" lets the kernel pick a free port so the test never collides.
	srv, err := ListenProbe(":0")
	if err != nil {
		t.Fatalf("ListenProbe: %v", err)
	}
	defer srv.Close()
	addr := srv.Addr()

	lat, err := ProbeTarget(addr)
	if err != nil {
		t.Fatalf("ProbeTarget(%s): %v", addr, err)
	}
	if lat < 0 {
		t.Fatalf("ProbeTarget returned negative latency %v", lat)
	}
	if lat > 100 { // localhost must be sub-millisecond; 100ms is a sanity ceiling
		t.Fatalf("ProbeTarget latency %vms implausibly high for localhost", lat)
	}
}

// TestProbeRejectsBogus verifies the nonce check: a peer that does not echo
// the right nonce must be rejected, not silently accepted.
func TestProbeRejectsBogus(t *testing.T) {
	// A throwaway listener that accepts but echoes garbage instead of the nonce.
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 64)
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		_, _ = c.Read(buf)
		_, _ = c.Write([]byte("not-the-nonce\n"))
	}()

	if _, err := ProbeTarget(ln.Addr().String()); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("ProbeTarget should reject a bogus echo, got err=%v", err)
	}
}
