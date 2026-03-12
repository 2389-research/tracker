// ABOUTME: Tests for the dashboard agent log component — scrolling viewport of events.
package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

func TestAgentLogModelCreation(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	if log.width != 80 {
		t.Errorf("expected width=80, got %d", log.width)
	}
	if log.height != 20 {
		t.Errorf("expected height=20, got %d", log.height)
	}
}

func TestAgentLogModelInitiallyEmpty(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	if log.Len() != 0 {
		t.Errorf("expected 0 entries, got %d", log.Len())
	}
}

func TestAgentLogAppendLine(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	log.AppendLine("hello log line")
	if log.Len() != 1 {
		t.Errorf("expected 1 entry, got %d", log.Len())
	}
}

func TestAgentLogAppendMultipleLines(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	log.AppendLine("line 1")
	log.AppendLine("line 2")
	log.AppendLine("line 3")
	if log.Len() != 3 {
		t.Errorf("expected 3 entries, got %d", log.Len())
	}
}

func TestAgentLogAppendTraceVerbose(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	log.AppendTrace(llm.TraceEvent{
		Kind:     llm.TraceToolPrepare,
		Provider: "anthropic",
		Model:    "claude-opus-4-6",
		ToolName: "read",
		Preview:  `{"path":"go.mod"}`,
	}, true)

	if log.Len() != 1 {
		t.Fatalf("expected 1 trace entry in verbose mode, got %d", log.Len())
	}
	if !strings.Contains(log.entries[0].Message, "read") {
		t.Fatalf("expected tool name in log entry, got %q", log.entries[0].Message)
	}
}

func TestAgentLogAppendAgentEvent(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	log.AppendAgentEvent(agent.Event{
		Type:      agent.EventToolCallStart,
		ToolName:  "read",
		ToolInput: `{"path":"go.mod"}`,
	})

	if log.Len() != 1 {
		t.Fatalf("expected 1 agent event entry, got %d", log.Len())
	}
	if !strings.Contains(log.entries[0].Message, "read") {
		t.Fatalf("expected tool name in message, got %q", log.entries[0].Message)
	}
}

func TestAgentLogAppendEvent(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	evt := pipeline.PipelineEvent{
		Type:      pipeline.EventStageCompleted,
		NodeID:    "mynode",
		Message:   "completed",
		Timestamp: time.Now(),
	}
	log.AppendEvent(evt)
	if log.Len() != 1 {
		t.Errorf("expected 1 entry after AppendEvent, got %d", log.Len())
	}
}

func TestAgentLogAppendEventWithError(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	err := &errImpl{msg: "something went wrong"}
	evt := pipeline.PipelineEvent{
		Type:      pipeline.EventStageFailed,
		NodeID:    "failnode",
		Message:   "stage failed",
		Timestamp: time.Now(),
		Err:       err,
	}
	log.AppendEvent(evt)
	if log.Len() != 1 {
		t.Errorf("expected 1 entry after AppendEvent with error, got %d", log.Len())
	}
	entry := log.entries[0]
	if !entry.IsError {
		t.Error("expected IsError=true for event with non-nil Err")
	}
	if !strings.Contains(entry.Message, "something went wrong") {
		t.Errorf("expected error text in message, got: %q", entry.Message)
	}
}

func TestAgentLogViewContainsTitleWhenReady(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	view := log.View()
	if !strings.Contains(view, "ACTIVITY LOG") {
		t.Errorf("expected 'ACTIVITY LOG' title in view, got: %q", view)
	}
}

func TestAgentLogViewContainsInitializingWhenNotReady(t *testing.T) {
	log := NewAgentLogModel(0, 0)
	view := log.View()
	if !strings.Contains(view, "initializ") {
		t.Errorf("expected initializing message in view, got: %q", view)
	}
}

func TestAgentLogSetSize(t *testing.T) {
	log := NewAgentLogModel(40, 10)
	log.SetSize(100, 30)
	if log.width != 100 {
		t.Errorf("expected width=100, got %d", log.width)
	}
	if log.height != 30 {
		t.Errorf("expected height=30, got %d", log.height)
	}
}

func TestAgentLogSetSizeMakesReady(t *testing.T) {
	log := NewAgentLogModel(0, 0)
	if log.ready {
		t.Error("expected not ready with 0 dimensions")
	}
	log.SetSize(80, 20)
	if !log.ready {
		t.Error("expected ready after SetSize with positive dimensions")
	}
}

func TestAgentLogInitReturnsNilCmd(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	cmd := log.Init()
	if cmd != nil {
		t.Error("expected nil Cmd from Init")
	}
}

func TestAgentLogFormatOmitsTimestamp(t *testing.T) {
	entry := LogEntry{
		Time:      time.Date(2026, 3, 6, 12, 30, 45, 0, time.UTC),
		EventType: "stage_started",
		NodeID:    "mynode",
		Message:   "test message",
	}
	result := formatLogEntry(entry, 0, false)
	if strings.Contains(result, "12:30:45") {
		t.Errorf("expected no timestamp in formatted entry, got: %q", result)
	}
}

func TestAgentLogFormatCompletionEventStyledCorrectly(t *testing.T) {
	entry := LogEntry{
		Time:      time.Now(),
		EventType: "stage_completed",
		Message:   "done",
	}
	result := formatLogEntry(entry, 0, false)
	if !strings.Contains(result, "done") {
		t.Errorf("expected message in formatted entry, got: %q", result)
	}
}

func TestAgentLogFormatIncludesNodeID(t *testing.T) {
	entry := LogEntry{
		Time:   time.Now(),
		NodeID: "node-xyz",
	}
	result := formatLogEntry(entry, 0, false)
	if !strings.Contains(result, "node-xyz") {
		t.Errorf("expected node ID in formatted entry, got: %q", result)
	}
}

func TestAgentLogFormatIncludesMessage(t *testing.T) {
	entry := LogEntry{
		Time:    time.Now(),
		Message: "pipeline is running",
	}
	result := formatLogEntry(entry, 0, false)
	if !strings.Contains(result, "pipeline is running") {
		t.Errorf("expected message in formatted entry, got: %q", result)
	}
}

func TestAgentLogFormatEmptyEventTypeOmitsTypeField(t *testing.T) {
	entry := LogEntry{
		Time:    time.Now(),
		Message: "bare message",
	}
	result := formatLogEntry(entry, 0, false)
	if strings.Contains(result, "[]") {
		t.Errorf("expected no empty brackets when EventType is empty, got: %q", result)
	}
}

func TestAgentLogAppendEventAllTypes(t *testing.T) {
	eventTypes := []pipeline.PipelineEventType{
		pipeline.EventPipelineStarted,
		pipeline.EventPipelineCompleted,
		pipeline.EventPipelineFailed,
		pipeline.EventStageStarted,
		pipeline.EventStageCompleted,
		pipeline.EventStageFailed,
		pipeline.EventInterviewStarted,
		pipeline.EventInterviewCompleted,
	}
	log := NewAgentLogModel(80, 20)
	for _, evtType := range eventTypes {
		log.AppendEvent(pipeline.PipelineEvent{
			Type:      evtType,
			Timestamp: time.Now(),
		})
	}
	if log.Len() != len(eventTypes) {
		t.Errorf("expected %d entries, got %d", len(eventTypes), log.Len())
	}
}

func TestAgentLogCoalescesTextChunks(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceRequestStart, Provider: "anthropic", Model: "opus"}, false)

	// Simulate three streaming text chunks
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceText, Provider: "anthropic", Model: "opus", Preview: "Hello"}, false)
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceText, Provider: "anthropic", Model: "opus", Preview: " world"}, false)
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceText, Provider: "anthropic", Model: "opus", Preview: "!"}, false)

	// Should produce: 1 model header + 1 coalesced text = 2 entries
	textEntries := 0
	for _, e := range log.entries {
		if e.EventType == string(llm.TraceText) {
			textEntries++
		}
	}
	if textEntries != 1 {
		t.Fatalf("expected 1 coalesced text entry, got %d", textEntries)
	}

	// The text entry should contain the accumulated text
	last := log.entries[log.Len()-1]
	if !strings.Contains(last.Message, "Hello world!") {
		t.Fatalf("expected accumulated text in message, got %q", last.Message)
	}
}

func TestAgentLogCoalescesReasoningChunks(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceRequestStart, Provider: "anthropic", Model: "opus"}, false)

	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceReasoning, Provider: "anthropic", Model: "opus", Preview: "Think"}, false)
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceReasoning, Provider: "anthropic", Model: "opus", Preview: "ing..."}, false)

	reasoningEntries := 0
	for _, e := range log.entries {
		if e.EventType == string(llm.TraceReasoning) {
			reasoningEntries++
		}
	}
	if reasoningEntries != 1 {
		t.Fatalf("expected 1 coalesced reasoning entry, got %d", reasoningEntries)
	}

	last := log.entries[log.Len()-1]
	if !strings.Contains(last.Message, "Thinking...") {
		t.Fatalf("expected accumulated reasoning in message, got %q", last.Message)
	}
}

func TestAgentLogFinishResetsCoalescing(t *testing.T) {
	log := NewAgentLogModel(80, 20)

	// First request cycle
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceRequestStart, Provider: "anthropic", Model: "opus"}, false)
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceText, Provider: "anthropic", Model: "opus", Preview: "first"}, false)
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceFinish, Provider: "anthropic", Model: "opus", FinishReason: "end_turn"}, false)

	// Second request cycle — text should NOT merge into previous
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceRequestStart, Provider: "anthropic", Model: "opus"}, false)
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceText, Provider: "anthropic", Model: "opus", Preview: "second"}, false)

	// Find text entries and verify they are distinct
	textEntries := 0
	for _, e := range log.entries {
		if e.EventType == string(llm.TraceText) {
			textEntries++
		}
	}
	if textEntries != 2 {
		t.Fatalf("expected 2 separate text entries across requests, got %d", textEntries)
	}
}

func TestAgentLogToolPrepareDoesNotBreakCoalescing(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceRequestStart, Provider: "anthropic", Model: "opus"}, false)

	// Text, then tool prepare (LLM-internal), then more text should stay coalesced
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceText, Provider: "anthropic", Model: "opus", Preview: "I'll read "}, false)
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceToolPrepare, Provider: "anthropic", Model: "opus", ToolName: "read"}, false)
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceText, Provider: "anthropic", Model: "opus", Preview: "the file"}, false)

	// In non-verbose mode, tool prepare is suppressed and text stays coalesced
	textEntries := 0
	for _, e := range log.entries {
		if e.EventType == string(llm.TraceText) {
			textEntries++
		}
	}
	if textEntries != 1 {
		t.Fatalf("expected 1 coalesced text entry (tool prepare shouldn't break it), got %d; entries: %v", textEntries, log.entries)
	}
	// Last entry is the coalesced text (header is before it)
	last := log.entries[log.Len()-1]
	if !strings.Contains(last.Message, "I'll read the file") {
		t.Fatalf("expected full accumulated text, got %q", last.Message)
	}
}

func TestAgentLogNonVerboseSuppressesDebugEvents(t *testing.T) {
	log := NewAgentLogModel(80, 20)

	// In non-verbose mode, start/finish/tool-prepare should be suppressed
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceRequestStart, Provider: "anthropic", Model: "opus"}, false)
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceToolPrepare, Provider: "anthropic", Model: "opus", ToolName: "read"}, false)
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceFinish, Provider: "anthropic", Model: "opus", FinishReason: "end_turn"}, false)

	if log.Len() != 0 {
		t.Fatalf("expected 0 entries in non-verbose mode for debug events, got %d", log.Len())
	}
}

func TestAgentLogVerboseShowsDebugEvents(t *testing.T) {
	log := NewAgentLogModel(80, 20)

	// In verbose mode, start/finish/tool-prepare should appear
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceRequestStart, Provider: "anthropic", Model: "opus"}, true)
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceToolPrepare, Provider: "anthropic", Model: "opus", ToolName: "read"}, true)
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceFinish, Provider: "anthropic", Model: "opus", FinishReason: "end_turn"}, true)

	if log.Len() != 3 {
		t.Fatalf("expected 3 entries in verbose mode, got %d", log.Len())
	}
}

func TestAgentLogTextShowsModelHeader(t *testing.T) {
	log := NewAgentLogModel(120, 20)
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceText, Provider: "anthropic", Model: "claude-opus-4-6", Preview: "Hello"}, false)

	// Should emit a model header entry followed by the text entry.
	if log.Len() != 2 {
		t.Fatalf("expected 2 entries (header + text), got %d", log.Len())
	}
	header := log.entries[0].Message
	if !strings.Contains(header, "anthropic/claude-opus-4-6") {
		t.Fatalf("expected model header, got %q", header)
	}
	text := log.entries[1].Message
	if !strings.Contains(text, "Hello") {
		t.Fatalf("expected text content, got %q", text)
	}
	// Text entry should NOT repeat the provider/model.
	if strings.Contains(text, "anthropic") {
		t.Fatalf("text entry should not repeat provider, got %q", text)
	}
}

func TestAgentLogModelHeaderOnlyOnChange(t *testing.T) {
	log := NewAgentLogModel(120, 20)
	// First text from anthropic — emits header + text.
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceText, Provider: "anthropic", Model: "opus", Preview: "Hello"}, false)
	// Finish resets coalescing but not the active model.
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceFinish, Provider: "anthropic", Model: "opus"}, false)
	// Second text from same model — no new header.
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceText, Provider: "anthropic", Model: "opus", Preview: "World"}, false)

	headerCount := 0
	for _, e := range log.entries {
		if e.EventType == "model_header" {
			headerCount++
		}
	}
	if headerCount != 1 {
		t.Fatalf("expected 1 model header (same model), got %d", headerCount)
	}

	// Third text from a different model — new header.
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceText, Provider: "openai", Model: "gpt-5", Preview: "Hi"}, false)
	headerCount = 0
	for _, e := range log.entries {
		if e.EventType == "model_header" {
			headerCount++
		}
	}
	if headerCount != 2 {
		t.Fatalf("expected 2 model headers (different model), got %d", headerCount)
	}
}

func TestAgentLogToolCallShowsNameAndInput(t *testing.T) {
	log := NewAgentLogModel(120, 20)
	log.AppendAgentEvent(agent.Event{
		Type:      agent.EventToolCallStart,
		ToolName:  "read",
		ToolInput: `{"path":"main.go"}`,
	})
	if log.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", log.Len())
	}
	msg := log.entries[0].Message
	if !strings.Contains(msg, "read") {
		t.Fatalf("expected tool name 'read' in message, got %q", msg)
	}
}

func TestAgentLogToolCallEndShowsOutput(t *testing.T) {
	log := NewAgentLogModel(120, 20)
	log.AppendAgentEvent(agent.Event{
		Type:       agent.EventToolCallEnd,
		ToolName:   "bash",
		ToolOutput: "go test: PASS",
	})
	if log.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", log.Len())
	}
	msg := log.entries[0].Message
	if !strings.Contains(msg, "go test: PASS") {
		t.Fatalf("expected output content in message, got %q", msg)
	}
}

func TestAgentLogCoalescedTextShowsCleanContent(t *testing.T) {
	log := NewAgentLogModel(120, 20)
	log.AppendTrace(llm.TraceEvent{Kind: llm.TraceText, Provider: "anthropic", Model: "opus", Preview: "Hello world"}, false)

	// Last entry is the text (header is first)
	msg := log.entries[log.Len()-1].Message
	// Should NOT contain preview= or quotes around the text
	if strings.Contains(msg, "preview=") {
		t.Fatalf("coalesced text should not use preview= format, got %q", msg)
	}
	// Should contain the actual text
	if !strings.Contains(msg, "Hello world") {
		t.Fatalf("expected 'Hello world' in coalesced text, got %q", msg)
	}
	// Should NOT contain the provider/model (that's in the header)
	if strings.Contains(msg, "anthropic") {
		t.Fatalf("text line should not repeat provider, got %q", msg)
	}
}

// errImpl is a minimal error implementation for test use.
type errImpl struct{ msg string }

func (e *errImpl) Error() string { return e.msg }
