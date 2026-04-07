// ABOUTME: Tests for streaming event types and channel-based streaming.
// ABOUTME: Validates StreamEvent construction and StreamAccumulator.
package llm

import (
	"encoding/json"
	"testing"
)

func TestStreamEventTextDelta(t *testing.T) {
	event := StreamEvent{
		Type:  EventTextDelta,
		Delta: "Hello",
	}
	if event.Type != EventTextDelta {
		t.Errorf("expected TextDelta, got %v", event.Type)
	}
	if event.Delta != "Hello" {
		t.Errorf("expected Hello, got %q", event.Delta)
	}
}

func TestStreamAccumulator(t *testing.T) {
	acc := NewStreamAccumulator()

	acc.Process(StreamEvent{Type: EventStreamStart})
	acc.Process(StreamEvent{Type: EventTextStart, TextID: "t1"})
	acc.Process(StreamEvent{Type: EventTextDelta, Delta: "Hello ", TextID: "t1"})
	acc.Process(StreamEvent{Type: EventTextDelta, Delta: "world", TextID: "t1"})
	acc.Process(StreamEvent{Type: EventTextEnd, TextID: "t1"})
	acc.Process(StreamEvent{
		Type:         EventFinish,
		FinishReason: &FinishReason{Reason: "stop"},
		Usage:        &Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	})

	resp := acc.Response()
	if resp.Text() != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", resp.Text())
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("expected 15 total tokens, got %d", resp.Usage.TotalTokens)
	}
	if resp.FinishReason.Reason != "stop" {
		t.Errorf("expected stop, got %q", resp.FinishReason.Reason)
	}
}

func TestStreamAccumulatorReasoningSignature(t *testing.T) {
	acc := NewStreamAccumulator()

	acc.Process(StreamEvent{Type: EventReasoningStart})
	acc.Process(StreamEvent{Type: EventReasoningDelta, ReasoningDelta: "Let me think..."})
	acc.Process(StreamEvent{Type: EventReasoningSignature, ReasoningSignature: "sig_ABC123"})
	acc.Process(StreamEvent{Type: EventReasoningEnd})
	acc.Process(StreamEvent{Type: EventTextStart, TextID: "t1"})
	acc.Process(StreamEvent{Type: EventTextDelta, Delta: "Answer.", TextID: "t1"})
	acc.Process(StreamEvent{Type: EventTextEnd, TextID: "t1"})
	acc.Process(StreamEvent{
		Type:         EventFinish,
		FinishReason: &FinishReason{Reason: "stop"},
		Usage:        &Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	})

	resp := acc.Response()

	// Thinking should come before text.
	if len(resp.Message.Content) < 2 {
		t.Fatalf("expected at least 2 content parts, got %d", len(resp.Message.Content))
	}
	if resp.Message.Content[0].Kind != KindThinking {
		t.Errorf("first part should be thinking, got %s", resp.Message.Content[0].Kind)
	}
	if resp.Message.Content[0].Thinking == nil {
		t.Fatal("thinking data should not be nil")
	}
	if resp.Message.Content[0].Thinking.Text != "Let me think..." {
		t.Errorf("thinking text = %q", resp.Message.Content[0].Thinking.Text)
	}
	if resp.Message.Content[0].Thinking.Signature != "sig_ABC123" {
		t.Errorf("thinking signature = %q, want sig_ABC123", resp.Message.Content[0].Thinking.Signature)
	}
	if resp.Message.Content[1].Kind != KindText {
		t.Errorf("second part should be text, got %s", resp.Message.Content[1].Kind)
	}
}

func TestStreamAccumulatorRedactedThinking(t *testing.T) {
	acc := NewStreamAccumulator()

	// Redacted thinking block (opaque data blob)
	acc.Process(StreamEvent{Type: EventRedactedThinking, ReasoningSignature: "opaque_data_1"})
	acc.Process(StreamEvent{Type: EventRedactedThinking, ReasoningSignature: "opaque_data_2"})
	acc.Process(StreamEvent{Type: EventTextStart, TextID: "t1"})
	acc.Process(StreamEvent{Type: EventTextDelta, Delta: "Hello", TextID: "t1"})
	acc.Process(StreamEvent{Type: EventTextEnd, TextID: "t1"})
	acc.Process(StreamEvent{
		Type:         EventFinish,
		FinishReason: &FinishReason{Reason: "stop"},
	})

	resp := acc.Response()

	// Should have: 2 redacted_thinking + 1 text
	if len(resp.Message.Content) != 3 {
		t.Fatalf("expected 3 content parts, got %d", len(resp.Message.Content))
	}
	if resp.Message.Content[0].Kind != KindRedactedThinking {
		t.Errorf("part[0] = %s, want redacted_thinking", resp.Message.Content[0].Kind)
	}
	if resp.Message.Content[0].Thinking.Signature != "opaque_data_1" {
		t.Errorf("part[0] data = %q", resp.Message.Content[0].Thinking.Signature)
	}
	if resp.Message.Content[1].Kind != KindRedactedThinking {
		t.Errorf("part[1] = %s, want redacted_thinking", resp.Message.Content[1].Kind)
	}
	if resp.Message.Content[2].Kind != KindText {
		t.Errorf("part[2] = %s, want text", resp.Message.Content[2].Kind)
	}
}

func TestStreamAccumulatorSignatureOnlyThinking(t *testing.T) {
	// When display: "omitted", thinking block has no text but does have a signature.
	acc := NewStreamAccumulator()

	acc.Process(StreamEvent{Type: EventReasoningStart})
	// No thinking_delta events — display is omitted
	acc.Process(StreamEvent{Type: EventReasoningSignature, ReasoningSignature: "sig_omitted"})
	acc.Process(StreamEvent{Type: EventReasoningEnd})
	acc.Process(StreamEvent{Type: EventTextStart, TextID: "t1"})
	acc.Process(StreamEvent{Type: EventTextDelta, Delta: "Result", TextID: "t1"})
	acc.Process(StreamEvent{Type: EventTextEnd, TextID: "t1"})
	acc.Process(StreamEvent{Type: EventFinish, FinishReason: &FinishReason{Reason: "stop"}})

	resp := acc.Response()

	// Should still include thinking part with signature even though text is empty.
	if len(resp.Message.Content) < 2 {
		t.Fatalf("expected at least 2 parts, got %d", len(resp.Message.Content))
	}
	if resp.Message.Content[0].Kind != KindThinking {
		t.Fatalf("first part should be thinking, got %s", resp.Message.Content[0].Kind)
	}
	if resp.Message.Content[0].Thinking.Signature != "sig_omitted" {
		t.Errorf("signature = %q, want sig_omitted", resp.Message.Content[0].Thinking.Signature)
	}
}

func TestStreamAccumulatorPreservesToolCallThoughtSignature(t *testing.T) {
	acc := NewStreamAccumulator()

	acc.Process(StreamEvent{
		Type: EventToolCallStart,
		ToolCall: &ToolCallData{
			ID:             "call_1",
			Name:           "bash",
			Arguments:      json.RawMessage(`{"command":"ls"}`),
			ThoughtSigData: "sig-123",
		},
	})
	acc.Process(StreamEvent{Type: EventToolCallEnd})

	resp := acc.Response()
	calls := resp.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].ThoughtSigData != "sig-123" {
		t.Fatalf("ThoughtSigData = %q, want %q", calls[0].ThoughtSigData, "sig-123")
	}
}
