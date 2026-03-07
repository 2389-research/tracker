// ABOUTME: Tests for BubbleteaInterviewer and the choiceRunner/freeformRunner wrappers.
// ABOUTME: Uses headless tea.Program testing to avoid real terminal requirements.
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/2389-research/tracker/tui/components"
)

// ─── choiceRunner tests ──────────────────────────────────────────────────────

func TestChoiceRunnerForwardsDoneMsg(t *testing.T) {
	runner := choiceRunner{inner: components.NewChoiceModel("Pick", []string{"a", "b"}, "")}
	m2, cmd := runner.Update(components.ChoiceDoneMsg{Value: "a"})
	cr := m2.(choiceRunner)
	if cr.result != "a" {
		t.Errorf("expected result='a', got %q", cr.result)
	}
	if cmd == nil {
		t.Fatal("expected tea.Quit command")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestChoiceRunnerForwardsCancelMsg(t *testing.T) {
	runner := choiceRunner{inner: components.NewChoiceModel("Pick", []string{"a"}, "")}
	m2, cmd := runner.Update(components.ChoiceCancelMsg{})
	cr := m2.(choiceRunner)
	if !cr.cancelled {
		t.Error("expected cancelled=true")
	}
	if cmd == nil {
		t.Fatal("expected tea.Quit command")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestChoiceRunnerDelegatesNavigationToInner(t *testing.T) {
	runner := choiceRunner{inner: components.NewChoiceModel("Pick", []string{"a", "b", "c"}, "")}
	// Press down — should move cursor in inner model
	m2, _ := runner.Update(tea.KeyMsg{Type: tea.KeyDown})
	cr := m2.(choiceRunner)
	if cr.inner.IsDone() || cr.inner.IsCancelled() {
		t.Error("inner model should not be done/cancelled after navigation")
	}
}

func TestChoiceRunnerViewDelegatesToInner(t *testing.T) {
	runner := choiceRunner{inner: components.NewChoiceModel("My Prompt", []string{"x", "y"}, "")}
	view := runner.View()
	if view == "" {
		t.Error("expected non-empty view from runner")
	}
}

// ─── freeformRunner tests ────────────────────────────────────────────────────

func TestFreeformRunnerForwardsDoneMsg(t *testing.T) {
	runner := freeformRunner{inner: components.NewFreeformModel("Ask")}
	m2, cmd := runner.Update(components.FreeformDoneMsg{Value: "my answer"})
	fr := m2.(freeformRunner)
	if fr.result != "my answer" {
		t.Errorf("expected result='my answer', got %q", fr.result)
	}
	if cmd == nil {
		t.Fatal("expected tea.Quit command")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestFreeformRunnerForwardsCancelMsg(t *testing.T) {
	runner := freeformRunner{inner: components.NewFreeformModel("Ask")}
	m2, cmd := runner.Update(components.FreeformCancelMsg{})
	fr := m2.(freeformRunner)
	if !fr.cancelled {
		t.Error("expected cancelled=true")
	}
	if cmd == nil {
		t.Fatal("expected tea.Quit command")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestFreeformRunnerViewDelegatesToInner(t *testing.T) {
	runner := freeformRunner{inner: components.NewFreeformModel("Enter text")}
	view := runner.View()
	if view == "" {
		t.Error("expected non-empty view from freeformRunner")
	}
}

// ─── BubbleteaInterviewer construction tests ─────────────────────────────────

func TestNewBubbleteaInterviewerIsMode1(t *testing.T) {
	iv := NewBubbleteaInterviewer()
	if iv.tuiProgram != nil {
		t.Error("expected mode-1 interviewer to have nil tuiProgram")
	}
}

func TestNewBubbleteaInterviewerMode2HasProgram(t *testing.T) {
	// Create a minimal program. We won't run it.
	type dummyModel struct{}
	dummyModel_ := dummyModel{}
	_ = dummyModel_
	// Just test the struct assignment — no need to start the program.
	iv, ch := NewBubbleteaInterviewerMode2(nil)
	if iv.replyCh == nil {
		t.Error("expected non-nil replyCh for mode-2 interviewer")
	}
	// ch should be the same channel
	if ch == nil {
		t.Error("expected non-nil returned channel")
	}
}

// ─── Mode 1 headless tests ───────────────────────────────────────────────────

// headlessChoiceProgram simulates an inline choice gate by driving the
// choiceRunner model without a real terminal.
func TestChoiceRunnerFullSelectSequence(t *testing.T) {
	runner := choiceRunner{inner: components.NewChoiceModel("Pick", []string{"alpha", "beta", "gamma"}, "")}

	// Navigate: down once → cursor=1 (beta)
	m, _ := runner.Update(tea.KeyMsg{Type: tea.KeyDown})
	runner = m.(choiceRunner)

	// Confirm with enter → should emit ChoiceDoneMsg from inner, but inner
	// returns the message via cmd, not directly. Let's drive it to completion.
	_, cmd := runner.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected command after enter on inner")
	}
	// The inner model emits ChoiceDoneMsg; feed it back to the runner
	msg := cmd()
	m2, cmd2 := runner.Update(msg)
	runner = m2.(choiceRunner)
	if runner.result != "beta" {
		t.Errorf("expected 'beta', got %q", runner.result)
	}
	if cmd2 == nil {
		t.Fatal("expected tea.Quit after done msg")
	}
	quitMsg := cmd2()
	if _, ok := quitMsg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", quitMsg)
	}
}

func TestFreeformRunnerFullSubmitSequence(t *testing.T) {
	runner := freeformRunner{inner: components.NewFreeformModel("Enter answer")}

	// Type "hello" character by character
	for _, ch := range "hello" {
		m, _ := runner.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		runner = m.(freeformRunner)
	}

	// Press enter to submit
	_, cmd := runner.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected command after enter")
	}

	// Inner model emits FreeformDoneMsg
	msg := cmd()
	m2, cmd2 := runner.Update(msg)
	runner = m2.(freeformRunner)
	if runner.result != "hello" {
		t.Errorf("expected 'hello', got %q", runner.result)
	}
	if cmd2 == nil {
		t.Fatal("expected tea.Quit after done msg")
	}
	quitMsg := cmd2()
	if _, ok := quitMsg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", quitMsg)
	}
}

// ─── Ask/AskFreeform with no choices ────────────────────────────────────────

func TestAskReturnsErrorForEmptyChoices(t *testing.T) {
	iv := NewBubbleteaInterviewer()
	// We can't run a real tea.Program in tests without a TTY, but we CAN test
	// the validation guard that runs before the program.
	_, err := iv.Ask("Prompt", []string{}, "")
	if err == nil {
		t.Error("expected error for empty choices")
	}
}

// ─── Mode 2 gate request messages ───────────────────────────────────────────

func TestGateChoiceRequestMsgFields(t *testing.T) {
	ch := make(chan string, 1)
	msg := GateChoiceRequestMsg{
		Prompt:        "Choose",
		Choices:       []string{"a", "b"},
		DefaultChoice: "a",
		ReplyCh:       ch,
	}
	if msg.Prompt != "Choose" {
		t.Errorf("unexpected prompt: %q", msg.Prompt)
	}
	if len(msg.Choices) != 2 {
		t.Errorf("expected 2 choices, got %d", len(msg.Choices))
	}
}

func TestGateFreeformRequestMsgFields(t *testing.T) {
	ch := make(chan string, 1)
	msg := GateFreeformRequestMsg{
		Prompt:  "Tell me",
		ReplyCh: ch,
	}
	if msg.Prompt != "Tell me" {
		t.Errorf("unexpected prompt: %q", msg.Prompt)
	}
}
