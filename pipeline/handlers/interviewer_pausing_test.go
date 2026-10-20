// ABOUTME: Tests for PausingInterviewer: it pauses each gate with an exact record,
// ABOUTME: applies a held answer once to the matching gate, and re-pauses on change.
package handlers

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func gateCtx(node string, scope ...string) context.Context {
	return WithGateInfo(context.Background(), GateInfo{NodeID: node, Mode: "choice", Label: "Pick", Prompt: "Pick one", ScopePath: scope})
}

func pausedGateOf(t *testing.T, err error) pipeline.PausedGate {
	t.Helper()
	var pe *pipeline.PauseError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want a *PauseError", err)
	}
	if pe.Status != pipeline.OutcomePausedGate || len(pe.Gates) != 1 {
		t.Fatalf("pause = %+v, want one paused_gate record", pe)
	}
	return pe.Gates[0]
}

func TestPausing_NoAnswerPauses(t *testing.T) {
	p := NewPausingInterviewer(nil)
	ctx := gateCtx("G")

	_, err := p.AskContext(ctx, "Pick one", []string{"approve", "reject"}, "reject")
	g := pausedGateOf(t, err)
	if g.NodeID != "G" || g.Mode != "choice" || !slices.Equal(g.Choices, []string{"approve", "reject"}) || g.Default != "reject" || g.Prompt != "Pick one" || g.Nonce == "" || g.Fingerprint == "" {
		t.Errorf("choice record = %+v", g)
	}

	_, err = p.AskFreeformWithLabelsContext(ctx, "Say", []string{"revise", "approve"}, "approve")
	if g := pausedGateOf(t, err); g.Mode != "freeform" || !slices.Equal(g.Choices, []string{"revise", "approve"}) || g.Default != "approve" {
		t.Errorf("labeled freeform record = %+v", g)
	}

	_, err = p.AskFreeformContext(ctx, "Say anything")
	if g := pausedGateOf(t, err); g.Mode != "freeform" || len(g.Choices) != 0 {
		t.Errorf("freeform record = %+v", g)
	}

	yn := WithGateInfo(context.Background(), GateInfo{NodeID: "Y", Mode: "yes_no"})
	_, err = p.AskContext(yn, "Ship?", []string{"Yes", "No"}, "")
	if g := pausedGateOf(t, err); g.Mode != "yes_no" {
		t.Errorf("yes_no record = %+v", g)
	}

	qs := []Question{{Index: 1, Text: "Which db?", Context: "we need one", Options: []string{"pg", "sqlite"}}, {Index: 2, Text: "Name?"}}
	_, err = p.AskInterviewContext(ctx, qs, nil)
	g = pausedGateOf(t, err)
	if g.Mode != "interview" || len(g.Questions) != 2 || g.Questions[0].ID != "q1" || g.Questions[0].Context != "we need one" || g.Questions[1].ID != "q2" {
		t.Errorf("interview record = %+v", g)
	}
}

func answerFor(t *testing.T, ctx context.Context, prompt string, choices []string, def string, label string) *PendingAnswer {
	t.Helper()
	_, err := NewPausingInterviewer(nil).AskContext(ctx, prompt, choices, def)
	g := pausedGateOf(t, err)
	return &PendingAnswer{NodeID: g.NodeID, ScopePath: g.ScopePath, Fingerprint: g.Fingerprint, Nonce: g.Nonce, Label: label}
}

func TestPausing_AnswerAppliedOnce(t *testing.T) {
	ctx := gateCtx("G")
	choices := []string{"approve", "reject"}
	p := NewPausingInterviewer(answerFor(t, ctx, "Pick one", choices, "reject", "approve"))
	got, err := p.AskContext(ctx, "Pick one", choices, "reject")
	if err != nil || got != "approve" {
		t.Fatalf("first ask = (%q, %v), want approve", got, err)
	}
	if _, err := p.AskContext(ctx, "Pick one", choices, "reject"); err == nil {
		t.Fatal("second ask of the same gate was answered again; the answer must apply once")
	}
}

func TestPausing_FingerprintMismatchRepauses(t *testing.T) {
	ctx := gateCtx("G")
	choices := []string{"approve", "reject"}
	p := NewPausingInterviewer(answerFor(t, ctx, "Pick one", choices, "reject", "approve"))
	_, err := p.AskContext(ctx, "Pick one — the diff changed", choices, "reject")
	g := pausedGateOf(t, err)
	if !strings.Contains(g.Note, "the question changed since you answered") {
		t.Errorf("note = %q", g.Note)
	}
	// The stale answer is gone, not kept for a later match.
	if _, err := p.AskContext(ctx, "Pick one", choices, "reject"); err == nil {
		t.Error("stale answer applied on a later ask")
	}
}

func TestPausing_OtherScopeUnaffected(t *testing.T) {
	choices := []string{"go"}
	p := NewPausingInterviewer(answerFor(t, gateCtx("G", "B1"), "Pick one", choices, "", "go"))
	if _, err := p.AskContext(gateCtx("G", "B2"), "Pick one", choices, ""); err == nil {
		t.Fatal("answer for branch B1 answered the same gate in B2")
	}
	if got, err := p.AskContext(gateCtx("G", "B1"), "Pick one", choices, ""); err != nil || got != "go" {
		t.Fatalf("B1 ask = (%q, %v), want go", got, err)
	}
}

func TestPausing_FreeformText(t *testing.T) {
	ctx := gateCtx("G")
	_, err := NewPausingInterviewer(nil).AskFreeformWithLabelsContext(ctx, "Say", []string{"approve"}, "")
	g := pausedGateOf(t, err)
	p := NewPausingInterviewer(&PendingAnswer{NodeID: "G", Fingerprint: g.Fingerprint, Text: "make the title shorter"})
	if got, err := p.AskFreeformWithLabelsContext(ctx, "Say", []string{"approve"}, ""); err != nil || got != "make the title shorter" {
		t.Fatalf("got (%q, %v)", got, err)
	}
}

func TestPausing_InterviewAnswersAndCancel(t *testing.T) {
	ctx := gateCtx("G")
	qs := []Question{{Index: 1, Text: "Which db?", Options: []string{"pg", "sqlite"}}, {Index: 2, Text: "Name?"}}
	_, err := NewPausingInterviewer(nil).AskInterviewContext(ctx, qs, nil)
	g := pausedGateOf(t, err)

	p := NewPausingInterviewer(&PendingAnswer{NodeID: "G", Fingerprint: g.Fingerprint, Interview: map[string]string{"q1": "pg", "q2": "orders"}})
	res, err := p.AskInterviewContext(ctx, qs, nil)
	if err != nil || res == nil || len(res.Questions) != 2 || res.Questions[0].Answer != "pg" || res.Questions[1].Answer != "orders" || res.Questions[0].ID != "q1" {
		t.Fatalf("interview result = %+v, %v", res, err)
	}

	p = NewPausingInterviewer(&PendingAnswer{NodeID: "G", Fingerprint: g.Fingerprint, Cancel: true})
	res, err = p.AskInterviewContext(ctx, qs, nil)
	if err != nil || res == nil || !res.Canceled {
		t.Fatalf("cancel result = %+v, %v", res, err)
	}
}

func TestPausing_Marker(t *testing.T) {
	var iv Interviewer = NewPausingInterviewer(nil)
	gp, ok := iv.(GatePauser)
	if !ok || !gp.PausesGates() {
		t.Fatal("PausingInterviewer must report PausesGates")
	}
	if actorOf(iv) != pipeline.ActorHuman {
		t.Errorf("actor = %q, want human", actorOf(iv))
	}
}

// --- handler integration ---

func choiceGateGraph() *pipeline.Graph {
	g := pipeline.NewGraph("pause_gate")
	g.AddNode(&pipeline.Node{ID: "Review", Shape: "hexagon", Label: "Review it"})
	g.AddNode(&pipeline.Node{ID: "Ship", Shape: "box"})
	g.AddNode(&pipeline.Node{ID: "Drop", Shape: "box"})
	g.AddEdge(&pipeline.Edge{From: "Review", To: "Ship", Label: "approve"})
	g.AddEdge(&pipeline.Edge{From: "Review", To: "Drop", Label: "reject"})
	return g
}

func TestHumanHandler_PauseResolvedEvent(t *testing.T) {
	g := choiceGateGraph()
	events := &collectingEventHandler{}
	h := NewHumanHandler(NewPausingInterviewer(nil), g, WithHumanPipelineEmitter(events))
	_, err := h.Execute(context.Background(), g.Nodes["Review"], pipeline.NewPipelineContext())
	pausedGateOf(t, err)

	var opened, resolved *pipeline.GateDetail
	for _, evt := range events.Events() {
		switch evt.Type {
		case pipeline.EventGateOpened:
			opened = evt.Gate
		case pipeline.EventGateResolved:
			resolved = evt.Gate
		}
	}
	if opened == nil || resolved == nil {
		t.Fatalf("opened=%v resolved=%v", opened, resolved)
	}
	if resolved.GateID != opened.GateID || resolved.Resolution != pipeline.GateResolutionPaused || resolved.Error != "" || resolved.Fingerprint == "" {
		t.Errorf("gate_resolved = %+v", resolved)
	}
}

func TestHumanHandler_SubgraphGateFailsClosedInPauseMode(t *testing.T) {
	g := choiceGateGraph()
	events := &collectingEventHandler{}
	h := NewHumanHandler(NewPausingInterviewer(nil), g, WithHumanPipelineEmitter(events))
	ctx := pipeline.WithSubgraphScope(context.Background(), "Sub")
	out, err := h.Execute(ctx, g.Nodes["Review"], pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("err = %v, want nil (a routable failure)", err)
	}
	if out.Status != pipeline.OutcomeFail || !strings.Contains(out.FailureReason, "a gate inside a subgraph can't pause yet") {
		t.Errorf("outcome = %+v", out)
	}
	var resolvedErr string
	for _, evt := range events.Events() {
		if evt.Type == pipeline.EventGateResolved && evt.Gate != nil {
			resolvedErr = evt.Gate.Error
		}
	}
	if !strings.Contains(resolvedErr, "can't pause yet") {
		t.Errorf("gate_resolved error = %q", resolvedErr)
	}

	// A non-pausing interviewer inside a subgraph is unaffected.
	q := NewHumanHandler(&QueueInterviewer{Answers: []string{"approve"}}, g)
	out, err = q.Execute(ctx, g.Nodes["Review"], pipeline.NewPipelineContext())
	if err != nil || out.Status != pipeline.OutcomeSuccess {
		t.Errorf("queue interviewer in subgraph = %+v, %v", out, err)
	}
}
