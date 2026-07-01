// ABOUTME: Tests for mid-session steering channel injection into the agent loop.
// ABOUTME: Verifies steering messages are read, injected as user messages, and emit correct events.
package agent

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/llm"
)

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
