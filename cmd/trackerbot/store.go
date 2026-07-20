// ABOUTME: Persists active runs so they can be resumed after a bot process restart.
// ABOUTME: A JSON file maps thread_ts → the info needed to re-launch from checkpoint.
package main

import (
	"encoding/json"
	"os"
	"sort"
	"sync"
)

// RunRecord is what's needed to resume a run after a restart: which thread and
// channel it belongs to, and which workflow (+ params) it ran. The workdir and
// checkpoint path are derived deterministically from the thread_ts.
type RunRecord struct {
	ThreadTS string            `json:"thread_ts"`
	Channel  string            `json:"channel"`
	Workflow string            `json:"workflow"`
	Params   map[string]string `json:"params,omitempty"`
}

// store is a small JSON-file-backed set of active runs, keyed by thread_ts. A
// nil *store is a valid no-op (persistence disabled).
type store struct {
	path string
	mu   sync.Mutex
	recs map[string]RunRecord
}

// openStore loads (or starts) the store at path. A read error (e.g. missing
// file) yields an empty store, not an error.
func openStore(path string) *store {
	s := &store{path: path, recs: make(map[string]RunRecord)}
	if data, err := os.ReadFile(path); err == nil {
		var recs []RunRecord
		if json.Unmarshal(data, &recs) == nil {
			for _, r := range recs {
				s.recs[r.ThreadTS] = r
			}
		}
	}
	return s
}

// put records (or replaces) a run and persists.
func (s *store) put(rec RunRecord) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs[rec.ThreadTS] = rec
	s.flush()
}

// remove drops a run (on completion) and persists.
func (s *store) remove(threadTS string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.recs, threadTS)
	s.flush()
}

// list returns the recorded runs, ordered by thread_ts.
func (s *store) list() []RunRecord {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RunRecord, 0, len(s.recs))
	for _, r := range s.recs {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ThreadTS < out[j].ThreadTS })
	return out
}

// flush writes the current set to disk. Caller holds the lock. Best-effort.
func (s *store) flush() {
	if s.path == "" {
		return
	}
	out := make([]RunRecord, 0, len(s.recs))
	for _, r := range s.recs {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ThreadTS < out[j].ThreadTS })
	if data, err := json.MarshalIndent(out, "", "  "); err == nil {
		_ = os.WriteFile(s.path, data, 0o600)
	}
}
