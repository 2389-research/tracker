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
