// ABOUTME: Tests for the JSONL activity log event handler.
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

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }
