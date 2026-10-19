// ABOUTME: BackendResolver resolves a node's `backend: auto` once per run and shares the
// ABOUTME: answer across parallel branches and subgraph children; seeded on resume.
package pipeline

import (
	"fmt"
	"sync"
)

// BackendResolution is what `backend: auto` resolved to for a run.
type BackendResolution struct {
	Backend       string // "claude-code" or "native"
	Reason        string // human-readable, shown in the announcement
	ClaudePath    string // set when Backend is claude-code
	ClaudeVersion string // `claude --version` output, when probed
	FromRecord    bool   // seeded from the checkpoint rather than probed
}

// BackendResolver holds a run's `backend: auto` resolution. One resolver is
// created per run and handed to the engine and to every handler registry the
// run builds, including subgraph children, so the probe runs at most once.
type BackendResolver struct {
	mu        sync.Mutex
	res       BackendResolution
	resolved  bool
	announced bool
}

// NewBackendResolver returns an unresolved resolver.
func NewBackendResolver() *BackendResolver { return &BackendResolver{} }

// Resolve returns the run's resolution, calling probe only if nothing has been
// resolved or seeded yet. A probe error is returned and not cached, so a later
// node can try again (e.g. after the user installs claude mid-run).
func (r *BackendResolver) Resolve(probe func() (BackendResolution, error)) (BackendResolution, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.resolved {
		return r.res, nil
	}
	res, err := probe()
	if err != nil {
		return BackendResolution{}, err
	}
	r.res, r.resolved = res, true
	return res, nil
}

// Seed records a resolution read back from a checkpoint. Only "native" and
// "claude-code" are valid: `auto` never resolves to acp (whose environment
// passes through in full), so a checkpoint naming anything else was tampered
// with or is corrupt.
func (r *BackendResolver) Seed(backend string) error {
	if backend != "native" && backend != "claude-code" {
		return fmt.Errorf("checkpoint backend_resolved %q is not a backend auto can resolve to (want native or claude-code)", backend)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.res = BackendResolution{Backend: backend, Reason: "recorded in the checkpoint", FromRecord: true}
	r.resolved = true
	return nil
}

// Result reports the resolution, if any.
func (r *BackendResolver) Result() (BackendResolution, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.res, r.resolved
}

// ClaimAnnouncement reports true exactly once per run: the caller that gets
// true emits EventBackendResolved, so a run with several auto nodes, parallel
// branches or subgraph children announces its backend once.
func (r *BackendResolver) ClaimAnnouncement() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.announced {
		return false
	}
	r.announced = true
	return true
}

// WithBackendResolver gives the engine the run's resolver so it can seed it on
// resume and persist its result in the checkpoint.
func WithBackendResolver(r *BackendResolver) EngineOption {
	return func(e *Engine) { e.backendResolver = r }
}

// seedBackendResolver restores a resumed run's backend: auto resolution from
// its checkpoint. A recorded value other than native / claude-code fails the
// resume (BackendResolver.Seed). --backend still wins: the codergen handler
// consults it before the resolver.
func (e *Engine) seedBackendResolver(cp *Checkpoint) error {
	if e.backendResolver == nil || cp.BackendResolved == "" {
		return nil
	}
	if err := e.backendResolver.Seed(cp.BackendResolved); err != nil {
		return fmt.Errorf("resume: %w", err)
	}
	return nil
}

// stampBackendResolved copies the run's resolution onto the checkpoint so a
// resume reuses it rather than probing again.
func (e *Engine) stampBackendResolved(cp *Checkpoint) {
	if e.backendResolver == nil {
		return
	}
	if res, ok := e.backendResolver.Result(); ok {
		cp.BackendResolved = res.Backend
	}
}
