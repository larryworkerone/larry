// Package protocol defines the JSON messages exchanged over WebSocket
// between Larry agents and the Larry server, and the snapshots the server
// pushes to browser dashboards.
//
// All messages carry a "type" tag so a single connection can multiplex
// several message kinds without out-of-band framing.
package protocol

import "time"

// ---- message direction: Agent -> Server ----

// Hello is the first message an agent sends after opening its WebSocket.
// Token authenticates the agent against the server's agent registry.
// ProbeAddr is the host:port on which the agent listens for mesh probes
// (other agents dial it to measure pairwise latency).
type Hello struct {
	Type      string `json:"type"`       // "hello"
	Token     string `json:"token"`      // pre-registered agent token
	Hostname  string `json:"hostname"`   // reported host name
	OS        string `json:"os"`         // runtime.GOOS
	Arch      string `json:"arch"`       // runtime.GOARCH
	Kernel    string `json:"kernel"`     // kernel/version string
	Version   string `json:"version"`    // agent version
	ProbeAddr string `json:"probe_addr"` // host:port the agent listens on for mesh probes
}

// Report carries one sample of host metrics. All byte counts are in bytes;
// the UI converts to human units. NetIn/NetOut are bytes-per-second deltas
// computed by the agent over the previous interval.
type Report struct {
	Type      string             `json:"type"` // "report"
	TS        int64              `json:"ts"`   // unix seconds
	CPU       float64            `json:"cpu"`  // 0..100 percent
	MemUsed   uint64             `json:"mem_used"`
	MemTotal  uint64             `json:"mem_total"`
	SwapUsed  uint64             `json:"swap_used"`
	SwapTotal uint64             `json:"swap_total"`
	Disks     []Disk             `json:"disks"`
	NetIn     uint64             `json:"net_in"`  // bytes/s since last report
	NetOut    uint64             `json:"net_out"` // bytes/s since last report
	Load1     float64            `json:"load1"`
	Load5     float64            `json:"load5"`
	Load15    float64            `json:"load15"`
	Conns     uint64             `json:"conns"`  // established TCP connections
	Procs     uint64             `json:"procs"`  // process count
	Uptime    uint64             `json:"uptime"` // seconds since boot
	BootTime  int64              `json:"boot_time"`
	Temps     map[string]float64 `json:"temps,omitempty"` // sensor -> °C
}

// Disk is a single mount point's usage.
type Disk struct {
	Mount string `json:"mount"`
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
}

// MeshResult reports one round of pairwise probing back to the server.
type MeshResult struct {
	Type    string        `json:"type"` // "mesh_result"
	ReqID   string        `json:"req_id"`
	Results []ProbeResult `json:"results"`
}

// ProbeResult is the outcome of probing a single target.
type ProbeResult struct {
	TargetID int     `json:"target_id"`
	Addr     string  `json:"addr"`
	Latency  float64 `json:"latency_ms"` // milliseconds, -1 on failure
	Err      string  `json:"err,omitempty"`
}

// ---- message direction: Server -> Agent ----

// Welcome acknowledges a successful Hello and configures the agent.
type Welcome struct {
	Type     string `json:"type"` // "welcome"
	AgentID  int    `json:"agent_id"`
	Name     string `json:"name"`     // canonical name from registry
	Interval int    `json:"interval"` // report interval, seconds
}

// MeshPing instructs the agent to probe the given targets and reply with
// a MeshResult carrying ReqID.
type MeshPing struct {
	Type    string   `json:"type"` // "mesh_ping"
	ReqID   string   `json:"req_id"`
	Targets []Target `json:"targets"`
}

// Target is one peer to probe.
type Target struct {
	ID   int    `json:"id"`
	Addr string `json:"addr"` // host:port
}

// ServerError rejects a message (e.g. bad token).
type ServerError struct {
	Type string `json:"type"` // "error"
	Msg  string `json:"msg"`
}

// ---- message direction: Server -> Browser (dashboard) ----

// Snapshot is the full dashboard state the server pushes to browsers over /ws.
// It is intentionally a complete snapshot: browsers are read-only viewers and
// the dataset is small (a few hundred agents at most), so incremental deltas
// are unnecessary complexity.
type Snapshot struct {
	Type   string                  `json:"type"` // "snapshot"
	TS     int64                   `json:"ts"`
	Agents []AgentState            `json:"agents"`
	Mesh   map[int]map[int]float64 `json:"mesh"` // from_id -> to_id -> latency_ms
}

// AgentState is one agent's current view in the dashboard.
type AgentState struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Online     bool   `json:"online"`
	LastSeen   int64  `json:"last_seen"`
	Hostname   string `json:"hostname"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	Kernel     string `json:"kernel"`
	Version    string `json:"version"`
	ProbeAddr  string `json:"probe_addr"`
	RemoteAddr string `json:"remote_addr"` // server's view of the agent's WS peer
	Report
}

// Now is a tiny convenience used by both sides for monotonic-ish unix time.
func Now() int64 { return time.Now().Unix() }
