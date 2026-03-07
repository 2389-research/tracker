// ABOUTME: Tests for normalized LLM trace event building from stream events.
package llm

import (
	"encoding/json"
	"testing"
)

func TestTraceBuilderEmitsNormalizedEvents(t *testing.T) {
	builder := NewTraceBuilder(TraceOptions{Provider: "anthropic", Model: "claude-opus-4-6"})

	builder.Process(StreamEvent{Type: EventStreamStart})
	builder.Process(StreamEvent{Type: EventReasoningStart})
	builder.Process(StreamEvent{Type: EventReasoningDelta, ReasoningDelta: "checking files"})
	builder.Process(StreamEvent{
		Type: EventToolCallStart,
		ToolCall: &ToolCallData{
			Name:      "read",
			Arguments: json.RawMessage(`{"path":"go.mod"}`),
		},
	})
	builder.Process(StreamEvent{Type: EventToolCallEnd})
	builder.Process(StreamEvent{
		Type:         EventFinish,
		FinishReason: &FinishReason{Reason: "tool_calls", Raw: "tool_use"},
		Usage:        &Usage{InputTokens: 12, OutputTokens: 3},
	})

	events := builder.Events()

	if len(events) != 4 {
		t.Fatalf("expected 4 trace events, got %d", len(events))
	}
	if events[0].Kind != TraceRequestStart {
		t.Fatalf("events[0].Kind = %q, want %q", events[0].Kind, TraceRequestStart)
	}
	if events[1].Kind != TraceReasoning {
		t.Fatalf("events[1].Kind = %q, want %q", events[1].Kind, TraceReasoning)
	}
	if events[2].Kind != TraceToolPrepare {
		t.Fatalf("events[2].Kind = %q, want %q", events[2].Kind, TraceToolPrepare)
	}
	if events[2].ToolName != "read" {
		t.Fatalf("events[2].ToolName = %q, want %q", events[2].ToolName, "read")
	}
	if events[3].Kind != TraceFinish {
		t.Fatalf("events[3].Kind = %q, want %q", events[3].Kind, TraceFinish)
	}
	if events[3].FinishReason != "tool_calls" {
		t.Fatalf("events[3].FinishReason = %q, want %q", events[3].FinishReason, "tool_calls")
	}
	for _, evt := range events {
		if evt.Kind == TraceProviderRaw {
			t.Fatal("did not expect raw provider events in non-verbose mode")
		}
	}
}

func TestTraceBuilderEmitsProviderRawOnlyInVerboseMode(t *testing.T) {
	builder := NewTraceBuilder(TraceOptions{
		Provider: "openai",
		Model:    "gpt-5.2",
		Verbose:  true,
	})

	builder.Process(StreamEvent{
		Type: EventProviderEvent,
		Raw:  json.RawMessage(`{"type":"response.output_item.added"}`),
	})

	events := builder.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 trace event, got %d", len(events))
	}
	if events[0].Kind != TraceProviderRaw {
		t.Fatalf("events[0].Kind = %q, want %q", events[0].Kind, TraceProviderRaw)
	}
	if events[0].RawPreview == "" {
		t.Fatal("expected raw preview to be populated")
	}
}
