// ABOUTME: End-to-end tests for the REPL transport via a fake Dispatcher — no LLM.
package cli

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	chatops "github.com/2389-research/tracker/transport/chatops"
)

func TestRenderGate(t *testing.T) {
	choice := renderGate(chatops.Gate{Kind: chatops.GateChoice, Prompt: "Pick one", Choices: []string{"A", "B"}, Default: "B"})
	for _, want := range []string{"Pick one", "1) A", "2) B", "[default]", "number"} {
		if !strings.Contains(choice, want) {
			t.Errorf("choice render missing %q:\n%s", want, choice)
		}
	}
	free := renderGate(chatops.Gate{Kind: chatops.GateFreeform, Prompt: "Describe it"})
	if !strings.Contains(free, "Describe it") || !strings.Contains(free, "answer") {
		t.Errorf("freeform render unexpected:\n%s", free)
	}
}

func TestMapGateAnswer(t *testing.T) {
	choice := chatops.Gate{Kind: chatops.GateChoice, Choices: []string{"Approve", "Reject"}}
	if a := mapGateAnswer(choice, "2"); a.Choice != "Reject" {
		t.Errorf("number select: got %+v", a)
	}
	if a := mapGateAnswer(choice, "approve"); a.Choice != "Approve" {
		t.Errorf("label select (case-insensitive): got %+v", a)
	}
	if a := mapGateAnswer(choice, "5"); a.Freeform != "5" || a.Choice != "" {
		t.Errorf("out-of-range number should be freeform 'other': got %+v", a)
	}
	if a := mapGateAnswer(choice, "do neither"); a.Freeform != "do neither" {
		t.Errorf("unmatched should be freeform 'other': got %+v", a)
	}
	free := chatops.Gate{Kind: chatops.GateFreeform}
	if a := mapGateAnswer(free, "3"); a.Freeform != "3" || a.Choice != "" {
		t.Errorf("freeform gate should never select a choice: got %+v", a)
	}
}

// fakeDispatcher records calls, signaling mentions over a channel so a test can
// await the goroutine-dispatched call deterministically.
type fakeDispatcher struct {
	mentions chan string

	mu           sync.Mutex
	interactions []chatops.GateAnswer
}

func newFakeDispatcher() *fakeDispatcher {
	return &fakeDispatcher{mentions: make(chan string, 8)}
}

func (f *fakeDispatcher) OnMention(_ context.Context, _, _, text string) {
	f.mentions <- text
}

func (f *fakeDispatcher) OnInteraction(_, _ string, answer chatops.GateAnswer) bool {
	f.mu.Lock()
	f.interactions = append(f.interactions, answer)
	f.mu.Unlock()
	return true
}

func TestSession_RoutesFreshRequestAsMention(t *testing.T) {
	s := NewSession(&strings.Builder{})
	disp := newFakeDispatcher()
	go func() { _ = s.Run(context.Background(), strings.NewReader("make me a greeter\n/quit\n"), disp) }()

	select {
	case got := <-disp.mentions:
		if got != "make me a greeter" {
			t.Errorf("mention text = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request was not dispatched as a mention")
	}
}

func TestSession_RoutesLineToPendingGate(t *testing.T) {
	s := NewSession(&strings.Builder{})
	disp := newFakeDispatcher()

	// Arm a pending gate as the interviewer would, via the ThreadUI.
	ui := s.ThreadUI(Channel, Thread)
	if err := ui.PostGate(chatops.Gate{ID: "g1", Kind: chatops.GateChoice, Choices: []string{"Yes", "No"}}); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() { _ = s.Run(context.Background(), strings.NewReader("2\n/quit\n"), disp); close(done) }()
	<-done

	disp.mu.Lock()
	defer disp.mu.Unlock()
	if len(disp.interactions) != 1 || disp.interactions[0].Choice != "No" {
		t.Errorf("gate answer routing: got %+v", disp.interactions)
	}
}

func TestSession_ClearPendingReleasesSlot(t *testing.T) {
	s := NewSession(&strings.Builder{})
	ui := s.ThreadUI(Channel, Thread).(cliUI)
	_ = ui.PostGate(chatops.Gate{ID: "g1", Kind: chatops.GateFreeform})
	ui.ClearPending("g1")
	if g := s.takePending(); g != nil {
		t.Errorf("ClearPending should have released the slot, got %+v", g)
	}
}
