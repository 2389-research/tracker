// ABOUTME: Tests for console formatting of structured LLM trace events.
package llm

import (
	"bytes"
	"strings"
	"testing"
)

func TestTraceLoggerDefaultOmitsRawProviderEvents(t *testing.T) {
	var buf bytes.Buffer
	logger := NewTraceLogger(&buf, TraceLoggerOptions{Verbose: false})

	logger.HandleTraceEvent(TraceEvent{
		Kind:          TraceProviderRaw,
		ProviderEvent: "message_delta",
		RawPreview:    `{"x":1}`,
	})

	if buf.Len() != 0 {
		t.Fatalf("expected raw provider events to be hidden by default, got %q", buf.String())
	}
}

func TestTraceLoggerWritesNormalizedEvents(t *testing.T) {
	var buf bytes.Buffer
	logger := NewTraceLogger(&buf, TraceLoggerOptions{})

	logger.HandleTraceEvent(TraceEvent{
		Kind:     TraceToolPrepare,
		Provider: "anthropic",
		Model:    "claude-opus-4-6",
		ToolName: "read",
		Preview:  `{"path":"go.mod"}`,
	})

	got := buf.String()
	if !strings.Contains(got, "llm tool prepare anthropic/claude-opus-4-6") {
		t.Fatalf("expected provider/model in output, got %q", got)
	}
	if !strings.Contains(got, "name=read") {
		t.Fatalf("expected tool name in output, got %q", got)
	}
}
