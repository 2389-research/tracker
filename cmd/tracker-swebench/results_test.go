// ABOUTME: Tests for the results writer, run stats, and run metadata functionality.
// ABOUTME: Covers write/resume, summary formatting, and run meta file writing.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResultsWriter_WriteAndResume(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "predictions.jsonl")
	model := "test-model"

	// First session: write two predictions.
	w, err := NewResultsWriter(path, model)
	if err != nil {
		t.Fatalf("NewResultsWriter: %v", err)
	}

	if err := w.WritePrediction("instance-1", "diff1"); err != nil {
		t.Fatalf("WritePrediction instance-1: %v", err)
	}
	if err := w.WritePrediction("instance-2", "diff2"); err != nil {
		t.Fatalf("WritePrediction instance-2: %v", err)
	}

	if !w.IsCompleted("instance-1") {
		t.Error("expected instance-1 to be completed")
	}
	if !w.IsCompleted("instance-2") {
		t.Error("expected instance-2 to be completed")
	}
	if w.IsCompleted("instance-3") {
		t.Error("expected instance-3 to NOT be completed")
	}
	if got := w.CompletedCount(); got != 2 {
		t.Errorf("CompletedCount = %d, want 2", got)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify file contents are valid JSONL.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	for i, line := range lines {
		var p Prediction
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("line %d invalid JSON: %v", i+1, err)
		}
		if p.ModelNameOrPath != model {
			t.Errorf("line %d: ModelNameOrPath = %q, want %q", i+1, p.ModelNameOrPath, model)
		}
	}

	// Second session: re-open; should see both already completed.
	w2, err := NewResultsWriter(path, model)
	if err != nil {
		t.Fatalf("NewResultsWriter (resume): %v", err)
	}
	defer w2.Close()

	if !w2.IsCompleted("instance-1") {
		t.Error("resume: expected instance-1 to be completed")
	}
	if !w2.IsCompleted("instance-2") {
		t.Error("resume: expected instance-2 to be completed")
	}
	if got := w2.CompletedCount(); got != 2 {
		t.Errorf("resume: CompletedCount = %d, want 2", got)
	}

	// Write a third prediction in the resumed session.
	if err := w2.WritePrediction("instance-3", "diff3"); err != nil {
		t.Fatalf("WritePrediction instance-3: %v", err)
	}
	if got := w2.CompletedCount(); got != 3 {
		t.Errorf("after instance-3: CompletedCount = %d, want 3", got)
	}

	// File should now have 3 lines (append mode).
	data2, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after resume: %v", err)
	}
	lines2 := strings.Split(strings.TrimSpace(string(data2)), "\n")
	if len(lines2) != 3 {
		t.Fatalf("expected 3 lines after resume, got %d", len(lines2))
	}
}

func TestRunStats_Summary(t *testing.T) {
	stats := RunStats{
		Total:        10,
		Completed:    7,
		Skipped:      2,
		TimedOut:     1,
		Patched:      5,
		InputTokens:  1_500_000,
		OutputTokens: 300_000,
		StartTime:    time.Now().Add(-5 * time.Minute),
	}

	summary := stats.Summary()
	if summary == "" {
		t.Fatal("Summary returned empty string")
	}
	// Check for key pieces of information.
	if !strings.Contains(summary, "10") {
		t.Error("summary should contain total count")
	}
	if !strings.Contains(summary, "7") {
		t.Error("summary should contain completed count")
	}
}

func TestWriteRunMeta(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run_meta.json")

	meta := RunMeta{
		Model:      "claude-sonnet-4-6",
		Provider:   "anthropic",
		GatewayURL: "https://gateway.example.com",
		Dataset:    "swebench-verified",
		MaxTurns:   30,
		Timeout:    "5m",
		Commit:     "abc1234",
	}

	if err := WriteRunMeta(path, meta); err != nil {
		t.Fatalf("WriteRunMeta: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("run_meta.json is empty")
	}

	// Verify it's valid JSON.
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("invalid JSON in run_meta.json: %v", err)
	}

	// StartedAt should have been auto-filled.
	if _, ok := out["started_at"]; !ok {
		t.Error("expected started_at to be set")
	}

	// GatewayURL omitempty: should be present since it's set.
	if _, ok := out["gateway_url"]; !ok {
		t.Error("expected gateway_url to be present when non-empty")
	}
}

func TestWriteRunMeta_OmitsEmptyGateway(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run_meta.json")

	meta := RunMeta{
		Model:    "claude-sonnet-4-6",
		Provider: "anthropic",
		Dataset:  "swebench-verified",
		MaxTurns: 10,
		Timeout:  "2m",
	}

	if err := WriteRunMeta(path, meta); err != nil {
		t.Fatalf("WriteRunMeta: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// GatewayURL omitempty: should be absent when empty.
	if _, ok := out["gateway_url"]; ok {
		t.Error("expected gateway_url to be omitted when empty")
	}
}
