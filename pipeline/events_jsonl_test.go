// ABOUTME: Tests for the JSONL activity log event handler.
// ABOUTME: Covers pipeline, agent, and LLM event logging to the unified activity.jsonl file.
package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJSONLEventHandlerWritesEvents(t *testing.T) {
	dir := t.TempDir()
	h := NewJSONLEventHandler(dir)
	defer h.Close()

	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Date(2026, 3, 11, 10, 0, 0, 0, time.UTC),
		RunID:     "abc123",
		Message:   "pipeline started",
	})
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventStageStarted,
		Timestamp: time.Date(2026, 3, 11, 10, 0, 1, 0, time.UTC),
		RunID:     "abc123",
		NodeID:    "step1",
		Message:   "executing node",
	})

	h.Close()

	logPath := filepath.Join(dir, "abc123", "activity.jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read activity log: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %s", len(lines), string(data))
	}

	var entry jsonlLogEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("unmarshal first line: %v", err)
	}
	if entry.Type != "pipeline_started" {
		t.Errorf("expected pipeline_started, got %q", entry.Type)
	}
	if entry.RunID != "abc123" {
		t.Errorf("expected run_id abc123, got %q", entry.RunID)
	}
}

func TestJSONLEventHandlerRecordsErrors(t *testing.T) {
	dir := t.TempDir()
	h := NewJSONLEventHandler(dir)
	defer h.Close()

	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineFailed,
		Timestamp: time.Now(),
		RunID:     "def456",
		Message:   "pipeline failed",
		Err:       &testErr{msg: "context cancelled"},
	})

	h.Close()

	data, err := os.ReadFile(filepath.Join(dir, "def456", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read activity log: %v", err)
	}

	var entry jsonlLogEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry.Error != "context cancelled" {
		t.Errorf("expected error field, got %q", entry.Error)
	}
}

func TestJSONLEventHandlerNoopWithoutRunID(t *testing.T) {
	dir := t.TempDir()
	h := NewJSONLEventHandler(dir)
	defer h.Close()

	// Event without RunID should not panic or create files
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
	})

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected no files without RunID, got %d", len(entries))
	}
}

func TestJSONLEventHandlerCloseWithoutEvents(t *testing.T) {
	dir := t.TempDir()
	h := NewJSONLEventHandler(dir)
	// Close without writing any events should not panic
	if err := h.Close(); err != nil {
		t.Fatalf("Close without events: %v", err)
	}
}

func TestJSONLEventHandlerWritesPipelineSource(t *testing.T) {
	dir := t.TempDir()
	h := NewJSONLEventHandler(dir)
	defer h.Close()

	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "src123",
		Message:   "started",
	})
	h.Close()

	data, err := os.ReadFile(filepath.Join(dir, "src123", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var entry jsonlLogEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry.Source != "pipeline" {
		t.Errorf("source = %q, want pipeline", entry.Source)
	}
}

func TestJSONLEventHandlerWritesAgentEvents(t *testing.T) {
	dir := t.TempDir()
	h := NewJSONLEventHandler(dir)

	// Open the file by sending a pipeline event first (to get run ID).
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "agent123",
	})

	h.WriteAgentEvent("tool_call_end", "gen_code", "execute_command", "output here", "", "", "", "anthropic", "claude-sonnet-4-6")
	h.Close()

	data, err := os.ReadFile(filepath.Join(dir, "agent123", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	var entry jsonlLogEntry
	if err := json.Unmarshal([]byte(lines[1]), &entry); err != nil {
		t.Fatalf("unmarshal agent line: %v", err)
	}
	if entry.Source != "agent" {
		t.Errorf("source = %q, want agent", entry.Source)
	}
	if entry.ToolName != "execute_command" {
		t.Errorf("tool_name = %q, want execute_command", entry.ToolName)
	}
	if entry.Content != "output here" {
		t.Errorf("content = %q, want 'output here'", entry.Content)
	}
	if entry.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", entry.Provider)
	}
	if entry.NodeID != "gen_code" {
		t.Errorf("node_id = %q, want gen_code", entry.NodeID)
	}
}

func TestJSONLEventHandlerWritesLLMEvents(t *testing.T) {
	dir := t.TempDir()
	h := NewJSONLEventHandler(dir)

	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "llm123",
	})

	h.WriteLLMEvent("request_start", "anthropic", "claude-sonnet-4-6", "", "hello world")
	h.Close()

	data, err := os.ReadFile(filepath.Join(dir, "llm123", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	var entry jsonlLogEntry
	if err := json.Unmarshal([]byte(lines[1]), &entry); err != nil {
		t.Fatalf("unmarshal llm line: %v", err)
	}
	if entry.Source != "llm" {
		t.Errorf("source = %q, want llm", entry.Source)
	}
	if entry.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", entry.Provider)
	}
	if entry.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q, want claude-sonnet-4-6", entry.Model)
	}
	if entry.Content != "hello world" {
		t.Errorf("content = %q, want 'hello world'", entry.Content)
	}
}

func TestJSONLEventHandlerAgentErrorCombining(t *testing.T) {
	dir := t.TempDir()
	h := NewJSONLEventHandler(dir)

	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "err123",
	})

	h.WriteAgentEvent("tool_call_end", "", "cmd", "", "exit code 1", "", "process killed", "", "")
	h.Close()

	data, err := os.ReadFile(filepath.Join(dir, "err123", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var entry jsonlLogEntry
	if err := json.Unmarshal([]byte(lines[1]), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry.Error != "exit code 1: process killed" {
		t.Errorf("error = %q, want 'exit code 1: process killed'", entry.Error)
	}
}

func TestBuildLogEntry_CostSnapshot(t *testing.T) {
	evt := PipelineEvent{
		Type:      EventCostUpdated,
		Timestamp: time.Unix(100, 0),
		RunID:     "run-1",
		Cost: &CostSnapshot{
			TotalTokens:  1500,
			TotalCostUSD: 0.0375,
			ProviderTotals: map[string]ProviderUsage{
				"anthropic": {InputTokens: 1000, OutputTokens: 500, CostUSD: 0.0375, SessionCount: 2},
			},
			WallElapsed: 500 * time.Millisecond,
		},
	}
	entry := buildLogEntry(evt)
	if entry.TotalTokens != 1500 {
		t.Errorf("TotalTokens = %d, want 1500", entry.TotalTokens)
	}
	if entry.TotalCostUSD < 0.03749 || entry.TotalCostUSD > 0.03751 {
		t.Errorf("TotalCostUSD = %f, want 0.0375", entry.TotalCostUSD)
	}
	if entry.WallElapsedMs != 500 {
		t.Errorf("WallElapsedMs = %d, want 500", entry.WallElapsedMs)
	}
	if entry.ProviderTotals == nil || entry.ProviderTotals["anthropic"].InputTokens != 1000 {
		t.Errorf("ProviderTotals[anthropic] = %+v", entry.ProviderTotals["anthropic"])
	}
	if entry.Estimated {
		t.Error("Estimated = true for a cost snapshot with Estimated:false; want false")
	}
}

// TestBuildLogEntry_CostSnapshot_Estimated pins the #186 NDJSON surface:
// when CostSnapshot.Estimated is true, the activity.jsonl entry carries
// `estimated: true` so external consumers (dashboards, tracker diagnose,
// embedded integrations) can distinguish heuristic spend from metered
// spend without re-deriving the flag from ProviderTotals.
func TestBuildLogEntry_CostSnapshot_Estimated(t *testing.T) {
	evt := PipelineEvent{
		Type:      EventCostUpdated,
		Timestamp: time.Unix(100, 0),
		RunID:     "run-1",
		Cost: &CostSnapshot{
			TotalTokens:  300,
			TotalCostUSD: 0.0125,
			ProviderTotals: map[string]ProviderUsage{
				"acp": {InputTokens: 200, OutputTokens: 100, CostUSD: 0.0125, SessionCount: 1, Estimated: true},
			},
			WallElapsed: 250 * time.Millisecond,
			Estimated:   true,
		},
	}
	entry := buildLogEntry(evt)
	if !entry.Estimated {
		t.Error("Estimated = false; want true (CostSnapshot.Estimated=true)")
	}
	if !entry.ProviderTotals["acp"].Estimated {
		t.Error("ProviderTotals[acp].Estimated = false; want true (per-bucket flag lost)")
	}
}

func TestBuildLogEntry_NilCost(t *testing.T) {
	evt := PipelineEvent{Type: EventPipelineStarted, Timestamp: time.Unix(100, 0), RunID: "run-1"}
	entry := buildLogEntry(evt)
	if entry.TotalTokens != 0 || entry.TotalCostUSD != 0 {
		t.Errorf("nil cost should yield zero fields, got %+v", entry)
	}
}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }
