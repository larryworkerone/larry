// Command larry-agent runs the Larry monitoring agent. It collects system
// metrics, reports them to a Larry dashboard over WebSocket, and serves a
// small TCP probe port that other agents dial to measure pairwise latency.
//
// Usage:
//
//	larry-agent --server ws://dashboard-host:80 --token <token>
//	larry-agent --server ws://1.2.3.4:80 --token <tok> --probe-host 1.2.3.4
//	larry-agent version
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/larry-probe/larry/internal/agent"
)

// Version is overridable via -ldflags "-X main.Version=…".
var Version = "dev"

func main() {
	if len(os.Args) >= 2 && (os.Args[1] == "version" || os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Println("larry-agent", Version)
		return
	}

	cfg := agent.Config{}
	flag.StringVar(&cfg.ServerURL, "server", "", "dashboard WebSocket URL, e.g. ws://host:80")
	flag.StringVar(&cfg.Token, "token", "", "agent token (from `larry-server token add`)")
	flag.StringVar(&cfg.ProbeAddr, "probe-addr", ":36510", "mesh probe TCP listen address")
	flag.StringVar(&cfg.ProbeHost, "probe-host", "", "reachable host others use to dial the probe port (auto-detected from server side if empty)")
	flag.IntVar(&cfg.Interval, "interval", 5, "report interval, seconds (server may override)")
	flag.StringVar(&cfg.Name, "name", "", "override reported hostname")
	flag.BoolVar(&cfg.Once, "once", false, "run a single session then exit (self-test)")
	flag.Parse()

	cfg.ServerURL = os.Expand(cfg.ServerURL, os.Getenv)
	agent.Version = Version
	agent.Run(cfg)
}
