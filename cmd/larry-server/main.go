// Command larry-server runs the Larry dashboard. It serves the embedded web
// UI, accepts agent WebSocket connections, runs the mesh scheduler, and
// exposes a small token-management CLI.
//
// Usage:
//
//	larry-server                      # start dashboard on default :80
//	larry-server token add <name>     # mint a new agent token (prints once)
//	larry-server token list           # show registered agents
//	larry-server token remove <id>    # revoke an agent token
//	larry-server version
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/larry-probe/larry/internal/server"
)

// Version is overridable via -ldflags "-X main.Version=…".
var Version = "dev"

const defaultRegistry = "agents.json"

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "token":
			tokenCLI(os.Args[2:])
			return
		case "version", "-version", "--version":
			fmt.Println("larry-server", Version)
			return
		}
	}

	addr := flag.String("addr", ":80", "listen address")
	registry := flag.String("registry", defaultRegistry, "path to agents.json")
	broadcast := flag.Duration("broadcast", 2*time.Second, "browser snapshot push interval")
	mesh := flag.Duration("mesh-interval", 15*time.Second, "mesh latency probe interval")
	offline := flag.Duration("offline-ttl", 30*time.Second, "report staleness before marking offline")
	flag.Parse()

	srv, err := server.New(server.Config{
		Addr:            *addr,
		RegistryPath:    *registry,
		BroadcastPeriod: *broadcast,
		OfflineTTL:      *offline,
		MeshInterval:    *mesh,
	})
	if err != nil {
		log.Fatalf("larry-server: %v", err)
	}
	log.Printf("larry-server %s starting", Version)
	if err := srv.Run(); err != nil {
		log.Fatalf("larry-server: %v", err)
	}
}

// tokenCLI manages the agent registry offline (no server needed).
func tokenCLI(args []string) {
	if len(args) == 0 {
		tokenUsage()
		return
	}
	// re-parse a registry flag so operators can manage a non-default file
	regPath := defaultRegistry
	sub := args[0]
	rest := args[1:]
	// allow `token add name --registry x.json`
	if len(rest) >= 2 && (rest[len(rest)-2] == "--registry" || rest[len(rest)-2] == "-registry") {
		regPath = rest[len(rest)-1]
		rest = rest[:len(rest)-2]
	}
	switch sub {
	case "add":
		if len(rest) < 1 {
			fmt.Fprintln(os.Stderr, "usage: larry-server token add <name> [--registry path]")
			os.Exit(2)
		}
		name := rest[0]
		reg, err := server.NewRegistry(regPath)
		if err != nil {
			log.Fatalf("registry: %v", err)
		}
		rec, err := reg.Add(name)
		if err != nil {
			log.Fatalf("add: %v", err)
		}
		fmt.Printf("Agent registered.\n  ID:    %d\n  Name:  %s\n  Token: %s\n", rec.ID, rec.Name, rec.Token)
		fmt.Println("\nConfigure the agent:")
		fmt.Printf("  larry-agent --server ws://<dashboard-host> --token %s\n", rec.Token)
		fmt.Println("\n(This token is shown once. Find it again in the agents.json file.)")
	case "list":
		reg, err := server.NewRegistry(regPath)
		if err != nil {
			log.Fatalf("registry: %v", err)
		}
		all := reg.All()
		if len(all) == 0 {
			fmt.Println("(no agents registered) — run: larry-server token add <name>")
			return
		}
		fmt.Printf("%-4s %-20s %s\n", "ID", "NAME", "TOKEN")
		for _, r := range all {
			fmt.Printf("%-4d %-20s %s…\n", r.ID, r.Name, shortToken(r.Token))
		}
	case "remove":
		if len(rest) < 1 {
			fmt.Fprintln(os.Stderr, "usage: larry-server token remove <id>")
			os.Exit(2)
		}
		id, err := strconv.Atoi(rest[0])
		if err != nil {
			log.Fatalf("id must be a number")
		}
		reg, err := server.NewRegistry(regPath)
		if err != nil {
			log.Fatalf("registry: %v", err)
		}
		if err := reg.Remove(id); err != nil {
			log.Fatalf("remove: %v", err)
		}
		fmt.Printf("Agent %d removed.\n", id)
	default:
		tokenUsage()
	}
}

func tokenUsage() {
	fmt.Println(`larry-server token <command> [args]

commands:
  add <name> [--registry path]   mint a new agent token
  list        [--registry path]   list registered agents
  remove <id> [--registry path]   revoke an agent token`)
}

func shortToken(t string) string {
	if len(t) <= 8 {
		return t
	}
	return t[:8]
}
