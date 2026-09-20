// ABOUTME: Tests for the Herdr pane lifecycle reporter.
// ABOUTME: Drives the reporter through an injected fake runner so no herdr binary is spawned.
package herdr

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// fakeRunner records every herdr CLI invocation instead of executing it.
type fakeRunner struct {
	mu    sync.Mutex
	calls []recordedCall
	err   error
}

type recordedCall struct {
	bin  string
	args []string
}

func (f *fakeRunner) run(_ context.Context, bin string, args []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, recordedCall{bin: bin, args: append([]string(nil), args...)})
	return f.err
}

func (f *fakeRunner) snapshot() []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedCall(nil), f.calls...)
}

func testEnv() Env { return Env{PaneID: "w1:p1", BinPath: "/usr/bin/herdr"} }

// flagVal returns the value following --name in args.
func flagVal(args []string, name string) (string, bool) {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == name {
			return args[i+1], true
		}
	}
	return "", false
}

// reportedStates pulls the --state value from every report-agent call, in order.
func reportedStates(calls []recordedCall) []string {
	var out []string
	for _, c := range calls {
		if len(c.args) >= 2 && c.args[1] == "report-agent" {
			if v, ok := flagVal(c.args, "--state"); ok {
				out = append(out, v)
			}
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestReportsWorkingOnPipelineStarted(t *testing.T) {
	f := &fakeRunner{}
	r := newReporter(testEnv(), true, f.run)

	r.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventPipelineStarted, RunID: "run-123"})

	calls := f.snapshot()
	if len(calls) != 1 {
		t.Fatalf("want 1 call, got %d: %+v", len(calls), calls)
	}
	c := calls[0]
	if c.bin != "/usr/bin/herdr" {
		t.Errorf("bin = %q, want /usr/bin/herdr", c.bin)
	}
	if len(c.args) < 3 || c.args[0] != "pane" || c.args[1] != "report-agent" || c.args[2] != "w1:p1" {
		t.Fatalf("prefix args = %v, want [pane report-agent w1:p1 ...]", c.args)
	}
	if v, _ := flagVal(c.args, "--state"); v != "working" {
		t.Errorf("--state = %q, want working", v)
	}
	if v, _ := flagVal(c.args, "--source"); v != "custom:tracker" {
		t.Errorf("--source = %q, want custom:tracker", v)
	}
	if v, _ := flagVal(c.args, "--agent"); v != "tracker" {
		t.Errorf("--agent = %q, want tracker", v)
	}
	if v, _ := flagVal(c.args, "--agent-session-id"); v != "run-123" {
		t.Errorf("--agent-session-id = %q, want run-123", v)
	}
	if v, _ := flagVal(c.args, "--seq"); v != "1" {
		t.Errorf("--seq = %q, want 1", v)
	}
}

func TestHumanGateReportsBlockedThenWorking(t *testing.T) {
	f := &fakeRunner{}
	r := newReporter(testEnv(), true, f.run)

	r.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventPipelineStarted, RunID: "r"})
	r.HandlePipelineEvent(pipeline.PipelineEvent{
		Type: pipeline.EventGateOpened,
		Gate: &pipeline.GateDetail{GateID: "g1", Label: "Approve deploy?"},
	})
	r.HandlePipelineEvent(pipeline.PipelineEvent{
		Type: pipeline.EventGateResolved,
		Gate: &pipeline.GateDetail{GateID: "g1"},
	})

	if got := reportedStates(f.snapshot()); !equalStrings(got, []string{"working", "blocked", "working"}) {
		t.Fatalf("states = %v, want [working blocked working]", got)
	}
	blocked := f.snapshot()[1]
	if v, _ := flagVal(blocked.args, "--message"); v != "Approve deploy?" {
		t.Errorf("blocked --message = %q, want %q", v, "Approve deploy?")
	}
}

func TestTerminalEventReportsIdle(t *testing.T) {
	f := &fakeRunner{}
	r := newReporter(testEnv(), true, f.run)

	r.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventPipelineStarted, RunID: "r"})
	r.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventPipelineCompleted, TerminalStatus: "success"})

	states := reportedStates(f.snapshot())
	if len(states) == 0 || states[len(states)-1] != "idle" {
		t.Fatalf("last state = %v, want idle", states)
	}
}

func TestScopedTerminalDoesNotReportIdle(t *testing.T) {
	// A subgraph / manager_loop child that trips the shared budget guard emits
	// its own terminal-status event with a scoped ("parent/child") NodeID. Only
	// the unscoped top-level finish flips the pane to idle.
	f := &fakeRunner{}
	r := newReporter(testEnv(), true, f.run)

	r.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventPipelineStarted, RunID: "r"})
	r.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:           pipeline.EventBudgetExceeded,
		TerminalStatus: "budget_exceeded",
		NodeID:         "parent/child",
	})

	for _, s := range reportedStates(f.snapshot()) {
		if s == "idle" {
			t.Fatalf("scoped terminal must not report idle: %v", reportedStates(f.snapshot()))
		}
	}
}

func TestReleaseSendsReleaseAgent(t *testing.T) {
	f := &fakeRunner{}
	r := newReporter(testEnv(), true, f.run)

	r.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventPipelineStarted, RunID: "r"})
	r.Release()

	calls := f.snapshot()
	last := calls[len(calls)-1]
	if len(last.args) < 3 || last.args[0] != "pane" || last.args[1] != "release-agent" || last.args[2] != "w1:p1" {
		t.Fatalf("release args = %v, want [pane release-agent w1:p1 ...]", last.args)
	}
	if v, _ := flagVal(last.args, "--source"); v != "custom:tracker" {
		t.Errorf("--source = %q, want custom:tracker", v)
	}
	if v, _ := flagVal(last.args, "--agent"); v != "tracker" {
		t.Errorf("--agent = %q, want tracker", v)
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	f := &fakeRunner{}
	r := newReporter(testEnv(), true, f.run)

	r.Release()
	r.Release()

	n := 0
	for _, c := range f.snapshot() {
		if len(c.args) >= 2 && c.args[1] == "release-agent" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("release-agent calls = %d, want 1", n)
	}
}

func TestSeqStrictlyIncreases(t *testing.T) {
	f := &fakeRunner{}
	r := newReporter(testEnv(), true, f.run)

	r.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventPipelineStarted, RunID: "r"})
	r.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventGateOpened, Gate: &pipeline.GateDetail{GateID: "g1", Label: "x"}})
	r.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventGateResolved, Gate: &pipeline.GateDetail{GateID: "g1"}})

	var seqs []int
	for _, c := range f.snapshot() {
		if v, ok := flagVal(c.args, "--seq"); ok {
			n, err := strconv.Atoi(v)
			if err != nil {
				t.Fatalf("--seq = %q, not an int", v)
			}
			seqs = append(seqs, n)
		}
	}
	if len(seqs) < 2 {
		t.Fatalf("want at least 2 sequenced reports, got %v", seqs)
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("--seq not strictly increasing: %v", seqs)
		}
	}
}

func TestParallelGatesStayBlockedUntilAllResolved(t *testing.T) {
	f := &fakeRunner{}
	r := newReporter(testEnv(), true, f.run)

	r.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventPipelineStarted, RunID: "r"})
	r.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventGateOpened, Gate: &pipeline.GateDetail{GateID: "a", Label: "A"}})
	r.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventGateOpened, Gate: &pipeline.GateDetail{GateID: "b", Label: "B"}})
	r.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventGateResolved, Gate: &pipeline.GateDetail{GateID: "a"}})

	// b is still open — the pane must remain blocked (one blocked report, no working yet).
	if got := reportedStates(f.snapshot()); !equalStrings(got, []string{"working", "blocked"}) {
		t.Fatalf("after first resolve, states = %v, want [working blocked]", got)
	}

	r.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventGateResolved, Gate: &pipeline.GateDetail{GateID: "b"}})
	if got := reportedStates(f.snapshot()); !equalStrings(got, []string{"working", "blocked", "working"}) {
		t.Fatalf("after all resolve, states = %v, want [working blocked working]", got)
	}
}

func TestNonHumanGatesDoNotBlock(t *testing.T) {
	f := &fakeRunner{}
	r := newReporter(testEnv(), false, f.run) // humanGates=false: autopilot / auto-approve

	r.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventPipelineStarted, RunID: "r"})
	r.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventGateOpened, Gate: &pipeline.GateDetail{GateID: "g", Label: "x"}})
	r.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventGateResolved, Gate: &pipeline.GateDetail{GateID: "g"}})

	if got := reportedStates(f.snapshot()); !equalStrings(got, []string{"working"}) {
		t.Fatalf("autopilot gates must not block: states = %v, want [working]", got)
	}
}

func TestRunnerErrorNeverPanicsOrPropagates(t *testing.T) {
	f := &fakeRunner{err: errors.New("herdr exploded")}
	r := newReporter(testEnv(), true, f.run)

	// HandlePipelineEvent has no error return; a failing herdr binary must never
	// crash the engine goroutine or fail the run.
	r.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventPipelineStarted, RunID: "r"})
	r.Release()

	if len(f.snapshot()) == 0 {
		t.Fatal("expected the reporter to still attempt calls despite runner error")
	}
}

func TestDetectNilOutsideHerdr(t *testing.T) {
	t.Setenv("HERDR_ENV", "")
	if got := Detect(true); got != nil {
		t.Fatalf("Detect outside herdr = %v, want nil", got)
	}
}

func TestDetectNilWhenPaneMissing(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "")
	t.Setenv("HERDR_BIN_PATH", "/usr/bin/herdr")
	if got := Detect(true); got != nil {
		t.Fatalf("Detect with no pane id = %v, want nil", got)
	}
}

func TestDetectNilWhenDisabled(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("HERDR_BIN_PATH", "/usr/bin/herdr")
	t.Setenv("TRACKER_HERDR", "0")
	if got := Detect(true); got != nil {
		t.Fatalf("Detect with TRACKER_HERDR=0 = %v, want nil", got)
	}
}

func TestDetectReturnsReporterInPane(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("HERDR_BIN_PATH", "/usr/bin/herdr")
	t.Setenv("TRACKER_HERDR", "")
	r := Detect(true)
	if r == nil {
		t.Fatal("Detect inside herdr pane = nil, want reporter")
	}
	if r.env.PaneID != "w1:p1" || r.env.BinPath != "/usr/bin/herdr" {
		t.Errorf("env = %+v, want {PaneID:w1:p1 BinPath:/usr/bin/herdr}", r.env)
	}
}
