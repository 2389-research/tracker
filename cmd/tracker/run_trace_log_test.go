// ABOUTME: Tests for the client-level LLM trace → activity log observer (#354).
// ABOUTME: Session-owned events are skipped (agent llm_* path already logs them).
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

func TestLLMTraceLogObserver_SkipsSessionOwnedEvents(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TRACKER_AUDIT_DIR", filepath.Join(dir, "secure"))
	activityLog := pipeline.NewJSONLEventHandler(dir)
	activityLog.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "trace1",
	})

	observer := llmTraceLogObserver(activityLog)

	// Session-owned: the agent session re-emits this as an llm_* agent
	// event, so the trace path must not write a duplicate line.
	observer(llm.TraceEvent{Kind: llm.TraceText, Provider: "anthropic", Model: "m", Preview: "dup", SessionOwned: true})
	// Non-session (e.g. autopilot interviewer): the trace path is the
	// only log surface — must be written.
	observer(llm.TraceEvent{Kind: llm.TraceText, Provider: "anthropic", Model: "m", Preview: "kept"})
	if err := activityLog.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "trace1", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (pipeline + 1 llm), got %d:\n%s", len(lines), data)
	}
	var entry struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry.Type != "text" || entry.Content != "kept" {
		t.Errorf("logged entry = %+v, want type=text content=kept", entry)
	}
}
