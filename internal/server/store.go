package server

import (
	"net"
	"sync"
	"time"

	"github.com/larry-probe/larry/internal/protocol"
)

// historyLen is how many recent samples are kept per agent for the dashboard's
// sparkline charts. At a 5s interval this is ~5 minutes of fine-grained trend.
const historyLen = 64

// agentState is the server's live record for one connected (or recently seen)
// agent. It is the source the dashboard snapshot is built from.
type agentState struct {
	mu         sync.RWMutex
	id         int
	name       string
	online     bool
	lastSeen   time.Time
	remoteAddr string
	probeAddr  string
	info       protocol.Hello    // last Hello metadata
	last       protocol.Report   // most recent Report
	hist       []protocol.Report // ring buffer (newest appended, trimmed)
	meshRow    map[int]float64   // latency from this agent to others
}

// Store is the in-memory live state: all agents, their latest metrics, recent
// history, and the pairwise mesh matrix. It is NOT persisted — historical
// metrics live only as long as the server process. This is deliberate: a probe
// is a live-operations tool, not a long-term metrics warehouse. If you need
// long retention, export the /api/snapshot endpoint to your TSDB of choice.
type Store struct {
	mu      sync.RWMutex
	agents  map[int]*agentState
	meshTTL time.Duration // how long a mesh result is considered fresh
}

// NewStore creates an empty live store.
func NewStore() *Store {
	return &Store{
		agents:  make(map[int]*agentState),
		meshTTL: 2 * time.Minute,
	}
}

// ensureAgent returns the existing state for id, creating a fresh entry if the
// agent connects for the first time (or after the server restarted).
func (s *Store) ensureAgent(id int, name string) *agentState {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.agents[id]
	if !ok {
		a = &agentState{id: id, name: name, meshRow: make(map[int]float64)}
		s.agents[id] = a
	}
	if name != "" {
		a.name = name
	}
	return a
}

// OnHello records metadata from a fresh agent connection.
func (s *Store) OnHello(id int, name string, hello protocol.Hello, remoteAddr string) {
	a := s.ensureAgent(id, name)
	a.mu.Lock()
	a.online = true
	a.lastSeen = time.Now()
	a.remoteAddr = remoteAddr
	a.info = hello
	a.probeAddr = hello.ProbeAddr
	a.mu.Unlock()
}

// OnReport stores a new metric sample and marks the agent live.
func (s *Store) OnReport(id int, rep protocol.Report) {
	s.mu.Lock()
	a, ok := s.agents[id]
	s.mu.Unlock()
	if !ok {
		// report before hello? shouldn't happen, but be defensive
		a = s.ensureAgent(id, "")
	}
	a.mu.Lock()
	a.online = true
	a.lastSeen = time.Now()
	a.last = rep
	a.hist = append(a.hist, rep)
	if len(a.hist) > historyLen {
		a.hist = a.hist[len(a.hist)-historyLen:]
	}
	a.mu.Unlock()
}

// OnMeshResult stores one agent's probe results into the mesh matrix.
func (s *Store) OnMeshResult(fromID int, results []protocol.ProbeResult) {
	s.mu.Lock()
	a, ok := s.agents[fromID]
	if !ok {
		a = s.ensureAgent(fromID, "")
	}
	s.mu.Unlock()
	a.mu.Lock()
	if a.meshRow == nil {
		a.meshRow = make(map[int]float64)
	}
	for _, pr := range results {
		a.meshRow[pr.TargetID] = pr.Latency
	}
	a.mu.Unlock()
}

// OnDisconnect marks an agent offline (it will still show in the list, dimmed).
func (s *Store) OnDisconnect(id int) {
	s.mu.Lock()
	a, ok := s.agents[id]
	s.mu.Unlock()
	if !ok {
		return
	}
	a.mu.Lock()
	a.online = false
	a.mu.Unlock()
}

// DeleteAgent purges an agent completely from the live store (e.g. when revoked).
func (s *Store) DeleteAgent(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.agents, id)
}

// SweepOffline marks agents that have not reported within ttl as offline.
// Called periodically by the server so a crashed agent stops looking "green".
func (s *Store) SweepOffline(ttl time.Duration) {
	cutoff := time.Now().Add(-ttl)
	s.mu.RLock()
	ids := make([]int, 0, len(s.agents))
	for id := range s.agents {
		ids = append(ids, id)
	}
	s.mu.RUnlock()
	for _, id := range ids {
		s.mu.RLock()
		a := s.agents[id]
		s.mu.RUnlock()
		a.mu.RLock()
		stale := a.lastSeen.Before(cutoff)
		a.mu.RUnlock()
		if stale {
			a.mu.Lock()
			a.online = false
			a.mu.Unlock()
		}
	}
}

// ResolveProbeAddr returns the dialable address other agents should use to
// probe this agent. If the agent reported a host (non-empty host:port) it is
// used as-is; otherwise the host is taken from the server's view of the
// agent's WebSocket remote address.
func (s *Store) ResolveProbeAddr(id int) string {
	s.mu.RLock()
	a, ok := s.agents[id]
	s.mu.RUnlock()
	if !ok {
		return ""
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	addr := a.probeAddr
	host, port, err := net.SplitHostPort(addr)
	if err == nil && host != "" {
		return addr
	}
	// fall back to the WS remote host + the reported port
	rmHost := a.remoteAddr
	if h, _, err := net.SplitHostPort(rmHost); err == nil {
		rmHost = h
	}
	if port == "" {
		port = "36510"
	}
	return net.JoinHostPort(rmHost, port)
}

// Snapshot builds the full dashboard payload pushed to browsers.
func (s *Store) Snapshot() protocol.Snapshot {
	s.mu.RLock()
	ids := make([]int, 0, len(s.agents))
	for id := range s.agents {
		ids = append(ids, id)
	}
	s.mu.RUnlock()

	out := protocol.Snapshot{
		Type:   "snapshot",
		TS:     protocol.Now(),
		Agents: make([]protocol.AgentState, 0, len(ids)),
		Mesh:   make(map[int]map[int]float64),
	}
	for _, id := range ids {
		s.mu.RLock()
		a := s.agents[id]
		s.mu.RUnlock()
		a.mu.RLock()
		st := protocol.AgentState{
			ID:         a.id,
			Name:       a.name,
			Online:     a.online,
			LastSeen:   a.lastSeen.Unix(),
			Hostname:   a.info.Hostname,
			OS:         a.info.OS,
			Arch:       a.info.Arch,
			Kernel:     a.info.Kernel,
			Version:    a.info.Version,
			ProbeAddr:  a.probeAddr,
			RemoteAddr: a.remoteAddr,
			Report:     a.last,
		}
		meshRow := a.meshRow
		a.mu.RUnlock()
		out.Agents = append(out.Agents, st)
		if len(meshRow) > 0 {
			row := make(map[int]float64, len(meshRow))
			for k, v := range meshRow {
				row[k] = v
			}
			out.Mesh[a.id] = row
		}
	}
	return out
}

// OnlineAgentIDs returns the IDs of all agents currently marked online, plus
// their resolvable probe addresses. The mesh scheduler uses this to build the
// pairwise probe target lists.
func (s *Store) OnlineAgentIDs() (ids []int, addrs map[int]string) {
	s.mu.RLock()
	for id, a := range s.agents {
		a.mu.RLock()
		online := a.online
		a.mu.RUnlock()
		if online {
			ids = append(ids, id)
		}
	}
	s.mu.RUnlock()
	addrs = make(map[int]string, len(ids))
	for _, id := range ids {
		addrs[id] = s.ResolveProbeAddr(id)
	}
	return ids, addrs
}
