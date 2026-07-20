// ABOUTME: Tests for SlackInterviewer — the channel bridge from tracker gates to a thread.
package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/2389-research/tracker/pipeline/handlers"
)

// fakeUI records posted gates on a channel so a test can await and resolve them.
type fakeUI struct {
	gates chan Gate
	mu    sync.Mutex
	posts []string
}

func newFakeUI() *fakeUI { return &fakeUI{gates: make(chan Gate, 4)} }

func (f *fakeUI) PostGate(g Gate) error { f.gates <- g; return nil }
func (f *fakeUI) Post(text string) error {
	f.mu.Lock()
	f.posts = append(f.posts, text)
	f.mu.Unlock()
	return nil
}

// seqIDs returns a deterministic unique-id generator.
func seqIDs() func() string {
	var n int
	var mu sync.Mutex
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		n++
		return fmt.Sprintf("g%d", n)
	}
}

func awaitGate(t *testing.T, ui *fakeUI) Gate {
	t.Helper()
	select {
	case g := <-ui.gates:
		return g
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a posted gate")
		return Gate{}
	}
}

func TestSlackInterviewer_ChoiceAndYesNo(t *testing.T) {
	ui := newFakeUI()
	iv := NewSlackInterviewer(ui, seqIDs())

	got := make(chan string, 1)
	go func() { a, _ := iv.Ask("pick one", []string{"alpha", "beta"}, "alpha"); got <- a }()
	g := awaitGate(t, ui)
	if g.Kind != GateChoice {
		t.Fatalf("kind = %q, want choice", g.Kind)
	}
	if !iv.Resolve(g.ID, GateAnswer{Choice: "beta"}) {
		t.Fatal("Resolve returned false for a pending gate")
	}
	if a := <-got; a != "beta" {
		t.Fatalf("answer = %q, want beta", a)
	}

	// Yes/No renders as a yes_no gate.
	go func() { a, _ := iv.Ask("proceed?", []string{"Yes", "No"}, "Yes"); got <- a }()
	g = awaitGate(t, ui)
	if g.Kind != GateYesNo {
		t.Fatalf("kind = %q, want yes_no", g.Kind)
	}
	iv.Resolve(g.ID, GateAnswer{Choice: "No"})
	if a := <-got; a != "No" {
		t.Fatalf("answer = %q, want No", a)
	}
}

func TestSlackInterviewer_FreeformAndLabels(t *testing.T) {
	ui := newFakeUI()
	iv := NewSlackInterviewer(ui, seqIDs())

	got := make(chan string, 1)
	go func() { a, _ := iv.AskFreeform("what next?"); got <- a }()
	g := awaitGate(t, ui)
	if g.Kind != GateFreeform {
		t.Fatalf("kind = %q, want freeform", g.Kind)
	}
	iv.Resolve(g.ID, GateAnswer{Freeform: "ship it"})
	if a := <-got; a != "ship it" {
		t.Fatalf("answer = %q, want 'ship it'", a)
	}

	// A typed "other" reply wins over a label selection.
	go func() { a, _ := iv.AskFreeformWithLabels("choose", []string{"a", "b"}, "a"); got <- a }()
	g = awaitGate(t, ui)
	iv.Resolve(g.ID, GateAnswer{Choice: "a", Freeform: "custom"})
	if a := <-got; a != "custom" {
		t.Fatalf("labeled answer = %q, want custom", a)
	}
}

func TestSlackInterviewer_Interview(t *testing.T) {
	ui := newFakeUI()
	iv := NewSlackInterviewer(ui, seqIDs())

	questions := []handlers.Question{
		{Index: 1, Text: "name?"},                                  // open-ended → freeform
		{Index: 2, Text: "color?", Options: []string{"red", "blue"}}, // options → choice
	}
	got := make(chan *handlers.InterviewResult, 1)
	go func() { r, _ := iv.AskInterview(questions, nil); got <- r }()

	// Q1 is presented as a freeform gate; reply.
	g1 := awaitGate(t, ui)
	if g1.Kind != GateFreeform {
		t.Fatalf("q1 kind = %q, want freeform", g1.Kind)
	}
	iv.Resolve(g1.ID, GateAnswer{Freeform: "Ada"})

	// Q2 is presented as a choice gate; click.
	g2 := awaitGate(t, ui)
	if g2.Kind != GateChoice || len(g2.Choices) != 2 {
		t.Fatalf("q2 gate = %+v", g2)
	}
	iv.Resolve(g2.ID, GateAnswer{Choice: "blue"})

	r := <-got
	if r == nil || len(r.Questions) != 2 {
		t.Fatalf("interview result = %+v", r)
	}
	if r.Questions[0].Answer != "Ada" || r.Questions[1].Answer != "blue" {
		t.Fatalf("answers = %+v", r.Questions)
	}
}

func TestSlackInterviewer_CancelUnblocks(t *testing.T) {
	ui := newFakeUI()
	iv := NewSlackInterviewer(ui, seqIDs())

	// Freeform/choice cancel returns an error.
	errc := make(chan error, 1)
	go func() { _, err := iv.AskFreeform("hold"); errc <- err }()
	awaitGate(t, ui)
	iv.Cancel()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("expected an error after Cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Cancel did not unblock the gate")
	}

	// A cancelled interview returns a Canceled result, not an error.
	iv2 := NewSlackInterviewer(ui, seqIDs())
	resc := make(chan *handlers.InterviewResult, 1)
	go func() { r, _ := iv2.AskInterview([]handlers.Question{{Index: 1, Text: "q"}}, nil); resc <- r }()
	awaitGate(t, ui)
	iv2.Cancel()
	if r := <-resc; r == nil || !r.Canceled {
		t.Fatalf("expected canceled interview result, got %+v", r)
	}
}

func TestSlackInterviewer_ContextCancelUnblocks(t *testing.T) {
	ui := newFakeUI()
	iv := NewSlackInterviewer(ui, seqIDs())
	ctx, cancel := context.WithCancel(context.Background())
	iv.SetPipelineContext(ctx)

	errc := make(chan error, 1)
	go func() { _, err := iv.Ask("pick", []string{"a", "b"}, "a"); errc <- err }()
	awaitGate(t, ui)
	cancel()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("expected an error after context cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("context cancel did not unblock the gate")
	}

	// Resolving an unknown/torn-down gate is a safe no-op.
	if iv.Resolve("nonexistent", GateAnswer{Choice: "x"}) {
		t.Fatal("Resolve on unknown gate should return false")
	}
}
