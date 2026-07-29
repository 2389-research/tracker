// ABOUTME: A reusable conformance suite any transport runs to prove its
// handlers.Interviewer honours the gate contract (all modes + cancellation).
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
)

// Subject is the interviewer under test plus the hooks the suite needs to drive
// it. A transport builds one per sub-test (state must not leak between them).
type Subject struct {
	// Interviewer is the implementation under test. The suite type-asserts the
	// richer interfaces (FreeformInterviewer, LabeledFreeformInterviewer,
	// InterviewInterviewer) and skips modes it does not implement.
	Interviewer handlers.Interviewer

	// Answer resolves the interviewer's *next* posted gate the way the transport
	// would (a button click, a thread reply, …). reply is a choice label or
	// freeform text; the transport decides which by the gate it posted. It
	// returns the prompt shown (for optional assertions) and must fail t if no
	// gate appears in a reasonable time.
	Answer func(t *testing.T, reply string) (prompt string)

	// Cancel tears the interviewer down (the Cancel()/Close teardown path). It
	// must unblock any gate the interviewer is currently — or subsequently —
	// waiting on, returning an error from the Ask* call.
	Cancel func()

	// LastGateInfo is optional. A transport whose interviewer implements the
	// handlers.GateAware side-interface sets this to report the most recent
	// GateInfo the interviewer received via BeginGate (and whether one arrived).
	// When nil, or when the interviewer does not implement GateAware, the
	// GateAware sub-test is skipped — the callback is purely additive, so a
	// transport that ignores it is still conformant.
	LastGateInfo func() (handlers.GateInfo, bool)
}

// RunInterviewerSuite exercises the handlers.Interviewer family against a
// transport's implementation. newSubject is called once per sub-test with a
// fresh interviewer. A new transport calls this from a test to prove it honours
// the gate contract — the executable definition of "a correct interviewer".
func RunInterviewerSuite(t *testing.T, newSubject func() Subject) {
	t.Run("Choice", func(t *testing.T) { runChoice(t, newSubject()) })
	t.Run("YesNo", func(t *testing.T) { runYesNo(t, newSubject()) })
	t.Run("Freeform", func(t *testing.T) { runFreeform(t, newSubject()) })
	t.Run("Labels", func(t *testing.T) { runLabels(t, newSubject()) })
	t.Run("Interview", func(t *testing.T) { runInterview(t, newSubject()) })
	t.Run("CancelUnblocksWaitingGate", func(t *testing.T) { runCancel(t, newSubject()) })
	t.Run("GateAwareCorrelatesGateID", func(t *testing.T) { runGateAware(t, newSubject()) })
}

type askResult struct {
	val string
	err error
}

// await returns the Ask* result or fails the test if the interviewer never
// returned (a leaked/blocked gate goroutine).
func await(t *testing.T, ch <-chan askResult) askResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("interviewer did not return within 3s (gate never resolved?)")
		return askResult{}
	}
}

func runChoice(t *testing.T, s Subject) {
	ch := make(chan askResult, 1)
	go func() {
		v, err := s.Interviewer.Ask("Pick one", []string{"alpha", "beta"}, "alpha")
		ch <- askResult{v, err}
	}()
	s.Answer(t, "beta")
	if r := await(t, ch); r.err != nil || r.val != "beta" {
		t.Fatalf("Ask(choice) = %q, %v; want beta, nil", r.val, r.err)
	}
}

func runYesNo(t *testing.T, s Subject) {
	ch := make(chan askResult, 1)
	go func() {
		v, err := s.Interviewer.Ask("Proceed?", []string{"Yes", "No"}, "Yes")
		ch <- askResult{v, err}
	}()
	s.Answer(t, "No")
	if r := await(t, ch); r.err != nil || r.val != "No" {
		t.Fatalf("Ask(yes/no) = %q, %v; want No, nil", r.val, r.err)
	}
}

func runFreeform(t *testing.T, s Subject) {
	iv, ok := s.Interviewer.(handlers.FreeformInterviewer)
	if !ok {
		t.Skip("interviewer does not implement FreeformInterviewer")
	}
	ch := make(chan askResult, 1)
	go func() {
		v, err := iv.AskFreeform("Say something")
		ch <- askResult{v, err}
	}()
	s.Answer(t, "hello there")
	if r := await(t, ch); r.err != nil || r.val != "hello there" {
		t.Fatalf("AskFreeform = %q, %v; want \"hello there\", nil", r.val, r.err)
	}
}

func runLabels(t *testing.T, s Subject) {
	iv, ok := s.Interviewer.(handlers.LabeledFreeformInterviewer)
	if !ok {
		t.Skip("interviewer does not implement LabeledFreeformInterviewer")
	}
	ch := make(chan askResult, 1)
	go func() {
		v, err := iv.AskFreeformWithLabels("Choose", []string{"x", "y"}, "x")
		ch <- askResult{v, err}
	}()
	s.Answer(t, "y")
	if r := await(t, ch); r.err != nil || r.val != "y" {
		t.Fatalf("AskFreeformWithLabels = %q, %v; want y, nil", r.val, r.err)
	}
}

func runInterview(t *testing.T, s Subject) {
	iv, ok := s.Interviewer.(handlers.InterviewInterviewer)
	if !ok {
		t.Skip("interviewer does not implement InterviewInterviewer")
	}
	questions := []handlers.Question{
		{Index: 1, Text: "Favorite color?", Options: []string{"red", "blue"}},
		{Index: 2, Text: "Anything else?"}, // open-ended
	}
	type ir struct {
		res *handlers.InterviewResult
		err error
	}
	ch := make(chan ir, 1)
	go func() {
		res, err := iv.AskInterview(questions, nil)
		ch <- ir{res, err}
	}()

	// One answer per question, posted as a sequence of gates.
	s.Answer(t, "blue")
	s.Answer(t, "no thanks")

	select {
	case got := <-ch:
		assertInterview(t, got.res, got.err, "blue", "no thanks")
	case <-time.After(3 * time.Second):
		t.Fatal("AskInterview did not return within 3s")
	}
}

// assertInterview checks a completed interview returned the expected answers in
// order and was not canceled.
func assertInterview(t *testing.T, res *handlers.InterviewResult, err error, want ...string) {
	t.Helper()
	if err != nil {
		t.Fatalf("AskInterview error: %v", err)
	}
	if res == nil || res.Canceled {
		t.Fatalf("AskInterview result = %+v; want a completed, non-canceled result", res)
	}
	if len(res.Questions) != len(want) {
		t.Fatalf("got %d answers, want %d", len(res.Questions), len(want))
	}
	for i, w := range want {
		if res.Questions[i].Answer != w {
			t.Fatalf("interview answer %d = %q, want %q", i, res.Questions[i].Answer, w)
		}
	}
}

// runGateAware drives the interviewer through a real HumanHandler and proves the
// optional GateAware callback receives the gate identity — most importantly a
// GateID that EQUALS the gate_opened event's GateID for the same gate, the
// correlation an out-of-process transport relies on. Skipped when the
// interviewer does not implement GateAware (the callback is purely additive).
func runGateAware(t *testing.T, s Subject) {
	if _, ok := s.Interviewer.(handlers.GateAware); !ok || s.LastGateInfo == nil {
		t.Skip("interviewer does not implement GateAware")
	}

	graph := pipeline.NewGraph("gate-aware")
	graph.AddNode(&pipeline.Node{ID: "gate", Shape: "hexagon", Label: "Pick one"})
	graph.AddNode(&pipeline.Node{ID: "a", Shape: "box"})
	graph.AddNode(&pipeline.Node{ID: "b", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "a", Label: "alpha"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "b", Label: "beta"})

	var opened []pipeline.PipelineEvent
	emitter := pipeline.PipelineEventHandlerFunc(func(e pipeline.PipelineEvent) {
		if e.Type == pipeline.EventGateOpened {
			opened = append(opened, e)
		}
	})
	h := handlers.NewHumanHandler(s.Interviewer, graph, handlers.WithHumanPipelineEmitter(emitter))
	pctx := pipeline.NewPipelineContext()
	pctx.SetInternal(pipeline.InternalKeyRunID, "conformance-run")

	done := make(chan error, 1)
	go func() {
		_, err := h.Execute(context.Background(), graph.Nodes["gate"], pctx)
		done <- err
	}()
	s.Answer(t, "beta")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Execute through GateAware interviewer failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Execute did not return within 3s (gate never resolved?)")
	}

	assertGateAwareCorrelation(t, s, opened)
}

// assertGateAwareCorrelation checks the recorded BeginGate info matches the
// posted gate and correlates with the gate_opened event's GateID.
func assertGateAwareCorrelation(t *testing.T, s Subject, opened []pipeline.PipelineEvent) {
	t.Helper()
	info, ok := s.LastGateInfo()
	if !ok {
		t.Fatal("GateAware interviewer received no BeginGate before the Ask call")
	}
	assertGateInfoFields(t, info)
	if len(opened) != 1 || opened[0].Gate == nil {
		t.Fatalf("got %d gate_opened events, want 1 with a payload", len(opened))
	}
	if info.GateID == "" || info.GateID != opened[0].Gate.GateID {
		t.Fatalf("GateInfo.GateID = %q, gate_opened GateID = %q; the two MUST correlate", info.GateID, opened[0].Gate.GateID)
	}
}

// assertGateInfoFields checks the identity fields BeginGate reported for the
// choice gate the suite posted.
func assertGateInfoFields(t *testing.T, info handlers.GateInfo) {
	t.Helper()
	if info.NodeID != "gate" {
		t.Errorf("GateInfo.NodeID = %q, want gate", info.NodeID)
	}
	if info.Mode != "choice" {
		t.Errorf("GateInfo.Mode = %q, want choice", info.Mode)
	}
	if info.Label != "Pick one" {
		t.Errorf("GateInfo.Label = %q, want \"Pick one\"", info.Label)
	}
	if info.RunID != "conformance-run" {
		t.Errorf("GateInfo.RunID = %q, want conformance-run", info.RunID)
	}
}

func runCancel(t *testing.T, s Subject) {
	ch := make(chan askResult, 1)
	go func() {
		v, err := s.Interviewer.Ask("Pick", []string{"a", "b"}, "a")
		ch <- askResult{v, err}
	}()
	// Tear down while the gate is waiting; the Ask must return an error, not hang.
	s.Cancel()
	if r := await(t, ch); r.err == nil {
		t.Fatalf("Cancel must unblock a waiting gate with an error; got value %q, nil error", r.val)
	}
}
