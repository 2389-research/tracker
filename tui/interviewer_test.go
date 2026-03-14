// ABOUTME: Tests for the BubbleteaInterviewer gate bridge.
// ABOUTME: Verifies Mode 2 gate request/reply flow via channel-based mock.
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestInterviewerMode2SendsGateChoice(t *testing.T) {
	msgCh := make(chan tea.Msg, 1)
	mockSend := func(msg tea.Msg) { msgCh <- msg }
	iv := NewBubbleteaInterviewer(mockSend)

	done := make(chan string, 1)
	go func() {
		result, err := iv.Ask("Pick one", []string{"a", "b"}, "a")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		done <- result
	}()

	sentMsg := <-msgCh
	msg, ok := sentMsg.(MsgGateChoice)
	if !ok {
		t.Fatalf("expected MsgGateChoice, got %T", sentMsg)
	}
	msg.ReplyCh <- "b"

	result := <-done
	if result != "b" {
		t.Errorf("expected 'b', got %q", result)
	}
}
