// ABOUTME: Tests for normalized LLM trace event building from stream events.
package llm

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
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

func TestTraceBuilderPreservesSpacingInTextDeltas(t *testing.T) {
	builder := NewTraceBuilder(TraceOptions{Provider: "anthropic", Model: "claude-opus-4-6"})

	// Streaming APIs send chunks with leading spaces as word separators.
	builder.Process(StreamEvent{Type: EventTextDelta, Delta: "Now"})
	builder.Process(StreamEvent{Type: EventTextDelta, Delta: " I have"})
	builder.Process(StreamEvent{Type: EventTextDelta, Delta: " a clear"})

	events := builder.Events()
	if len(events) != 3 {
		t.Fatalf("expected 3 text events, got %d", len(events))
	}
	// The leading space must survive so coalesced text reads "Now I have a clear".
	if events[1].Preview != " I have" {
		t.Errorf("expected leading space preserved, got %q", events[1].Preview)
	}
	if events[2].Preview != " a clear" {
		t.Errorf("expected leading space preserved, got %q", events[2].Preview)
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

// TestTraceBuilderStampsCallID pins that every event from one request shares a
// call id. Two log paths record LLM activity (the agent session re-emits trace
// events; the client-level writer catches calls no session sees), so without a
// shared id a reader cannot tell one call seen twice from two calls.
func TestTraceBuilderStampsCallID(t *testing.T) {
	b := NewTraceBuilder(TraceOptions{Provider: "anthropic", Model: "m", Verbose: true, CallID: "call-1"})
	b.Process(StreamEvent{Type: EventStreamStart})
	b.Process(StreamEvent{Type: EventTextDelta, Delta: "hi"})
	b.Process(StreamEvent{Type: EventReasoningDelta, ReasoningDelta: "think"})
	b.Process(StreamEvent{Type: EventToolCallStart, ToolCall: &ToolCallData{Name: "bash", Arguments: []byte(`{"command":"ls"}`)}})
	b.Process(StreamEvent{Type: EventProviderEvent, Raw: []byte(`{"type":"x"}`)})
	b.Process(StreamEvent{Type: EventFinish, FinishReason: &FinishReason{Reason: "stop"}})

	events := b.Events()
	if len(events) != 6 {
		t.Fatalf("got %d events, want 6", len(events))
	}
	for _, evt := range events {
		if evt.CallID != "call-1" {
			t.Errorf("%s CallID = %q, want call-1", evt.Kind, evt.CallID)
		}
	}
}

// TestTraceBuilderCarriesRequestRaw pins that the wire body reaches the trace
// event. The normalized Request records what tracker asked for; only the body
// records what went out after provider translation.
func TestTraceBuilderCarriesRequestRaw(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	b := NewTraceBuilder(TraceOptions{Provider: "anthropic", Model: "m"})
	b.Process(StreamEvent{Type: EventStreamStart, RequestRaw: body})

	events := b.Events()
	if len(events) != 1 || events[0].Kind != TraceRequestStart {
		t.Fatalf("got %+v, want one request_start", events)
	}
	if string(events[0].RequestRaw) != string(body) {
		t.Errorf("RequestRaw = %q, want the wire body", events[0].RequestRaw)
	}
}

// TestTraceBuilderKeepsFullToolArguments pins the split between the display
// field and the log field: Preview is clipped to tracePreviewLimit so a TUI
// line stays one line, while ToolArguments stays whole. Before the split, an
// argument list longer than 80 chars reached disk truncated with no marker
// that anything was missing.
func TestTraceBuilderKeepsFullToolArguments(t *testing.T) {
	args := []byte(`{"command":"` + strings.Repeat("x", 200) + `"}`)
	b := NewTraceBuilder(TraceOptions{Provider: "anthropic", Model: "m"})
	b.Process(StreamEvent{Type: EventToolCallStart, ToolCall: &ToolCallData{Name: "bash", Arguments: args}})

	evt := b.Events()[0]
	if string(evt.ToolArguments) != string(args) {
		t.Errorf("ToolArguments length = %d, want %d (untruncated)", len(evt.ToolArguments), len(args))
	}
	// Count runes, not bytes: previewText clips to tracePreviewLimit
	// characters and appends a 3-byte ellipsis, so the byte length exceeds
	// the limit while the display width does not.
	if n := utf8.RuneCountInString(evt.Preview); n > tracePreviewLimit {
		t.Errorf("Preview = %d runes, want <= %d (clipped for display)", n, tracePreviewLimit)
	}
	if string(evt.Preview) == string(args) {
		t.Error("Preview should be the clipped form, not the full arguments")
	}
}

// TestTraceBuilderKeepsFullProviderRaw is the provider-chunk equivalent of the
// tool-argument split above.
func TestTraceBuilderKeepsFullProviderRaw(t *testing.T) {
	raw := []byte(`{"type":"content_block_delta","delta":{"text":"` + strings.Repeat("y", 200) + `"}}`)
	b := NewTraceBuilder(TraceOptions{Provider: "anthropic", Model: "m", Verbose: true})
	b.Process(StreamEvent{Type: EventProviderEvent, Raw: raw})

	evt := b.Events()[0]
	if string(evt.ProviderRaw) != string(raw) {
		t.Errorf("ProviderRaw length = %d, want %d (untruncated)", len(evt.ProviderRaw), len(raw))
	}
	if n := utf8.RuneCountInString(evt.RawPreview); n > tracePreviewLimit {
		t.Errorf("RawPreview = %d runes, want <= %d (clipped)", n, tracePreviewLimit)
	}
}

// TestNewCallIDIsUnique guards the only property the id needs: distinctness
// within one run's log.
func TestNewCallIDIsUnique(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		id := NewCallID()
		if id == "" {
			t.Fatal("NewCallID returned empty")
		}
		if seen[id] {
			t.Fatalf("duplicate call id %q after %d draws", id, i)
		}
		seen[id] = true
	}
}

// TestTraceBuilderFoldsRequestSentIntoStreamStart pins that the adapter's
// EventRequestSent produces no trace event of its own: it is stashed and folded
// into the single request_start, so the log does not gain a second
// request-start line for every call.
func TestTraceBuilderFoldsRequestSentIntoStreamStart(t *testing.T) {
	body := []byte(`{"model":"m","stream":true}`)
	b := NewTraceBuilder(TraceOptions{Provider: "anthropic", Model: "m"})

	b.Process(StreamEvent{Type: EventRequestSent, RequestRaw: body})
	if got := len(b.Events()); got != 0 {
		t.Fatalf("EventRequestSent emitted %d trace events, want 0", got)
	}

	b.Process(StreamEvent{Type: EventStreamStart, Raw: []byte(`{"type":"message_start"}`)})
	events := b.Events()
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1 request_start", len(events))
	}
	if events[0].Kind != TraceRequestStart {
		t.Fatalf("Kind = %q, want %q", events[0].Kind, TraceRequestStart)
	}
	if string(events[0].RequestRaw) != string(body) {
		t.Errorf("RequestRaw = %q, want the stashed wire body", events[0].RequestRaw)
	}
}

// TestStreamAccumulatorIgnoresRequestSent guards the other half of the
// contract: the new event type must not disturb response assembly.
func TestStreamAccumulatorIgnoresRequestSent(t *testing.T) {
	acc := NewStreamAccumulator()
	acc.Process(StreamEvent{Type: EventRequestSent, RequestRaw: []byte(`{"model":"m"}`)})
	acc.Process(StreamEvent{Type: EventTextDelta, Delta: "hi"})
	acc.Process(StreamEvent{Type: EventFinish, FinishReason: &FinishReason{Reason: "stop"}})

	resp := acc.Response()
	if got := resp.Text(); got != "hi" {
		t.Errorf("Text() = %q, want hi", got)
	}
}

// TestFirstRaw covers the fallback ordering.
func TestFirstRaw(t *testing.T) {
	if got := firstRaw(nil, []byte("b")); string(got) != "b" {
		t.Errorf("firstRaw(nil, b) = %q, want b", got)
	}
	if got := firstRaw([]byte("a"), []byte("b")); string(got) != "a" {
		t.Errorf("firstRaw(a, b) = %q, want a (first non-empty wins)", got)
	}
	if got := firstRaw(nil, nil); got != nil {
		t.Errorf("firstRaw(nil, nil) = %q, want nil", got)
	}
}
