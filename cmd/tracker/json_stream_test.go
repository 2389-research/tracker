// ABOUTME: Tests for the unified NDJSON stream writer used in --json mode.
// ABOUTME: Verifies pipeline, LLM trace, and agent events serialize correctly as typed JSON lines.
package main

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

func TestJSONStreamWriteProducesValidNDJSON(t *testing.T) {
	var buf bytes.Buffer
	s := newJSONStream(&buf)

	s.write(jsonStreamEvent{
		Timestamp: "2026-03-14T10:00:00.000Z",
		Source:    "pipeline",
		Type:      "pipeline_started",
		RunID:     "abc123",
		Message:   "pipeline started",
	})

	line := strings.TrimSpace(buf.String())
	var parsed jsonStreamEvent
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, line)
	}
	if parsed.Source != "pipeline" {
		t.Errorf("source = %q, want pipeline", parsed.Source)
	}
	if parsed.RunID != "abc123" {
		t.Errorf("run_id = %q, want abc123", parsed.RunID)
	}
	if parsed.Message != "pipeline started" {
		t.Errorf("message = %q, want 'pipeline started'", parsed.Message)
	}
}

func TestJSONStreamOmitsEmptyFields(t *testing.T) {
	var buf bytes.Buffer
	s := newJSONStream(&buf)

	s.write(jsonStreamEvent{
		Timestamp: "2026-03-14T10:00:00.000Z",
		Source:    "llm",
		Type:      "request_start",
	})

	line := strings.TrimSpace(buf.String())
	// Fields with omitempty should not appear
	if strings.Contains(line, `"run_id"`) {
		t.Error("expected run_id to be omitted when empty")
	}
	if strings.Contains(line, `"error"`) {
		t.Error("expected error to be omitted when empty")
	}
	if strings.Contains(line, `"node_id"`) {
		t.Error("expected node_id to be omitted when empty")
	}
}

func TestJSONStreamMultipleEventsProduceMultipleLines(t *testing.T) {
	var buf bytes.Buffer
	s := newJSONStream(&buf)

	for i := 0; i < 3; i++ {
		s.write(jsonStreamEvent{
			Timestamp: "2026-03-14T10:00:00.000Z",
			Source:    "pipeline",
			Type:      "test",
		})
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	for i, line := range lines {
		var evt jsonStreamEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			t.Errorf("line %d: invalid JSON: %v", i, err)
		}
	}
}

func TestJSONStreamConcurrentWritesAreSafe(t *testing.T) {
	var buf bytes.Buffer
	s := newJSONStream(&buf)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.write(jsonStreamEvent{
				Timestamp: "2026-03-14T10:00:00.000Z",
				Source:    "agent",
				Type:      "concurrent",
			})
		}()
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 50 {
		t.Fatalf("expected 50 lines, got %d", len(lines))
	}
	for i, line := range lines {
		var evt jsonStreamEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			t.Errorf("line %d: invalid JSON: %v", i, err)
		}
	}
}

func TestJSONStreamPipelineHandler(t *testing.T) {
	var buf bytes.Buffer
	s := newJSONStream(&buf)
	handler := s.pipelineHandler()

	ts := time.Date(2026, 3, 14, 10, 0, 0, 0, time.UTC)

	handler.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventPipelineStarted,
		Timestamp: ts,
		RunID:     "run1",
		NodeID:    "node1",
		Message:   "started",
	})

	var evt jsonStreamEvent
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

func TestJSONStreamPipelineHandlerWithError(t *testing.T) {
	var buf bytes.Buffer
	s := newJSONStream(&buf)
	handler := s.pipelineHandler()

	handler.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventPipelineFailed,
		Timestamp: time.Now(),
		RunID:     "run1",
		Message:   "pipeline failed",
		Err:       errors.New("context cancelled"),
	})

	var evt jsonStreamEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &evt); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if evt.Error != "context cancelled" {
		t.Errorf("error = %q, want 'context cancelled'", evt.Error)
	}
}

func TestJSONStreamTraceObserver(t *testing.T) {
	var buf bytes.Buffer
	s := newJSONStream(&buf)
	observer := s.traceObserver()

	observer.HandleTraceEvent(llm.TraceEvent{
		Kind:     llm.TraceRequestStart,
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
		Preview:  "hello world",
	})

	var evt jsonStreamEvent
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

func TestJSONStreamTraceObserverWithToolName(t *testing.T) {
	var buf bytes.Buffer
	s := newJSONStream(&buf)
	observer := s.traceObserver()

	observer.HandleTraceEvent(llm.TraceEvent{
		Kind:     llm.TraceToolPrepare,
		Provider: "openai",
		Model:    "gpt-4o",
		ToolName: "execute_command",
	})

	var evt jsonStreamEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &evt); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if evt.ToolName != "execute_command" {
		t.Errorf("tool_name = %q, want execute_command", evt.ToolName)
	}
}

func TestJSONStreamAgentHandler(t *testing.T) {
	var buf bytes.Buffer
	s := newJSONStream(&buf)
	handler := s.agentHandler()

	handler.HandleEvent(agent.Event{
		Type:       agent.EventToolCallEnd,
		Provider:   "anthropic",
		Model:      "claude-sonnet-4-6",
		ToolName:   "read_file",
		ToolOutput: "file contents here",
	})

	var evt jsonStreamEvent
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

func TestJSONStreamAgentHandlerFallsBackToText(t *testing.T) {
	var buf bytes.Buffer
	s := newJSONStream(&buf)
	handler := s.agentHandler()

	handler.HandleEvent(agent.Event{
		Type: agent.EventTextDelta,
		Text: "thinking about the problem",
	})

	var evt jsonStreamEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &evt); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if evt.Content != "thinking about the problem" {
		t.Errorf("content = %q, want 'thinking about the problem'", evt.Content)
	}
}

func TestJSONStreamAgentHandlerWithToolError(t *testing.T) {
	var buf bytes.Buffer
	s := newJSONStream(&buf)
	handler := s.agentHandler()

	handler.HandleEvent(agent.Event{
		Type:      agent.EventToolCallEnd,
		ToolName:  "execute_command",
		ToolError: "command timed out",
	})

	var evt jsonStreamEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &evt); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if evt.Error != "command timed out" {
		t.Errorf("error = %q, want 'command timed out'", evt.Error)
	}
}

func TestJSONStreamAgentHandlerCombinesErrors(t *testing.T) {
	var buf bytes.Buffer
	s := newJSONStream(&buf)
	handler := s.agentHandler()

	handler.HandleEvent(agent.Event{
		Type:      agent.EventToolCallEnd,
		ToolName:  "execute_command",
		ToolError: "exit code 1",
		Err:       errors.New("process killed"),
	})

	var evt jsonStreamEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &evt); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if evt.Error != "exit code 1: process killed" {
		t.Errorf("error = %q, want 'exit code 1: process killed'", evt.Error)
	}
}

func TestJSONStreamAgentHandlerErrorOnlyFromErr(t *testing.T) {
	var buf bytes.Buffer
	s := newJSONStream(&buf)
	handler := s.agentHandler()

	handler.HandleEvent(agent.Event{
		Type: agent.EventError,
		Err:  errors.New("session failed"),
	})

	var evt jsonStreamEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &evt); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if evt.Error != "session failed" {
		t.Errorf("error = %q, want 'session failed'", evt.Error)
	}
}
