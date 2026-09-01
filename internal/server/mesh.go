package server

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/larry-probe/larry/internal/protocol"
)

// MeshScheduler periodically asks every online agent to probe every other
// online agent. The results build the pairwise latency grid that is the
// signature feature of Larry: where Nezha and Komari show only each server's
// own metrics, Larry also shows how the servers reach each other.
type MeshScheduler struct {
	hub      *Hub
	store    *Store
	interval time.Duration
	counter  uint64
}

// NewMeshScheduler builds a scheduler. interval is how often a full mesh round
// runs; 15s is a sensible default — enough resolution to catch flaps without
// flooding agents with probe traffic.
func NewMeshScheduler(hub *Hub, store *Store, interval time.Duration) *MeshScheduler {
	return &MeshScheduler{hub: hub, store: store, interval: interval}
}

// Run blocks for the lifetime of the server, ticking one mesh round per
// interval. It also runs an immediate first round shortly after start so the
// grid is not empty for a whole interval.
func (m *MeshScheduler) Run() {
	time.Sleep(2 * time.Second) // let agents finish their first hello
	m.tick()
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for range t.C {
		m.tick()
	}
}

func (m *MeshScheduler) tick() {
	ids, addrs := m.store.OnlineAgentIDs()
	if len(ids) < 2 {
		// nothing to mesh with; leave existing results in place
		return
	}
	reqID := fmt.Sprintf("mesh-%d-%d", time.Now().Unix(), atomic.AddUint64(&m.counter, 1))

	// Fan out: each online agent probes every other online agent in parallel.
	// We send all MeshPing messages synchronously per round — they're tiny and
	// the actual probing happens concurrently inside each agent.
	for _, fromID := range ids {
		targets := make([]protocol.Target, 0, len(ids)-1)
		for _, toID := range ids {
			if toID == fromID {
				continue
			}
			if addr := addrs[toID]; addr != "" {
				targets = append(targets, protocol.Target{ID: toID, Addr: addr})
			}
		}
		if len(targets) == 0 {
			continue
		}
		m.hub.SendMeshPing(fromID, protocol.MeshPing{
			Type:    "mesh_ping",
			ReqID:   reqID,
			Targets: targets,
		})
	}
}
