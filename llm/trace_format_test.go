// ABOUTME: Tests for human-readable formatting of LLM trace events.
package llm

import (
	"strings"
	"testing"
)

func TestFormatTraceLineVerboseIncludesProviderEvent(t *testing.T) {
	line := FormatTraceLine(TraceEvent{
		Kind:          TraceProviderRaw,
		Provider:      "openai",
		Model:         "gpt-5.2",
		ProviderEvent: "response.output_item.added",
		RawPreview:    `{"type":"function_call"}`,
	}, true)

	if !strings.Contains(line, "response.output_item.added") {
		t.Fatalf("expected provider event in line: %q", line)
	}
	if !strings.Contains(line, "function_call") {
		t.Fatalf("expected raw preview in line: %q", line)
	}
}

func TestFormatTraceLineOmitsProviderRawWhenNotVerbose(t *testing.T) {
	line := FormatTraceLine(TraceEvent{
		Kind:          TraceProviderRaw,
		ProviderEvent: "message_delta",
		RawPreview:    `{"type":"delta"}`,
	}, false)

	if line != "" {
		t.Fatalf("expected empty line for non-verbose provider raw event, got %q", line)
	}
}
