package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// AgentRecord is one registered agent. The token is a pre-shared secret the
// agent presents in its Hello message; the server never accepts an agent it
// has not issued a token for.
type AgentRecord struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Token string `json:"token"`
}

// Registry is the file-backed agent registry. It is the trust boundary for the
// agent side: only agents with a token present here may connect and report.
//
// The on-disk format is a simple JSON array so an operator can inspect and
// hand-edit it. Concurrent access is guarded by a mutex.
type Registry struct {
	path   string
	mu     sync.RWMutex
	nextID int
	recs   []AgentRecord
}

// NewRegistry loads (or creates) the registry file at path.
func NewRegistry(path string) (*Registry, error) {
	r := &Registry{path: path}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Registry) load() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	data, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			r.recs = nil
			r.nextID = 1
			return nil
		}
		return err
	}
	if len(data) == 0 {
		r.nextID = 1
		return nil
	}
	if err := json.Unmarshal(data, &r.recs); err != nil {
		return fmt.Errorf("registry: parse %s: %w", r.path, err)
	}
	r.nextID = 1
	for _, rec := range r.recs {
		if rec.ID >= r.nextID {
			r.nextID = rec.ID + 1
		}
	}
	return nil
}

func (r *Registry) save() error {
	data, err := json.MarshalIndent(r.recs, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// Add creates a new agent record with the given name and a fresh random token.
// It returns the record (caller prints the token once — it is never shown again).
func (r *Registry) Add(name string) (AgentRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := AgentRecord{ID: r.nextID, Name: name}
	tok, err := randomToken()
	if err != nil {
		return AgentRecord{}, err
	}
	rec.Token = tok
	r.recs = append(r.recs, rec)
	r.nextID++
	if err := r.save(); err != nil {
		return AgentRecord{}, err
	}
	return rec, nil
}

// Lookup validates a token and returns the matching record, or false.
func (r *Registry) Lookup(token string) (AgentRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, rec := range r.recs {
		if rec.Token == token {
			return rec, true
		}
	}
	return AgentRecord{}, false
}

// All returns a copy of the registry (for listing in the admin UI/CLI).
func (r *Registry) All() []AgentRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]AgentRecord, len(r.recs))
	copy(out, r.recs)
	return out
}

// Remove deletes an agent by ID (used by the CLI to revoke a token).
func (r *Registry) Remove(id int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.recs[:0]
	for _, rec := range r.recs {
		if rec.ID != id {
			out = append(out, rec)
		}
	}
	r.recs = out
	return r.save()
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
