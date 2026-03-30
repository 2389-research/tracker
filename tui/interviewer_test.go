// ABOUTME: Tests for the BubbleteaInterviewer gate bridge.
// ABOUTME: Verifies Mode 2 gate request/reply flow via channel-based mock.
package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/2389-research/tracker/pipeline/handlers"
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

func TestInterviewerMode2SendsGateInterview(t *testing.T) {
	var sent tea.Msg
	bi := NewBubbleteaInterviewer(func(msg tea.Msg) { sent = msg })

	questions := []handlers.Question{
		{Index: 1, Text: "Test?", Options: []string{"a", "b"}},
	}

	// Start AskInterview in goroutine (it blocks on channel)
	done := make(chan struct{})
	var result *handlers.InterviewResult
	var askErr error
	go func() {
		result, askErr = bi.AskInterview(questions, nil)
		close(done)
	}()

	// Wait a moment for the message to be sent
	time.Sleep(10 * time.Millisecond)

	// Verify the message was sent
	msg, ok := sent.(MsgGateInterview)
	if !ok {
		t.Fatalf("expected MsgGateInterview, got %T", sent)
	}
	if len(msg.Questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(msg.Questions))
	}

	// Simulate reply
	reply := `{"questions":[{"id":"q1","text":"Test?","answer":"a"}]}`
	msg.ReplyCh <- reply

	<-done
	if askErr != nil {
		t.Fatalf("unexpected error: %v", askErr)
	}
	if result.Questions[0].Answer != "a" {
		t.Errorf("expected answer 'a', got %q", result.Questions[0].Answer)
	}
}
