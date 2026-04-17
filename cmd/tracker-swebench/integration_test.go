// ABOUTME: Integration test verifying the dataset-to-results pipeline without Docker.
// ABOUTME: Tests dataset loading, prompt generation, prediction writing, and resumability.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFullPipeline_DatasetToResults(t *testing.T) {
	// 1. Create a temp dir.
	dir := t.TempDir()

	// 2. Write a 2-instance test dataset JSONL file.
	datasetPath := filepath.Join(dir, "dataset.jsonl")
	datasetContent := `{"instance_id":"test__repo-001","repo":"test/repo","base_commit":"abc","problem_statement":"Fix bug","hints_text":"","version":"1.0","environment_setup_commit":"abc"}
{"instance_id":"test__repo-002","repo":"test/repo","base_commit":"def","problem_statement":"Add feature","hints_text":"Look at utils.py","version":"1.0","environment_setup_commit":"def"}
`
	if err := os.WriteFile(datasetPath, []byte(datasetContent), 0o644); err != nil {
		t.Fatalf("failed to write dataset file: %v", err)
	}

	// 3. Load it with LoadDataset(), verify 2 instances.
	instances, err := LoadDataset(datasetPath)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(instances))
	}

	// 4. Check AgentPrompt() — without hints is just the problem statement,
	//    with hints includes the hints section.
	inst1 := instances[0]
	if inst1.InstanceID != "test__repo-001" {
		t.Errorf("instance 1 ID: got %q, want %q", inst1.InstanceID, "test__repo-001")
	}
	prompt1 := inst1.AgentPrompt()
	if prompt1 != "Fix bug" {
		t.Errorf("instance 1 prompt without hints: got %q, want %q", prompt1, "Fix bug")
	}

	inst2 := instances[1]
	if inst2.InstanceID != "test__repo-002" {
		t.Errorf("instance 2 ID: got %q, want %q", inst2.InstanceID, "test__repo-002")
	}
	prompt2 := inst2.AgentPrompt()
	if !strings.Contains(prompt2, "Add feature") {
		t.Errorf("instance 2 prompt: expected problem statement, got %q", prompt2)
	}
	if !strings.Contains(prompt2, "## Hints") {
		t.Errorf("instance 2 prompt: expected hints section, got %q", prompt2)
	}
	if !strings.Contains(prompt2, "Look at utils.py") {
		t.Errorf("instance 2 prompt: expected hints text, got %q", prompt2)
	}

	// 5. Write a prediction for instance 1 via NewResultsWriter() + WritePrediction().
	predictionsPath := filepath.Join(dir, "predictions.jsonl")
	w, err := NewResultsWriter(predictionsPath, "test-model")
	if err != nil {
		t.Fatalf("NewResultsWriter: %v", err)
	}
	if err := w.WritePrediction(inst1.InstanceID, "diff --git a/fix.py"); err != nil {
		t.Fatalf("WritePrediction: %v", err)
	}

	// 6. Close the writer.
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 7. Re-open the writer (simulating resume) — verify instance 1 is completed,
	//    instance 2 is not.
	w2, err := NewResultsWriter(predictionsPath, "test-model")
	if err != nil {
		t.Fatalf("NewResultsWriter (resume): %v", err)
	}
	defer w2.Close()

	if !w2.IsCompleted(inst1.InstanceID) {
		t.Errorf("resume: expected %q to be completed", inst1.InstanceID)
	}
	if w2.IsCompleted(inst2.InstanceID) {
		t.Errorf("resume: expected %q to NOT be completed", inst2.InstanceID)
	}
	if got := w2.CompletedCount(); got != 1 {
		t.Errorf("resume: CompletedCount = %d, want 1", got)
	}

	// 8. Write run_meta.json via WriteRunMeta(), verify file is non-empty.
	metaPath := filepath.Join(dir, "run_meta.json")
	meta := RunMeta{
		Model:    "test-model",
		Provider: "anthropic",
		Dataset:  datasetPath,
		MaxTurns: 10,
		Timeout:  "5m",
	}
	if err := WriteRunMeta(metaPath, meta); err != nil {
		t.Fatalf("WriteRunMeta: %v", err)
	}
	info, err := os.Stat(metaPath)
	if err != nil {
		t.Fatalf("stat run_meta.json: %v", err)
	}
	if info.Size() == 0 {
		t.Error("run_meta.json is empty")
	}
}
