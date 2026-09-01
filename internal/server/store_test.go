package server

import (
	"testing"

	"github.com/larry-probe/larry/internal/protocol"
)

// TestStoreReportAndSnapshot verifies the live-state store round-trips a
// report into the dashboard snapshot — the core data path from agent to UI.
func TestStoreReportAndSnapshot(t *testing.T) {
	s := NewStore()

	hello := protocol.Hello{Type: "hello", Hostname: "h1", OS: "linux"}
	s.OnHello(1, "alpha", hello, "10.0.0.1:5000")

	rep := protocol.Report{Type: "report", TS: 123, CPU: 42.5, MemUsed: 100, MemTotal: 1000, Uptime: 99}
	s.OnReport(1, rep)

	snap := s.Snapshot()
	if len(snap.Agents) != 1 {
		t.Fatalf("snapshot has %d agents, want 1", len(snap.Agents))
	}
	a := snap.Agents[0]
	if a.ID != 1 || a.Name != "alpha" || !a.Online {
		t.Fatalf("snapshot agent = %+v", a)
	}
	if a.CPU != 42.5 || a.MemTotal != 1000 || a.Uptime != 99 {
		t.Fatalf("snapshot report fields wrong: %+v", a.Report)
	}
}

// TestStoreMeshResult verifies pairwise latency results land in the mesh
// matrix keyed by from->to.
func TestStoreMeshResult(t *testing.T) {
	s := NewStore()
	s.OnHello(1, "a", protocol.Hello{}, "1.1.1.1:1")
	s.OnHello(2, "b", protocol.Hello{}, "2.2.2.2:1")

	s.OnMeshResult(1, []protocol.ProbeResult{
		{TargetID: 2, Latency: 5.2},
	})
	s.OnMeshResult(2, []protocol.ProbeResult{
		{TargetID: 1, Latency: 4.8},
	})

	snap := s.Snapshot()
	if snap.Mesh[1][2] != 5.2 {
		t.Fatalf("mesh[1][2] = %v, want 5.2", snap.Mesh[1][2])
	}
	if snap.Mesh[2][1] != 4.8 {
		t.Fatalf("mesh[2][1] = %v, want 4.8", snap.Mesh[2][1])
	}
}

// TestResolveProbeAddr verifies that when an agent reports a probe address
// without a host (":port", common behind NAT), the server substitutes the
// host it observed from the agent's WebSocket connection.
func TestResolveProbeAddr(t *testing.T) {
	s := NewStore()
	// agent reports ":36510" (no host); server saw it at 203.0.113.5:43210
	s.OnHello(7, "nat", protocol.Hello{ProbeAddr: ":36510"}, "203.0.113.5:43210")
	got := s.ResolveProbeAddr(7)
	want := "203.0.113.5:36510"
	if got != want {
		t.Fatalf("ResolveProbeAddr = %q, want %q", got, want)
	}

	// agent reports an explicit host:port -> used verbatim
	s.OnHello(8, "explicit", protocol.Hello{ProbeAddr: "1.2.3.4:36510"}, "5.6.7.8:1")
	if got := s.ResolveProbeAddr(8); got != "1.2.3.4:36510" {
		t.Fatalf("explicit probe addr = %q, want 1.2.3.4:36510", got)
	}
}
