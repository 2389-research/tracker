// ABOUTME: Tests for RunManager — concurrent runs, isolation, capacity, cancel (#479).
package tracker

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// quickDip is a passthrough pipeline that completes immediately.
const quickDip = `workflow quick
  start: A
  exit: B

  agent A
    label: A

  agent B
    label: B

  edges
    A -> B
`

// blockDip holds at a freeform human gate until the interviewer returns.
const blockDip = `workflow blockrun
  start: Begin
  exit: Done

  agent Begin
    label: Begin

  human Gate
    label: "hold"
    mode: freeform

  agent Done
    label: Done

  edges
    Begin -> Gate
    Gate -> Done
`

// blockingInterviewer blocks in the gate until released or the pipeline context
// is cancelled, so a test can hold a run in the running state.
type blockingInterviewer struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	pctx    context.Context
}

func newBlockingInterviewer() *blockingInterviewer {
	return &blockingInterviewer{entered: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockingInterviewer) SetPipelineContext(ctx context.Context) { b.pctx = ctx }

func (b *blockingInterviewer) Ask(string, []string, string) (string, error) { return b.block() }
func (b *blockingInterviewer) AskFreeform(string) (string, error)           { return b.block() }
func (b *blockingInterviewer) AskFreeformWithLabels(string, []string, string) (string, error) {
	return b.block()
}

func (b *blockingInterviewer) block() (string, error) {
	b.once.Do(func() { close(b.entered) })
	var ctxDone <-chan struct{}
	if b.pctx != nil {
		ctxDone = b.pctx.Done()
	}
	select {
	case <-b.release:
		return "released", nil
	case <-ctxDone:
		return "", context.Canceled
	}
}

func waitDone(t *testing.T, m *ManagedRun, d time.Duration) {
	t.Helper()
	select {
	case <-m.Done():
	case <-time.After(d):
		t.Fatalf("run %q did not finish within %s (state=%s)", m.Key, d, m.State())
	}
}

func TestRunManager_ConcurrentIsolatedRuns(t *testing.T) {
	base := t.TempDir()
	rm := NewRunManager(WithWorkDirBase(base))

	var runs []*ManagedRun
	for _, key := range []string{"alpha", "beta", "gamma"} {
		m, err := rm.Start(context.Background(), key, quickDip, Config{
			Format:    "dip",
			LLMClient: successStub(),
		})
		if err != nil {
			t.Fatalf("Start(%q): %v", key, err)
		}
		runs = append(runs, m)
	}

	seen := map[string]bool{}
	for _, m := range runs {
		waitDone(t, m, 10*time.Second)
		if m.State() != RunSucceeded {
			res, err := m.Result()
			t.Fatalf("run %q state=%s (err=%v, status=%v)", m.Key, m.State(), err, res)
		}
		if !strings.HasPrefix(m.WorkDir, base) {
			t.Fatalf("run %q workdir %q not under base %q", m.Key, m.WorkDir, base)
		}
		if seen[m.WorkDir] {
			t.Fatalf("run %q reused workdir %q", m.Key, m.WorkDir)
		}
		seen[m.WorkDir] = true
	}
	// The readable prefix is the sanitized key; a short hash suffix makes the
	// segment injective (see workdirSegment / TestWorkdirSegment_NoCollision).
	if got := filepath.Base(runs[0].WorkDir); !strings.HasPrefix(got, "alpha-") {
		t.Fatalf("expected per-key workdir name prefixed by the key, got %q", got)
	}
}

func TestRunManager_CapacityAndKeyGuards(t *testing.T) {
	rm := NewRunManager(WithMaxConcurrent(1), WithWorkDirBase(t.TempDir()))
	iv := newBlockingInterviewer()

	m1, err := rm.Start(context.Background(), "one", blockDip, Config{
		Format: "dip", LLMClient: successStub(), Interviewer: iv,
	})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	<-iv.entered // run is now blocked in the gate, holding the only slot

	// Duplicate active key is rejected.
	if _, err := rm.Start(context.Background(), "one", blockDip, Config{Format: "dip", LLMClient: successStub()}); err != ErrRunKeyActive {
		t.Fatalf("duplicate key: got %v, want ErrRunKeyActive", err)
	}
	// A different key at capacity is rejected.
	if _, err := rm.Start(context.Background(), "two", quickDip, Config{Format: "dip", LLMClient: successStub()}); err != ErrAtCapacity {
		t.Fatalf("at capacity: got %v, want ErrAtCapacity", err)
	}

	// Release the blocking run; its slot frees.
	close(iv.release)
	waitDone(t, m1, 10*time.Second)

	// Now a new run is admitted, and the finished key can be reused.
	m2, err := rm.Start(context.Background(), "one", quickDip, Config{Format: "dip", LLMClient: successStub()})
	if err != nil {
		t.Fatalf("reuse key after finish: %v", err)
	}
	waitDone(t, m2, 10*time.Second)
	if m2.State() != RunSucceeded {
		t.Fatalf("reused run state=%s", m2.State())
	}
}

// TestRunManager_PausedBillingIsResumable asserts a run whose engine stops in
// the recoverable paused_billing terminal (#487) is reported as RunPaused — NOT
// RunFailed — and exposes a resume id a control plane can feed back as
// Config.ResumeRunID. Reporting it as failed would tell an embedder the run is
// dead when in fact "add credit and resume" recovers it.
func TestRunManager_PausedBillingIsResumable(t *testing.T) {
	rm := NewRunManager(WithWorkDirBase(t.TempDir()))

	m, err := rm.Start(context.Background(), "brokerun", costDip, Config{
		Format:      "dip",
		LLMClient:   &failingCompleter{err: errors.New("your credit balance is too low")},
		RetryPolicy: "none",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitDone(t, m, 10*time.Second)

	res, _ := m.Result()
	if res == nil || res.Status != string(pipeline.OutcomePausedBilling) {
		t.Fatalf("engine status = %v, want %q", res, pipeline.OutcomePausedBilling)
	}
	if got := m.State(); got != RunPaused {
		t.Fatalf("state = %s, want %s", got, RunPaused)
	}
	if !m.State().Terminal() {
		t.Fatal("RunPaused must be Terminal() — it is finished-but-resumable")
	}
	resumeID := m.ResumeRunID()
	if resumeID == "" {
		t.Fatal("ResumeRunID() empty for a paused run — a caller cannot resume it")
	}
	if resumeID != res.RunID {
		t.Fatalf("ResumeRunID() = %q, want run id %q", resumeID, res.RunID)
	}

	// A paused run is finished, so the active-key guard lets the caller start the
	// resume attempt under the same key (a non-Terminal paused state would strand
	// it here with ErrRunKeyActive). Wait for the second run so no goroutine
	// outlives the test's temp dir; its own outcome is not what's under test.
	resumed, err := rm.Start(context.Background(), "brokerun", quickDip, Config{
		Format: "dip", LLMClient: successStub(), ResumeRunID: resumeID,
	})
	if err != nil {
		t.Fatalf("restart under a paused key: %v", err)
	}
	waitDone(t, resumed, 10*time.Second)
}

// TestManagedRun_ResumeRunIDOnlyWhenPaused asserts the resume handle is empty
// for non-paused states, so a caller cannot mistake a succeeded/failed run's id
// for a resumable one.
func TestManagedRun_ResumeRunIDOnlyWhenPaused(t *testing.T) {
	rm := NewRunManager(WithWorkDirBase(t.TempDir()))
	m, err := rm.Start(context.Background(), "ok", quickDip, Config{Format: "dip", LLMClient: successStub()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitDone(t, m, 10*time.Second)
	if m.State() != RunSucceeded {
		t.Fatalf("state = %s, want %s", m.State(), RunSucceeded)
	}
	if got := m.ResumeRunID(); got != "" {
		t.Fatalf("ResumeRunID() = %q for a succeeded run, want \"\"", got)
	}
}

// TestRunManager_ResumeInTerminalReleaseWindow guards #516: a same-key resume
// fired the instant a run reaches a terminal state — the exact window a control
// plane resuming a paused_billing run at its concurrency ceiling hits — must not
// get a spurious, retryable ErrAtCapacity. The active-key guard admits the
// resume as soon as State().Terminal() is true; if the finished run's capacity
// slot were released only afterward, that resume would falsely hit the cap.
// Correct ordering releases the slot BEFORE publishing the terminal state, so
// the slot is guaranteed free by the time the resume is admitted.
func TestRunManager_ResumeInTerminalReleaseWindow(t *testing.T) {
	cfg := Config{Format: "dip", LLMClient: successStub()}
	for i := 0; i < 300; i++ {
		rm := NewRunManager(WithMaxConcurrent(1), WithWorkDirBase(t.TempDir()))
		m, err := rm.Start(context.Background(), "k", quickDip, cfg)
		if err != nil {
			t.Fatalf("iteration %d: first Start: %v", i, err)
		}
		// Spin until the run publishes a terminal state, then race a same-key
		// resume against the finished run's teardown.
		for !m.State().Terminal() {
			runtime.Gosched()
		}
		m2, err := rm.Start(context.Background(), "k", quickDip, cfg)
		if errors.Is(err, ErrAtCapacity) {
			t.Fatalf("iteration %d: spurious ErrAtCapacity resuming a terminal run under cap 1", i)
		}
		if err != nil {
			t.Fatalf("iteration %d: resume Start: %v", i, err)
		}
		waitDone(t, m, 10*time.Second)
		waitDone(t, m2, 10*time.Second)
	}
}

// TestRunManager_FailedRunClassifiesAsFailed guards #516: a genuine run failure
// (provider hard-fail, RetryPolicy none — res.Status=fail with a nil Run error)
// must surface as RunFailed, NOT RunCanceled. The execute defer cancels the
// run's own context during teardown; classifyFinalState must snapshot the
// cancellation signal BEFORE that cancel, or ctx.Err() reads non-nil for every
// finished run and misclassifies every failure as canceled — a user-visible
// wrong terminal state for an embedder (Slack/web control plane).
func TestRunManager_FailedRunClassifiesAsFailed(t *testing.T) {
	rm := NewRunManager(WithWorkDirBase(t.TempDir()))

	m, err := rm.Start(context.Background(), "boom", costDip, Config{
		Format:      "dip",
		LLMClient:   &failingCompleter{err: errors.New("provider exploded")},
		RetryPolicy: "none",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitDone(t, m, 10*time.Second)

	res, rerr := m.Result()
	if rerr != nil {
		t.Fatalf("Result error = %v, want nil (a failure halt returns fail with a nil error)", rerr)
	}
	if res == nil || res.Status != string(pipeline.OutcomeFail) {
		t.Fatalf("engine status = %v, want %q", res, pipeline.OutcomeFail)
	}
	if got := m.State(); got != RunFailed {
		t.Fatalf("state = %s, want %s", got, RunFailed)
	}
}

func TestRunManager_Cancel(t *testing.T) {
	rm := NewRunManager(WithWorkDirBase(t.TempDir()))
	iv := newBlockingInterviewer()

	m, err := rm.Start(context.Background(), "cancelme", blockDip, Config{
		Format: "dip", LLMClient: successStub(), Interviewer: iv,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-iv.entered

	if !rm.Cancel("cancelme") {
		t.Fatal("Cancel returned false for an active run")
	}
	waitDone(t, m, 10*time.Second)
	if m.State() != RunCanceled {
		t.Fatalf("state after cancel = %s, want %s", m.State(), RunCanceled)
	}

	// Forget drops the finished run; Get no longer finds it.
	if !rm.Forget("cancelme") {
		t.Fatal("Forget returned false for a finished run")
	}
	if _, ok := rm.Get("cancelme"); ok {
		t.Fatal("run still tracked after Forget")
	}
	if rm.Cancel("missing") {
		t.Fatal("Cancel returned true for an unknown key")
	}
}

func TestWorkdirSegment_NoCollision(t *testing.T) {
	// Distinct keys that sanitizeKey folds to the same segment must still map to
	// distinct workdir segments. The active-key guard keys on the RAW key, so two
	// such keys both pass the guard; if they also share a workdir, two concurrent
	// runs corrupt one working tree.
	pairs := [][2]string{
		{"feature/x", "feature_x"},
		{"a/b", "a.b"},
		{"team:one", "team_one"},
	}
	for _, p := range pairs {
		if sanitizeKey(p[0]) != sanitizeKey(p[1]) {
			t.Fatalf("test bug: %q and %q do not collide under sanitizeKey", p[0], p[1])
		}
		if s0, s1 := workdirSegment(p[0]), workdirSegment(p[1]); s0 == s1 {
			t.Fatalf("workdirSegment collision: %q and %q both -> %q", p[0], p[1], s0)
		}
	}
	// Determinism: the same key must map to the same segment so a resumed run
	// lands in the same workdir.
	if workdirSegment("feature/x") != workdirSegment("feature/x") {
		t.Fatal("workdirSegment is not deterministic")
	}
}
