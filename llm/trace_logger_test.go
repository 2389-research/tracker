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

func TestTraceLoggerBatchesTextDeltas(t *testing.T) {
	var buf bytes.Buffer
	logger := NewTraceLogger(&buf, TraceLoggerOptions{})

	// Simulate a streaming response: start, multiple text deltas, finish
	logger.HandleTraceEvent(TraceEvent{
		Kind:     TraceRequestStart,
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
	})
	logger.HandleTraceEvent(TraceEvent{
		Kind:     TraceText,
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
		Preview:  "Now",
	})
	logger.HandleTraceEvent(TraceEvent{
		Kind:     TraceText,
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
		Preview:  "I have a clear",
	})
	logger.HandleTraceEvent(TraceEvent{
		Kind:     TraceText,
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
		Preview:  "picture",
	})
	logger.HandleTraceEvent(TraceEvent{
		Kind:         TraceFinish,
		Provider:     "anthropic",
		Model:        "claude-sonnet-4-6",
		FinishReason: "stop",
		Usage:        Usage{InputTokens: 100, OutputTokens: 20},
	})

	got := buf.String()
	// Should have exactly one "llm text" line (batched), not three separate lines
	textLineCount := strings.Count(got, "llm text")
	if textLineCount != 1 {
		t.Errorf("expected 1 batched 'llm text' line, got %d in:\n%s", textLineCount, got)
	}
	// The batched line should contain the accumulated preview
	if !strings.Contains(got, "Now") || !strings.Contains(got, "picture") {
		t.Errorf("expected batched text to contain accumulated preview, got:\n%s", got)
	}
}

func TestTraceLoggerBatchNotBrokenByHiddenProviderRaw(t *testing.T) {
	// Reproduces the real production bug: TraceProviderRaw events (verbose=true
	// on TraceBuilder) are interspersed between TraceText events. When the
	// logger has verbose=false, these raw events should NOT flush the text batch.
	var buf bytes.Buffer
	logger := NewTraceLogger(&buf, TraceLoggerOptions{Verbose: false})

	logger.HandleTraceEvent(TraceEvent{
		Kind:     TraceRequestStart,
		Provider: "anthropic",
		Model:    "claude-opus-4-6",
	})
	// Simulate: text, raw, text, raw, text, raw, finish
	for _, word := range []string{"Now", "I have a clear", "picture"} {
		logger.HandleTraceEvent(TraceEvent{
			Kind:     TraceText,
			Provider: "anthropic",
			Model:    "claude-opus-4-6",
			Preview:  word,
		})
		// Interspersed raw provider event (emitted by TraceBuilder with Verbose=true)
		logger.HandleTraceEvent(TraceEvent{
			Kind:          TraceProviderRaw,
			Provider:      "anthropic",
			Model:         "claude-opus-4-6",
			ProviderEvent: "content_block_delta",
			RawPreview:    `{"type":"content_block_delta"}`,
		})
	}
	logger.HandleTraceEvent(TraceEvent{
		Kind:         TraceFinish,
		Provider:     "anthropic",
		Model:        "claude-opus-4-6",
		FinishReason: "stop",
		Usage:        Usage{InputTokens: 100, OutputTokens: 20},
	})

	got := buf.String()
	textLineCount := strings.Count(got, "llm text")
	if textLineCount != 1 {
		t.Errorf("expected 1 batched 'llm text' line, got %d in:\n%s", textLineCount, got)
	}
}

func TestTraceLoggerBatchesReasoningDeltas(t *testing.T) {
	var buf bytes.Buffer
	logger := NewTraceLogger(&buf, TraceLoggerOptions{})

	logger.HandleTraceEvent(TraceEvent{
		Kind:     TraceRequestStart,
		Provider: "anthropic",
		Model:    "claude-opus-4-6",
	})
	logger.HandleTraceEvent(TraceEvent{
		Kind:     TraceReasoning,
		Provider: "anthropic",
		Model:    "claude-opus-4-6",
		Preview:  "Let me think",
	})
	logger.HandleTraceEvent(TraceEvent{
		Kind:     TraceReasoning,
		Provider: "anthropic",
		Model:    "claude-opus-4-6",
		Preview:  "about this",
	})
	logger.HandleTraceEvent(TraceEvent{
		Kind:         TraceFinish,
		Provider:     "anthropic",
		Model:        "claude-opus-4-6",
		FinishReason: "stop",
		Usage:        Usage{InputTokens: 50, OutputTokens: 10},
	})

	got := buf.String()
	thinkingCount := strings.Count(got, "llm thinking")
	if thinkingCount != 1 {
		t.Errorf("expected 1 batched 'llm thinking' line, got %d in:\n%s", thinkingCount, got)
	}
}

func TestTraceLoggerFlushDrainsBatch(t *testing.T) {
	// When a stream terminates with an error (no TraceFinish), Flush()
	// should drain any accumulated text/reasoning batch so output is not lost.
	var buf bytes.Buffer
	logger := NewTraceLogger(&buf, TraceLoggerOptions{})

	logger.HandleTraceEvent(TraceEvent{
		Kind:     TraceRequestStart,
		Provider: "openai",
		Model:    "gpt-5.4",
	})
	logger.HandleTraceEvent(TraceEvent{
		Kind:     TraceText,
		Provider: "openai",
		Model:    "gpt-5.4",
		Preview:  "partial output before error",
	})
	// Simulate: no finish event, just an error — caller invokes Flush
	logger.Flush()

	got := buf.String()
	if !strings.Contains(got, "llm text") {
		t.Errorf("expected Flush to emit batched text line, got:\n%s", got)
	}
	if !strings.Contains(got, "partial output before error") {
		t.Errorf("expected batched preview in flushed output, got:\n%s", got)
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
