// ABOUTME: Tests for tool output truncation.
// ABOUTME: Validates that long outputs are truncated keeping the tail, with a marker showing cut amount.
package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
)

func stubCall(name string) llm.ToolCallData {
	return llm.ToolCallData{
		ID:        "call_trunc",
		Name:      name,
		Arguments: json.RawMessage(`{}`),
	}
}

func TestTruncateOutputShortUnchanged(t *testing.T) {
	input := "short output"
	got := truncateOutput(input, 8000)
	if got != input {
		t.Errorf("expected unchanged output, got %q", got)
	}
}

func TestTruncateOutputEmptyUnchanged(t *testing.T) {
	got := truncateOutput("", 8000)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestTruncateOutputExactlyAtLimitUnchanged(t *testing.T) {
	input := strings.Repeat("x", 8000)
	got := truncateOutput(input, 8000)
	if got != input {
		t.Errorf("expected unchanged output of length %d, got length %d", len(input), len(got))
	}
}

func TestTruncateOutputLongTruncatedWithMarker(t *testing.T) {
	input := strings.Repeat("a", 10000)
	got := truncateOutput(input, 8000)

	if len(got) == len(input) {
		t.Fatal("expected output to be truncated")
	}

	if !strings.HasPrefix(got, "[... truncated") {
		t.Errorf("expected truncation marker prefix, got %q", got[:50])
	}

	if !strings.Contains(got, "characters ...]") {
		t.Error("expected truncation marker to contain 'characters ...]'")
	}
}

func TestTruncateOutputPreservesTail(t *testing.T) {
	// Build input: some head content + a known tail
	tail := "THIS_IS_THE_TAIL_CONTENT"
	head := strings.Repeat("x", 10000)
	input := head + tail

	got := truncateOutput(input, 8000)

	if !strings.HasSuffix(got, tail) {
		t.Errorf("expected output to end with tail content %q, got suffix %q",
			tail, got[len(got)-len(tail):])
	}
}

func TestTruncateOutputMarkerShowsCorrectCharCount(t *testing.T) {
	input := strings.Repeat("a", 12000)
	maxLen := 8000
	got := truncateOutput(input, maxLen)

	// The tail portion is maxLen/2 = 4000 characters
	// So truncated amount is 12000 - 4000 = 8000
	tailLen := maxLen / 2
	truncatedCount := len(input) - tailLen
	expectedMarker := fmt.Sprintf("[... truncated %d characters ...]\n", truncatedCount)

	if !strings.HasPrefix(got, expectedMarker) {
		t.Errorf("expected marker %q, got prefix %q", expectedMarker, got[:len(expectedMarker)+10])
	}
}

func TestTruncateOutputIntegrationWithExecute(t *testing.T) {
	// Verify that Execute applies truncation to successful outputs
	longOutput := strings.Repeat("z", 10000)
	r := NewRegistry()
	r.Register(&stubTool{name: "verbose", result: longOutput})

	call := stubCall("verbose")
	result := r.Execute(t.Context(), call)

	if result.IsError {
		t.Fatal("expected no error")
	}

	if len(result.Content) >= len(longOutput) {
		t.Errorf("expected truncated output (got len %d, original %d)", len(result.Content), len(longOutput))
	}

	if !strings.HasPrefix(result.Content, "[... truncated") {
		t.Error("expected truncation marker in result")
	}
}

func TestTruncateOutputErrorsNotTruncated(t *testing.T) {
	// Verify error outputs are NOT truncated by Execute
	r := NewRegistry()
	// Execute with unknown tool produces an error result - those should not be truncated
	call := stubCall("nonexistent")
	result := r.Execute(t.Context(), call)

	if !result.IsError {
		t.Fatal("expected error result")
	}

	// Error content should be as-is, not truncated
	if strings.HasPrefix(result.Content, "[... truncated") {
		t.Error("error output should not be truncated")
	}
}
