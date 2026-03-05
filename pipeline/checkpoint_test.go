// ABOUTME: Tests for checkpoint serialization, deserialization, and state tracking.
// ABOUTME: Validates save/load round-trip, directory creation, and retry/completion tracking.
package pipeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckpointSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cp.json")

	original := &Checkpoint{
		RunID:          "abc123",
		CurrentNode:    "step2",
		CompletedNodes: []string{"start", "step1"},
		RetryCounts:    map[string]int{"step1": 2},
		Context:        map[string]string{"key": "value"},
		Timestamp:      time.Now().Truncate(time.Second),
	}

	if err := SaveCheckpoint(original, path); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}

	if loaded.RunID != original.RunID {
		t.Errorf("RunID: got %q, want %q", loaded.RunID, original.RunID)
	}
	if loaded.CurrentNode != original.CurrentNode {
		t.Errorf("CurrentNode: got %q, want %q", loaded.CurrentNode, original.CurrentNode)
	}
	if len(loaded.CompletedNodes) != len(original.CompletedNodes) {
		t.Errorf("CompletedNodes length: got %d, want %d", len(loaded.CompletedNodes), len(original.CompletedNodes))
	}
	if loaded.RetryCounts["step1"] != 2 {
		t.Errorf("RetryCounts[step1]: got %d, want 2", loaded.RetryCounts["step1"])
	}
	if loaded.Context["key"] != "value" {
		t.Errorf("Context[key]: got %q, want %q", loaded.Context["key"], "value")
	}
}

func TestCheckpointLoadMissing(t *testing.T) {
	_, err := LoadCheckpoint("/nonexistent/path/checkpoint.json")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestCheckpointSaveCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c", "cp.json")

	cp := &Checkpoint{
		RunID:          "run1",
		CurrentNode:    "start",
		CompletedNodes: []string{},
		RetryCounts:    map[string]int{},
		Context:        map[string]string{},
		Timestamp:      time.Now(),
	}

	if err := SaveCheckpoint(cp, nested); err != nil {
		t.Fatalf("SaveCheckpoint failed to create directories: %v", err)
	}

	if _, err := os.Stat(nested); os.IsNotExist(err) {
		t.Fatal("checkpoint file was not created")
	}
}

func TestCheckpointIsCompleted(t *testing.T) {
	cp := &Checkpoint{
		CompletedNodes: []string{"start", "step1"},
	}

	if !cp.IsCompleted("start") {
		t.Error("expected start to be completed")
	}
	if !cp.IsCompleted("step1") {
		t.Error("expected step1 to be completed")
	}
	if cp.IsCompleted("step2") {
		t.Error("expected step2 to not be completed")
	}
}

func TestCheckpointRetryCount(t *testing.T) {
	cp := &Checkpoint{
		RetryCounts: map[string]int{"flaky": 3},
	}

	if cp.RetryCount("flaky") != 3 {
		t.Errorf("expected 3, got %d", cp.RetryCount("flaky"))
	}
	if cp.RetryCount("unknown") != 0 {
		t.Errorf("expected 0 for unknown node, got %d", cp.RetryCount("unknown"))
	}
}

func TestCheckpointMarkCompletedDeduplication(t *testing.T) {
	cp := &Checkpoint{
		CompletedNodes: []string{},
		RetryCounts:    map[string]int{},
	}

	cp.MarkCompleted("start")
	cp.MarkCompleted("start") // duplicate — should be ignored
	cp.MarkCompleted("step1")

	if len(cp.CompletedNodes) != 2 {
		t.Errorf("expected 2 completed nodes (no dupes), got %d: %v", len(cp.CompletedNodes), cp.CompletedNodes)
	}
}

func TestCheckpointNilRetryCounts(t *testing.T) {
	// Simulates a checkpoint loaded from JSON where retry_counts was absent.
	cp := &Checkpoint{}

	if cp.RetryCount("anything") != 0 {
		t.Errorf("expected 0 for nil RetryCounts, got %d", cp.RetryCount("anything"))
	}

	// Should not panic on nil map.
	cp.IncrementRetry("node1")
	if cp.RetryCount("node1") != 1 {
		t.Errorf("expected 1 after increment on nil map, got %d", cp.RetryCount("node1"))
	}
}

func TestCheckpointIsCompletedAfterDeserialization(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cp.json")

	original := &Checkpoint{
		RunID:          "test",
		CompletedNodes: []string{"s", "step1"},
		RetryCounts:    map[string]int{},
		Context:        map[string]string{},
	}
	if err := SaveCheckpoint(original, path); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}

	// completedSet is json:"-" so it's nil after load.
	// IsCompleted should lazily rebuild it.
	if !loaded.IsCompleted("s") {
		t.Error("expected 's' to be completed after deserialization")
	}
	if !loaded.IsCompleted("step1") {
		t.Error("expected 'step1' to be completed after deserialization")
	}
	if loaded.IsCompleted("step2") {
		t.Error("expected 'step2' to NOT be completed")
	}
}

func TestCheckpointIncrementRetry(t *testing.T) {
	cp := &Checkpoint{
		RetryCounts: map[string]int{},
	}

	cp.IncrementRetry("node1")
	if cp.RetryCount("node1") != 1 {
		t.Errorf("expected 1 after first increment, got %d", cp.RetryCount("node1"))
	}

	cp.IncrementRetry("node1")
	if cp.RetryCount("node1") != 2 {
		t.Errorf("expected 2 after second increment, got %d", cp.RetryCount("node1"))
	}
}
