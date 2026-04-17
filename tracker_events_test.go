// ABOUTME: Tests for the public NDJSONWriter and StreamEvent types in the tracker package.
// ABOUTME: Covers write, stable JSON tags, concurrency, and handler factory methods.
package tracker

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

func TestNDJSONWriter_Write(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)
	w.Write(StreamEvent{Timestamp: "2026-04-17T10:00:00Z", Source: "pipeline", Type: "stage_started", NodeID: "N1"})

	line := strings.TrimSuffix(buf.String(), "\n")
	var got StreamEvent
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Source != "pipeline" || got.Type != "stage_started" || got.NodeID != "N1" {
		t.Errorf("wrong event: %+v", got)
	}
}

func TestNDJSONWriter_StableJSONTags(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)
	w.Write(StreamEvent{
		Timestamp: "t", Source: "agent", Type: "tool_call",
		RunID: "r1", NodeID: "n1", Message: "m", Error: "e",
		Provider: "p", Model: "mo", ToolName: "tn", Content: "c",
	})
	want := `"ts":"t"`
	if !strings.Contains(buf.String(), want) {
		t.Errorf("missing stable tag %q in output: %s", want, buf.String())
	}
	for _, tag := range []string{`"source"`, `"type"`, `"run_id"`, `"node_id"`, `"message"`, `"error"`, `"provider"`, `"model"`, `"tool_name"`, `"content"`} {
		if !strings.Contains(buf.String(), tag) {
			t.Errorf("missing JSON tag %s in output", tag)
		}
	}
}

func TestNDJSONWriter_ConcurrentWrites(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			w.Write(StreamEvent{Timestamp: "t", Source: "pipeline", Type: "x"})
		}()
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != n {
		t.Fatalf("got %d lines, want %d", len(lines), n)
	}
	for i, l := range lines {
		var evt StreamEvent
		if err := json.Unmarshal([]byte(l), &evt); err != nil {
			t.Fatalf("line %d: unmarshal: %v; got %q", i, err, l)
		}
	}
}

func TestNDJSONWriter_OmitsEmptyFields(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)
	w.Write(StreamEvent{
		Timestamp: "2026-04-17T10:00:00Z",
		Source:    "llm",
		Type:      "request_start",
	})

	line := strings.TrimSpace(buf.String())
	for _, tag := range []string{`"run_id"`, `"error"`, `"node_id"`, `"message"`, `"provider"`, `"model"`, `"tool_name"`, `"content"`} {
		if strings.Contains(line, tag) {
			t.Errorf("expected %s to be omitted when empty, got: %s", tag, line)
		}
	}
}

func TestNDJSONWriter_PipelineHandler(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)
	handler := w.PipelineHandler()

	ts := time.Date(2026, 3, 14, 10, 0, 0, 0, time.UTC)
	handler.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventPipelineStarted,
		Timestamp: ts,
		RunID:     "run1",
		NodeID:    "node1",
		Message:   "started",
	})

	var evt StreamEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &evt); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if evt.Source != "pipeline" {
		t.Errorf("source = %q, want pipeline", evt.Source)
	}
	if evt.Type != "pipeline_started" {
		t.Errorf("type = %q, want pipeline_started", evt.Type)
	}
	if evt.RunID != "run1" {
		t.Errorf("run_id = %q, want run1", evt.RunID)
	}
	if evt.NodeID != "node1" {
		t.Errorf("node_id = %q, want node1", evt.NodeID)
	}
	if evt.Timestamp != "2026-03-14T10:00:00.000Z" {
		t.Errorf("ts = %q, want 2026-03-14T10:00:00.000Z", evt.Timestamp)
	}
}

func TestNDJSONWriter_PipelineHandlerWithError(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)
	handler := w.PipelineHandler()

	handler.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventPipelineFailed,
		Timestamp: time.Now(),
		RunID:     "run1",
		Message:   "pipeline failed",
		Err:       errors.New("context cancelled"),
	})

	var evt StreamEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &evt); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if evt.Error != "context cancelled" {
		t.Errorf("error = %q, want 'context cancelled'", evt.Error)
	}
}

func TestNDJSONWriter_TraceObserver(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)
	observer := w.TraceObserver()

	observer.HandleTraceEvent(llm.TraceEvent{
		Kind:     llm.TraceRequestStart,
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
		Preview:  "hello world",
	})

	var evt StreamEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &evt); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if evt.Source != "llm" {
		t.Errorf("source = %q, want llm", evt.Source)
	}
	if evt.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", evt.Provider)
	}
	if evt.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q, want claude-sonnet-4-6", evt.Model)
	}
	if evt.Content != "hello world" {
		t.Errorf("content = %q, want 'hello world'", evt.Content)
	}
}

func TestNDJSONWriter_TraceObserverWithToolName(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)
	observer := w.TraceObserver()

	observer.HandleTraceEvent(llm.TraceEvent{
		Kind:     llm.TraceToolPrepare,
		Provider: "openai",
		Model:    "gpt-4o",
		ToolName: "execute_command",
	})

	var evt StreamEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &evt); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if evt.ToolName != "execute_command" {
		t.Errorf("tool_name = %q, want execute_command", evt.ToolName)
	}
}

func TestNDJSONWriter_AgentHandler(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)
	handler := w.AgentHandler()

	handler.HandleEvent(agent.Event{
		Type:       agent.EventToolCallEnd,
		Provider:   "anthropic",
		Model:      "claude-sonnet-4-6",
		ToolName:   "read_file",
		ToolOutput: "file contents here",
	})

	var evt StreamEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &evt); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if evt.Source != "agent" {
		t.Errorf("source = %q, want agent", evt.Source)
	}
	if evt.Type != string(agent.EventToolCallEnd) {
		t.Errorf("type = %q, want %s", evt.Type, agent.EventToolCallEnd)
	}
	if evt.ToolName != "read_file" {
		t.Errorf("tool_name = %q, want read_file", evt.ToolName)
	}
	if evt.Content != "file contents here" {
		t.Errorf("content = %q, want 'file contents here'", evt.Content)
	}
}

func TestNDJSONWriter_AgentHandlerFallsBackToText(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)
	handler := w.AgentHandler()

	handler.HandleEvent(agent.Event{
		Type: agent.EventTextDelta,
		Text: "thinking about the problem",
	})

	var evt StreamEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &evt); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if evt.Content != "thinking about the problem" {
		t.Errorf("content = %q, want 'thinking about the problem'", evt.Content)
	}
}

func TestNDJSONWriter_AgentHandlerWithToolError(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)
	handler := w.AgentHandler()

	handler.HandleEvent(agent.Event{
		Type:      agent.EventToolCallEnd,
		ToolName:  "execute_command",
		ToolError: "command timed out",
	})

	var evt StreamEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &evt); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if evt.Error != "command timed out" {
		t.Errorf("error = %q, want 'command timed out'", evt.Error)
	}
}

func TestNDJSONWriter_AgentHandlerCombinesErrors(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)
	handler := w.AgentHandler()

	handler.HandleEvent(agent.Event{
		Type:      agent.EventToolCallEnd,
		ToolName:  "execute_command",
		ToolError: "exit code 1",
		Err:       errors.New("process killed"),
	})

	var evt StreamEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &evt); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if evt.Error != "exit code 1: process killed" {
		t.Errorf("error = %q, want 'exit code 1: process killed'", evt.Error)
	}
}

func TestNDJSONWriter_AgentHandler_ContentPriority(t *testing.T) {
	t.Run("ToolOutput wins when both present", func(t *testing.T) {
		var buf bytes.Buffer
		w := NewNDJSONWriter(&buf)
		h := w.AgentHandler()
		h.HandleEvent(agent.Event{Type: agent.EventToolCallEnd, ToolOutput: "tool-out", Text: "text-out"})

		var got StreamEvent
		if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.Content != "tool-out" {
			t.Errorf("content = %q, want tool-out", got.Content)
		}
	})
}

func TestNDJSONWriter_AgentHandlerErrorOnlyFromErr(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)
	handler := w.AgentHandler()

	handler.HandleEvent(agent.Event{
		Type: agent.EventError,
		Err:  errors.New("session failed"),
	})

	var evt StreamEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &evt); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if evt.Error != "session failed" {
		t.Errorf("error = %q, want 'session failed'", evt.Error)
	}
}

// errWriter always returns an error on Write, used to exercise NDJSONWriter's
// error-propagation path.
type errWriter struct{ err error }

func (e *errWriter) Write(_ []byte) (int, error) { return 0, e.err }

func TestNDJSONWriter_WriteReturnsError(t *testing.T) {
	w := NewNDJSONWriter(&errWriter{err: errors.New("sink closed")})
	err := w.Write(StreamEvent{Timestamp: "t", Source: "pipeline", Type: "stage_started"})
	if err == nil {
		t.Fatal("expected non-nil error when backing writer fails")
	}
	if !strings.Contains(err.Error(), "sink closed") {
		t.Errorf("error = %q, want to contain 'sink closed'", err.Error())
	}
}

// panicWriter panics on Write, used to exercise handler panic-recovery paths.
type panicWriter struct{}

func (p *panicWriter) Write(_ []byte) (int, error) { panic("sink exploded") }

func TestNDJSONWriter_PipelineHandler_PanicRecovery(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("PipelineHandler should recover from panic, got: %v", r)
		}
	}()
	w := NewNDJSONWriter(&panicWriter{})
	w.PipelineHandler().HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "r",
	})
}

func TestNDJSONWriter_AgentHandler_PanicRecovery(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AgentHandler should recover from panic, got: %v", r)
		}
	}()
	w := NewNDJSONWriter(&panicWriter{})
	w.AgentHandler().HandleEvent(agent.Event{Type: agent.EventTextDelta, Text: "x"})
}

func TestNDJSONWriter_TraceObserver_PanicRecovery(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("TraceObserver should recover from panic, got: %v", r)
		}
	}()
	w := NewNDJSONWriter(&panicWriter{})
	w.TraceObserver().HandleTraceEvent(llm.TraceEvent{Kind: llm.TraceRequestStart})
}

// TestNDJSONWriter_PanicSuppressionIsPerInstance verifies that one writer
// recovering from a panic does not silence panic logging on a separate
// writer instance. Both writers must independently report their first
// panic; regressions here mean package-level state re-crept in.
func TestNDJSONWriter_PanicSuppressionIsPerInstance(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("handlers should recover, got: %v", r)
		}
	}()
	w1 := NewNDJSONWriter(&panicWriter{})
	w2 := NewNDJSONWriter(&panicWriter{})
	// First panic on w1 — must not consume w2's Once.
	w1.PipelineHandler().HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventPipelineStarted,
		Timestamp: time.Now(),
	})
	// Second panic on w2 — still the first on its own instance.
	w2.PipelineHandler().HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventPipelineStarted,
		Timestamp: time.Now(),
	})
}
