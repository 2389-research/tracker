// ABOUTME: Tests for checkpoint serialization, deserialization, and state tracking.
// ABOUTME: Validates save/load round-trip, directory creation, and retry/completion tracking.
package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestCheckpoint_BundleIdentity_Roundtrip(t *testing.T) {
	cp := &Checkpoint{
		RunID:          "test-run",
		BundleIdentity: "sha256:efb5648d28e6c250dfad5411651d427f4f62ca24e185ce6cfc51478a4c6711ab",
		Timestamp:      time.Now(),
	}
	path := filepath.Join(t.TempDir(), "cp.json")
	if err := SaveCheckpoint(cp, path); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}
	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if loaded.BundleIdentity != cp.BundleIdentity {
		t.Errorf("BundleIdentity not preserved: got %q want %q", loaded.BundleIdentity, cp.BundleIdentity)
	}
}

func TestCheckpoint_BundleIdentity_BackwardCompat(t *testing.T) {
	// Old-format JSON without bundle_identity should load with empty string.
	path := filepath.Join(t.TempDir(), "old.json")
	old := `{"run_id":"old-run","current_node":"a","completed_nodes":["start"],"retry_counts":{},"context":{},"timestamp":"2026-05-01T00:00:00Z","restart_count":0}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if loaded.BundleIdentity != "" {
		t.Errorf("expected empty BundleIdentity on old checkpoint, got %q", loaded.BundleIdentity)
	}
}

// TestCheckpoint_WIPRefsRoundTrip verifies that recorded WIP refs (#302)
// survive a checkpoint save/load round-trip and that older checkpoints without
// the field still load (backward-compatible, additive).
func TestCheckpoint_WIPRefsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	cp := &Checkpoint{RunID: "r1", CurrentNode: "Implement"}
	cp.RecordWIPRef("Implement", "tracker/wip/r1/Implement")
	if err := SaveCheckpoint(cp, path); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}

	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if got := loaded.WIPRefs["Implement"]; got != "tracker/wip/r1/Implement" {
		t.Errorf("WIPRefs[Implement]: got %q want %q", got, "tracker/wip/r1/Implement")
	}

	// Backward compat: a checkpoint JSON without wip_refs loads with a nil map.
	legacy := filepath.Join(dir, "legacy.json")
	if err := os.WriteFile(legacy, []byte(`{"run_id":"old","current_node":"a","completed_nodes":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old, err := LoadCheckpoint(legacy)
	if err != nil {
		t.Fatalf("LoadCheckpoint(legacy): %v", err)
	}
	if len(old.WIPRefs) != 0 {
		t.Errorf("expected empty WIPRefs on legacy checkpoint, got %v", old.WIPRefs)
	}
}

func TestCheckpoint_GateRecheckPending_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	cp := &Checkpoint{RunID: "run-348"}
	cp.SetGateRecheckPending("FinalSpecCheck")
	if !cp.IsGateRecheckPending("FinalSpecCheck") {
		t.Fatal("expected FinalSpecCheck to be recheck-pending after Set")
	}
	if err := SaveCheckpoint(cp, path); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}

	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if !loaded.IsGateRecheckPending("FinalSpecCheck") {
		t.Fatal("gate_recheck_pending must survive a save/load cycle so resume replays the re-entry")
	}

	loaded.ClearGateRecheckPending("FinalSpecCheck")
	if loaded.IsGateRecheckPending("FinalSpecCheck") {
		t.Fatal("expected pending recheck to clear")
	}
	// Clearing on a nil map must not panic (pre-#348 checkpoints).
	(&Checkpoint{}).ClearGateRecheckPending("anything")
	if (&Checkpoint{}).IsGateRecheckPending("anything") {
		t.Fatal("empty checkpoint should report no pending rechecks")
	}
}

func TestCheckpoint_OverriddenGates_Roundtrip(t *testing.T) {
	cp := &Checkpoint{}
	// nil-map safety: read/clear before any write must not panic.
	if cp.IsGateOverridden("gate") {
		t.Fatal("IsGateOverridden on nil map = true, want false")
	}
	cp.ClearGateOverridden("gate") // must not panic on nil map

	cp.MarkGateOverridden("gate")
	if !cp.IsGateOverridden("gate") {
		t.Fatal("IsGateOverridden after Mark = false, want true")
	}

	data, err := json.Marshal(cp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Checkpoint
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.IsGateOverridden("gate") {
		t.Fatal("overridden gate did not survive JSON round-trip")
	}

	got.ClearGateOverridden("gate")
	if got.IsGateOverridden("gate") {
		t.Fatal("IsGateOverridden after Clear = true, want false")
	}

	// omitempty: an empty set must not appear in JSON (backward-compat).
	empty, _ := json.Marshal(&Checkpoint{})
	if strings.Contains(string(empty), "overridden_gates") {
		t.Fatalf("empty checkpoint JSON contains overridden_gates: %s", empty)
	}
}
