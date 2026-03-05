// ABOUTME: Tests for mid-session steering channel injection into the agent loop.
// ABOUTME: Verifies steering messages are read, injected as user messages, and emit correct events.
package agent

import (
	"context"
	"testing"

	"github.com/2389-research/mammoth-lite/llm"
)

func TestWithSteering_SetsChannel(t *testing.T) {
	ch := make(chan string, 1)
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Hi"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	sess := mustNewSession(t, client, cfg, WithSteering(ch))

	if sess.steering == nil {
		t.Fatal("expected steering channel to be set on session")
	}
	if sess.steering != ch {
		t.Fatal("expected steering channel to match the one provided")
	}
}

func TestSession_SteeringInjection(t *testing.T) {
	// Buffer a steering message before Run so it's available on the first turn.
	ch := make(chan string, 1)
	ch <- "please focus on error handling"

	// The session will do: turn 1 (reads steering, calls LLM) -> text response -> done.
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Understood, focusing on error handling."),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	var events []Event
	handler := EventHandlerFunc(func(evt Event) {
		events = append(events, evt)
	})

	cfg := DefaultConfig()
	sess := mustNewSession(t, client, cfg, WithSteering(ch), WithEventHandler(handler))

	result, err := sess.Run(context.Background(), "Write some code")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Turns != 1 {
		t.Errorf("expected 1 turn, got %d", result.Turns)
	}

	// Verify the steering event was emitted.
	foundSteering := false
	for _, evt := range events {
		if evt.Type == EventSteeringInjected {
			foundSteering = true
			if evt.Text != "please focus on error handling" {
				t.Errorf("expected steering text 'please focus on error handling', got %q", evt.Text)
			}
			break
		}
	}
	if !foundSteering {
		t.Error("expected EventSteeringInjected event to be emitted")
	}

	// Verify the steering message was injected into conversation by checking the
	// messages sent to the LLM. The mock completer received the request with the
	// steering message appended before the LLM call.
	// We check that at least one message contains the [STEERING] prefix.
	foundInMessages := false
	for _, msg := range sess.messages {
		if msg.Role == llm.RoleUser {
			text := msg.Text()
			if text == "[STEERING] please focus on error handling" {
				foundInMessages = true
				break
			}
		}
	}
	if !foundInMessages {
		t.Error("expected steering message to appear in session messages with [STEERING] prefix")
	}
}

func TestSession_SteeringNoChannel(t *testing.T) {
	// Session without steering should work normally without panicking.
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Hello!"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	sess := mustNewSession(t, client, cfg)

	result, err := sess.Run(context.Background(), "Say hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Turns != 1 {
		t.Errorf("expected 1 turn, got %d", result.Turns)
	}

	// Verify no steering channel is set.
	if sess.steering != nil {
		t.Error("expected steering channel to be nil when not configured")
	}
}

func TestSession_SteeringEventEmitted(t *testing.T) {
	ch := make(chan string, 1)
	ch <- "switch to verbose mode"

	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Switching to verbose mode."),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	var events []Event
	handler := EventHandlerFunc(func(evt Event) {
		events = append(events, evt)
	})

	cfg := DefaultConfig()
	sess := mustNewSession(t, client, cfg, WithSteering(ch), WithEventHandler(handler))

	_, err := sess.Run(context.Background(), "Do something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find the steering event and verify its fields.
	var steeringEvents []Event
	for _, evt := range events {
		if evt.Type == EventSteeringInjected {
			steeringEvents = append(steeringEvents, evt)
		}
	}

	if len(steeringEvents) != 1 {
		t.Fatalf("expected exactly 1 steering event, got %d", len(steeringEvents))
	}

	evt := steeringEvents[0]
	if evt.Text != "switch to verbose mode" {
		t.Errorf("expected steering event text 'switch to verbose mode', got %q", evt.Text)
	}
	if evt.SessionID != sess.id {
		t.Errorf("expected session ID %q, got %q", sess.id, evt.SessionID)
	}
}
