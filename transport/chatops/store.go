// ABOUTME: Persists active runs so they can be resumed after a bot process restart.
// ABOUTME: A JSON file maps thread_ts → the info needed to re-launch from checkpoint.
package chatops

import (
	"encoding/json"
	"log"
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
// nil *Store is a valid no-op (persistence disabled).
type Store struct {
	path string
	mu   sync.Mutex
	recs map[string]RunRecord
}

// OpenStore loads (or starts) the store at path. A missing file yields an empty
// store (fresh start). A *corrupt* file is not silently dropped — that would
// lose every resumable run without a trace; it is preserved aside and logged
// loudly, then the bot starts with no resumable runs (an operator can recover
// the file and restart).
func OpenStore(path string) *Store {
	s := &Store{path: path, recs: make(map[string]RunRecord)}
	data, err := os.ReadFile(path)
	if err != nil {
		return s // missing/unreadable file — normal on first run
	}
	var recs []RunRecord
	if err := json.Unmarshal(data, &recs); err != nil {
		aside := path + ".corrupt"
		_ = os.Rename(path, aside)
		log.Printf("trackerbot: state file %s is corrupt (%v); moved it to %s and starting with no resumable runs", path, err, aside)
		return s
	}
	for _, r := range recs {
		s.recs[r.ThreadTS] = r
	}
	return s
}

// put records (or replaces) a run and persists.
func (s *Store) put(rec RunRecord) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs[rec.ThreadTS] = rec
	s.flush()
}

// remove drops a run (on completion) and persists.
func (s *Store) remove(threadTS string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.recs, threadTS)
	s.flush()
}

// list returns the recorded runs, ordered by thread_ts.
func (s *Store) List() []RunRecord {
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
func (s *Store) flush() {
	if s.path == "" {
		return
	}
	out := make([]RunRecord, 0, len(s.recs))
	for _, r := range s.recs {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ThreadTS < out[j].ThreadTS })
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return
	}
	// Atomic write: a crash mid-write must not corrupt the state file (which
	// would drop every resumable run — see OpenStore). Temp + rename so a
	// reader always sees a complete file.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
	}
}
