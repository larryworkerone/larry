package server

import (
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/larry-probe/larry/web"
)

// Config configures the Larry server.
type Config struct {
	Addr            string        // listen address, e.g. ":80"
	RegistryPath    string        // path to agents.json
	BroadcastPeriod time.Duration // browser snapshot push interval
	OfflineTTL      time.Duration // report staleness -> offline
	MeshInterval    time.Duration // mesh round interval
}

// Server is the assembled Larry dashboard: HTTP routes + connection hub +
// live store + mesh scheduler. Construct it with New and Run it.
type Server struct {
	cfg      Config
	registry *Registry
	store    *Store
	hub      *Hub
	mesh     *MeshScheduler
}

// New builds a server from config: it loads the registry and wires the hub,
// store, and mesh scheduler. It does not start listening — call Run.
func New(cfg Config) (*Server, error) {
	registry, err := NewRegistry(cfg.RegistryPath)
	if err != nil {
		return nil, err
	}
	store := NewStore()
	hub := NewHub(registry, store)
	mesh := NewMeshScheduler(hub, store, cfg.MeshInterval)
	return &Server{cfg: cfg, registry: registry, store: store, hub: hub, mesh: mesh}, nil
}

// Registry exposes the agent registry so the CLI can manage tokens.
func (s *Server) Registry() *Registry { return s.registry }

// Run starts all background goroutines and the HTTP server. It blocks until
// the server stops accepting connections.
func (s *Server) Run() error {
	go s.hub.RunBroadcaster(s.cfg.BroadcastPeriod)
	go s.hub.RunSweeper(10*time.Second, s.cfg.OfflineTTL)
	go s.mesh.Run()

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/static/", s.handleStatic)
	mux.HandleFunc("/ws", s.hub.HandleBrowser)
	mux.HandleFunc("/api/agent", s.hub.HandleAgent)
	mux.HandleFunc("/api/snapshot", s.handleSnapshot)
	mux.HandleFunc("/api/agents", s.handleAgentsList)
	mux.HandleFunc("/api/tokens", s.handleTokens)

	log.Printf("server: Larry 探针 listening on %s (registry: %s)", s.cfg.Addr, s.cfg.RegistryPath)
	srv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(web.IndexHTML)
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	// strip "/static/" prefix and serve from the embedded FS.
	name := strings.TrimPrefix(r.URL.Path, "/static/")
	if name == "" {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(web.Static, "static/"+name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentTypeFor(name))
	_, _ = w.Write(data)
}

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.store.Snapshot())
}

func (s *Server) handleAgentsList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.registry.All())
}

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(s.registry.All())

	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
			http.Error(w, `{"error":"invalid name"}`, http.StatusBadRequest)
			return
		}
		rec, err := s.registry.Add(strings.TrimSpace(req.Name))
		if err != nil {
			http.Error(w, `{"error":"failed to add token"}`, http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(rec)

	case http.MethodDelete:
		idStr := r.URL.Query().Get("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			http.Error(w, `{"error":"invalid id"}`, http.StatusBadRequest)
			return
		}
		if err := s.registry.Remove(id); err != nil {
			http.Error(w, `{"error":"failed to remove token"}`, http.StatusInternalServerError)
			return
		}
		s.store.DeleteAgent(id)
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// contentTypeFor maps the few asset extensions we serve to MIME types.
func contentTypeFor(name string) string {
	switch {
	case strings.HasSuffix(name, ".js"):
		return "application/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}
