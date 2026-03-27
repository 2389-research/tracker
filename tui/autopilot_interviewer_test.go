// ABOUTME: Tests for AutopilotTUIInterviewer — verifies it delegates to the inner autopilot
// ABOUTME: and sends MsgGateAutopilot through the TUI send function.
package tui

import (
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// mockAutopilot implements handlers.LabeledFreeformInterviewer for testing.
type mockAutopilot struct {
	askResult      string
	askErr         error
	freeformResult string
	freeformErr    error
	labeledResult  string
	labeledErr     error
}

func (m *mockAutopilot) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	return m.askResult, m.askErr
}

func (m *mockAutopilot) AskFreeform(prompt string) (string, error) {
	return m.freeformResult, m.freeformErr
}

func (m *mockAutopilot) AskFreeformWithLabels(prompt string, labels []string, defaultLabel string) (string, error) {
	return m.labeledResult, m.labeledErr
}

// drainAndReply consumes a MsgGateAutopilot from the channel, sends the
// decision back on the reply channel (simulating auto-close), and returns
// the message for assertions.
func drainAndReply(ch <-chan tea.Msg) MsgGateAutopilot {
	msg := (<-ch).(MsgGateAutopilot)
	// Reply immediately so flashDecision unblocks.
	select {
	case msg.ReplyCh <- msg.Decision:
	default:
	}
	return msg
}

func TestAutopilotTUIInterviewerAsk(t *testing.T) {
	msgCh := make(chan tea.Msg, 2)
	mockSend := func(msg tea.Msg) { msgCh <- msg }

	inner := &mockAutopilot{askResult: "beta"}
	iv := NewAutopilotTUIInterviewer(inner, mockSend)

	done := make(chan string, 1)
	go func() {
		result, err := iv.Ask("Pick one", []string{"alpha", "beta"}, "alpha")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		done <- result
	}()

	gate := drainAndReply(msgCh)
	if gate.Decision != "beta" {
		t.Errorf("expected decision 'beta', got %q", gate.Decision)
	}
	if gate.Prompt != "Pick one" {
		t.Errorf("expected prompt 'Pick one', got %q", gate.Prompt)
	}

	select {
	case result := <-done:
		if result != "beta" {
			t.Errorf("expected 'beta', got %q", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Ask result")
	}
}

func TestAutopilotTUIInterviewerAskFreeform(t *testing.T) {
	msgCh := make(chan tea.Msg, 2)
	mockSend := func(msg tea.Msg) { msgCh <- msg }

	inner := &mockAutopilot{freeformResult: "custom input"}
	iv := NewAutopilotTUIInterviewer(inner, mockSend)

	done := make(chan string, 1)
	go func() {
		result, err := iv.AskFreeform("Enter text")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		done <- result
	}()

	gate := drainAndReply(msgCh)
	if gate.Decision != "custom input" {
		t.Errorf("expected decision 'custom input', got %q", gate.Decision)
	}

	select {
	case result := <-done:
		if result != "custom input" {
			t.Errorf("expected 'custom input', got %q", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for AskFreeform result")
	}
}

func TestAutopilotTUIInterviewerAskFreeformWithLabels(t *testing.T) {
	msgCh := make(chan tea.Msg, 2)
	mockSend := func(msg tea.Msg) { msgCh <- msg }

	inner := &mockAutopilot{labeledResult: "approve"}
	iv := NewAutopilotTUIInterviewer(inner, mockSend)

	done := make(chan string, 1)
	go func() {
		result, err := iv.AskFreeformWithLabels("Review", []string{"approve", "reject"}, "approve")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		done <- result
	}()

	gate := drainAndReply(msgCh)
	if gate.Decision != "approve" {
		t.Errorf("expected decision 'approve', got %q", gate.Decision)
	}
	if len(gate.Labels) != 2 {
		t.Errorf("expected 2 labels, got %d", len(gate.Labels))
	}
	if gate.Default != "approve" {
		t.Errorf("expected default 'approve', got %q", gate.Default)
	}

	select {
	case result := <-done:
		if result != "approve" {
			t.Errorf("expected 'approve', got %q", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for AskFreeformWithLabels result")
	}
}

func TestAutopilotTUIInterviewerAskError(t *testing.T) {
	msgCh := make(chan tea.Msg, 2)
	mockSend := func(msg tea.Msg) { msgCh <- msg }

	inner := &mockAutopilot{askErr: fmt.Errorf("autopilot failed")}
	iv := NewAutopilotTUIInterviewer(inner, mockSend)

	_, err := iv.Ask("Pick", []string{"a"}, "a")
	if err == nil {
		t.Fatal("expected error from inner autopilot")
	}
	if err.Error() != "autopilot failed" {
		t.Errorf("expected 'autopilot failed', got %q", err.Error())
	}

	// No message should be sent to TUI on error.
	select {
	case msg := <-msgCh:
		t.Errorf("expected no message on error, got %T", msg)
	default:
		// expected
	}
}

func TestAutopilotTUIInterviewerSendsDismiss(t *testing.T) {
	msgCh := make(chan tea.Msg, 4)
	mockSend := func(msg tea.Msg) { msgCh <- msg }

	inner := &mockAutopilot{askResult: "ok"}
	iv := NewAutopilotTUIInterviewer(inner, mockSend)

	done := make(chan struct{})
	go func() {
		iv.Ask("Pick", []string{"ok"}, "ok")
		close(done)
	}()

	// First message: MsgGateAutopilot
	first := <-msgCh
	gate, ok := first.(MsgGateAutopilot)
	if !ok {
		t.Fatalf("expected MsgGateAutopilot, got %T", first)
	}
	gate.ReplyCh <- gate.Decision

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Ask to return")
	}

	// Second message: MsgModalDismiss
	select {
	case second := <-msgCh:
		if _, ok := second.(MsgModalDismiss); !ok {
			t.Errorf("expected MsgModalDismiss, got %T", second)
		}
	case <-time.After(time.Second):
		t.Error("expected MsgModalDismiss after reply")
	}
}

func TestDecisionString(t *testing.T) {
	got := DecisionString("approve")
	if got != "Autopilot chose: approve" {
		t.Errorf("expected 'Autopilot chose: approve', got %q", got)
	}
}
