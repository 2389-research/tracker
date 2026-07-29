// ABOUTME: RunManager owns multiple concurrent pipeline runs keyed by an external id.
// ABOUTME: Transport-neutral infra for services (Slack/web) that drive many runs at once (#479).
package tracker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"

	"github.com/2389-research/tracker/pipeline"
)

// RunState is the lifecycle state of a managed run.
type RunState string

// The set of states is an OPEN enum — future minor releases may add more (e.g.
// a blocked-on-human-gate state). Classify with Terminal() and compare against
// the states you care about; do not switch exhaustively.
const (
	RunStarting  RunState = "starting"
	RunRunning   RunState = "running"
	RunSucceeded RunState = "succeeded"
	RunFailed    RunState = "failed"
	RunCanceled  RunState = "canceled"
	// RunPaused is a run that stopped in a recoverable, resumable terminal —
	// today only the engine's paused_billing halt (#487): the checkpoint is
	// saved, in-flight work preserved, and the run continues from the paused
	// node once credits are topped up. It is NOT a failure: an embedder should
	// surface "add credit and resume", not "this run is dead". Feed
	// ManagedRun.ResumeRunID() back as Config.ResumeRunID to resume.
	RunPaused RunState = "paused"
)

// Terminal reports whether the state is a finished state — the run's goroutine
// has exited, Done() is closed, and Result() is populated.
//
// RunPaused counts as Terminal even though the *work* is unfinished. It is
// "finished-but-resumable": this process's run really has ended, so a state
// where Terminal() were false would contradict a closed Done() channel, keep the
// key locked by the active-key guard in claim() (stranding the run — the caller
// could never start the resume under the same key), and make Forget() refuse it
// forever. Resumability is a separate axis from finishedness, surfaced by
// State() == RunPaused and ResumeRunID(), not by Terminal().
func (s RunState) Terminal() bool {
	return s == RunSucceeded || s == RunFailed || s == RunCanceled || s == RunPaused
}

var (
	// ErrRunKeyActive is returned by Start when a run with the same key is still active.
	ErrRunKeyActive = errors.New("a run with this key is already active")
	// ErrAtCapacity is returned by Start when the concurrency cap is reached. The
	// caller decides whether to queue, reject, or retry later.
	ErrAtCapacity = errors.New("run manager is at capacity")
)

// ManagedRun is a single run owned by a RunManager. Safe for concurrent reads.
type ManagedRun struct {
	Key     string // caller-chosen external id (e.g. a Slack thread_ts)
	WorkDir string // isolated working directory for this run

	mu       sync.Mutex
	state    RunState
	result   *Result
	err      error
	canceled bool
	cancel   context.CancelFunc
	done     chan struct{}
}

// State returns the run's current lifecycle state.
func (m *ManagedRun) State() RunState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// Done is closed when the run reaches a terminal state.
func (m *ManagedRun) Done() <-chan struct{} { return m.done }

// Result returns the run's result and error once it has finished. Before the
// run is Done, it returns (nil, nil). After Done it mirrors tracker.Run: a run
// that produced a terminal result has a non-nil *Result (with RunID and a
// terminal Status) — accompanied by a non-nil error for handler-error,
// strict-failure, paused (see RunPaused), or cancelled exits. A non-nil error
// therefore does not by itself mean the run failed; read State(). Only an
// init/invariant failure before any
// terminal result yields (nil, err).
func (m *ManagedRun) Result() (*Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.result, m.err
}

// RunID returns the tracker run id once the run has finished, else "".
func (m *ManagedRun) RunID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.result != nil {
		return m.result.RunID
	}
	return ""
}

// ResumeRunID returns the tracker run id to resume from when the run stopped in
// a recoverable paused state (State() == RunPaused), else "". Pass it as
// Config.ResumeRunID (with the same Config.CheckpointDir and this run's WorkDir)
// to continue from the paused node. Capture it before Forget() drops the run.
func (m *ManagedRun) ResumeRunID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != RunPaused || m.result == nil {
		return ""
	}
	return m.result.RunID
}

// RunManager owns multiple concurrent pipeline runs keyed by an external id.
// It provides the mechanism (isolation, lifecycle, an optional capacity cap)
// and leaves admission policy (queue vs reject at capacity) to the caller.
// Safe for concurrent use.
type RunManager struct {
	mu          sync.Mutex
	runs        map[string]*ManagedRun
	sem         chan struct{} // capacity cap; nil = unbounded
	workDirBase string        // base dir for per-run isolated workdirs when cfg.WorkingDir is empty
}

// RunManagerOption configures a RunManager.
type RunManagerOption func(*RunManager)

// WithMaxConcurrent caps the number of simultaneously-active runs. When the cap
// is reached, Start returns ErrAtCapacity. A value <= 0 means unbounded.
func WithMaxConcurrent(n int) RunManagerOption {
	return func(rm *RunManager) {
		if n > 0 {
			rm.sem = make(chan struct{}, n)
		}
	}
}

// WithWorkDirBase sets a base directory under which each run without an explicit
// Config.WorkingDir gets its own isolated subdirectory (base/<sanitized-key>).
// Isolated workdirs keep concurrent runs from colliding on run-state files.
func WithWorkDirBase(dir string) RunManagerOption {
	return func(rm *RunManager) { rm.workDirBase = dir }
}

// NewRunManager creates a RunManager.
func NewRunManager(opts ...RunManagerOption) *RunManager {
	rm := &RunManager{runs: make(map[string]*ManagedRun)}
	for _, opt := range opts {
		opt(rm)
	}
	return rm
}

// Start launches source under cfg as a new run keyed by key. The run executes in
// its own goroutine; ctx bounds its lifetime, so pass a long-lived context (not
// a short request context) and use Cancel to stop a single run. Returns
// ErrRunKeyActive if a run with the same key is still active, or ErrAtCapacity
// if the concurrency cap is reached — the caller decides whether to queue or
// reject. When cfg.WorkingDir is empty and a workdir base is configured, an
// isolated per-key directory is created.
func (rm *RunManager) Start(ctx context.Context, key, source string, cfg Config) (*ManagedRun, error) {
	// Prepare the isolated workdir first so the ManagedRun's WorkDir is final
	// before the run is published — its exported fields are then immutable and
	// safe to read without locking.
	if cfg.WorkingDir == "" && rm.workDirBase != "" {
		dir := filepath.Join(rm.workDirBase, sanitizeKey(key))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("run manager: create workdir for %q: %w", key, err)
		}
		cfg.WorkingDir = dir
	}

	runCtx, cancel := context.WithCancel(ctx)
	m := &ManagedRun{Key: key, WorkDir: cfg.WorkingDir, state: RunStarting, cancel: cancel, done: make(chan struct{})}

	if err := rm.claim(key, m); err != nil {
		cancel()
		return nil, err
	}

	go rm.execute(runCtx, m, source, cfg)
	return m, nil
}

// claim atomically registers m under key, honoring the active-key guard and the
// capacity cap so concurrent Start(key) calls cannot both publish a run for the
// same key (two runs in one workdir). Returns ErrRunKeyActive or ErrAtCapacity
// on rejection.
func (rm *RunManager) claim(key string, m *ManagedRun) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if existing, ok := rm.runs[key]; ok && !existing.State().Terminal() {
		return ErrRunKeyActive
	}
	if rm.sem != nil {
		select {
		case rm.sem <- struct{}{}:
		default:
			return ErrAtCapacity
		}
	}
	rm.runs[key] = m
	return nil
}

// release frees a capacity slot (if a cap is configured).
func (rm *RunManager) release() {
	if rm.sem != nil {
		select {
		case <-rm.sem:
		default:
		}
	}
}

// execute runs the pipeline and records the terminal state.
func (rm *RunManager) execute(ctx context.Context, m *ManagedRun, source string, cfg Config) {
	m.setState(RunRunning)

	var (
		res *Result
		err error
	)

	// Always release the slot, publish the terminal state, close done, and
	// cancel — even if Run panics or Goexits — so one bad run can neither crash
	// the service nor leak capacity nor hang callers blocked on Done()/Result()
	// (Phase 0). This is belt-and-suspenders over Engine.Run's own panic
	// recovery: it also catches a panic raised outside the engine (e.g. during
	// source parsing).
	//
	// Ordering matters (#516). The capacity slot MUST be released BEFORE the
	// terminal state is published, and the terminal state BEFORE done closes:
	//   1. cancel the context (frees its resources);
	//   2. release the slot — claim() admits a same-key resume the instant
	//      State().Terminal() is true (it is past the active-key guard by
	//      then), so the slot has to already be free or that resume gets a
	//      spurious, retryable ErrAtCapacity under a tight cap;
	//   3. publish the terminal state and result under m.mu;
	//   4. close done — a closed Done() must imply Terminal() (see
	//      RunState.Terminal), so the state has to be visible first.
	// Between steps 2 and 3 the state is still RunRunning, so a same-key Start
	// sees ErrRunKeyActive (not a slot rejection) and no double-start is
	// admitted; a different-key Start may take the freed slot without exceeding
	// the cap, since this run's goroutine is only finishing teardown.
	defer func() {
		r := recover()

		m.mu.Lock()
		cancel := m.cancel
		m.mu.Unlock()

		// Snapshot the context's cancellation signal BEFORE cancel() (#516).
		// cancel() cancels this run's OWN context, so reading ctx.Err() after it
		// would be non-nil for every finished run — misclassifying every genuine
		// failure as RunCanceled. Capture it here while it still distinguishes a
		// caller-initiated cancel from a normal completion.
		ctxCanceled := ctx.Err() != nil

		cancel() // release the context's resources

		rm.release()

		m.mu.Lock()
		if r != nil {
			m.err = fmt.Errorf("panic in run %q: %v\n%s", m.Key, r, debug.Stack())
			m.state = RunFailed
		} else {
			m.result, m.err = res, err
			m.state = m.classifyFinalState(ctxCanceled, res, err)
		}
		m.mu.Unlock()

		close(m.done)
	}()

	res, err = Run(ctx, source, cfg)
}

// classifyFinalState maps a finished Run's (result, error) onto a terminal
// lifecycle state. Caller must hold m.mu (it reads m.canceled). ctxCanceled is
// the run context's cancellation signal snapshotted BEFORE the run's own cancel
// fires (#516) — reading ctx.Err() here directly would always be non-nil.
func (m *ManagedRun) classifyFinalState(ctxCanceled bool, res *Result, err error) RunState {
	switch {
	case err == nil && res != nil && pipeline.TerminalStatus(res.Status).IsSuccess():
		// A genuine success wins even if the context was cancelled in the
		// completion window — the result is a real success, so State() must not
		// disagree with a successful Result().
		return RunSucceeded
	case res != nil && res.Status == string(pipeline.OutcomePausedBilling):
		// Same reasoning as success: the engine reached an authoritative
		// terminal, so it outranks a cancellation observed in the completion
		// window. paused_billing arrives WITH a non-nil error (the billing
		// error rides out on Run's error return), so it must be classified from
		// the status — not from err == nil — or it lands in RunFailed and the
		// embedder loses the resume affordance.
		return RunPaused
	case m.canceled || ctxCanceled:
		return RunCanceled
	default:
		return RunFailed
	}
}

func (m *ManagedRun) setState(s RunState) {
	m.mu.Lock()
	m.state = s
	m.mu.Unlock()
}

// Get returns the managed run for key, if present.
func (rm *RunManager) Get(key string) (*ManagedRun, bool) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	m, ok := rm.runs[key]
	return m, ok
}

// List returns all runs the manager currently tracks, ordered by key.
func (rm *RunManager) List() []*ManagedRun {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	out := make([]*ManagedRun, 0, len(rm.runs))
	for _, m := range rm.runs {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Cancel stops the run for key by cancelling its context. Returns false if no
// such run is tracked. Cancelling an already-finished run is a no-op.
func (rm *RunManager) Cancel(key string) bool {
	m, ok := rm.Get(key)
	if !ok {
		return false
	}
	m.mu.Lock()
	m.canceled = true
	cancel := m.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return true
}

// Forget drops a finished run from the manager's tracking. Returns false if the
// run is unknown or still active (active runs cannot be forgotten; Cancel first).
func (rm *RunManager) Forget(key string) bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	m, ok := rm.runs[key]
	if !ok || !m.State().Terminal() {
		return false
	}
	delete(rm.runs, key)
	return true
}

// sanitizeKey turns an arbitrary external id into a safe single path segment.
func sanitizeKey(key string) string {
	repl := func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}
	out := strings.Map(repl, key)
	if out == "" {
		return "run"
	}
	return out
}
