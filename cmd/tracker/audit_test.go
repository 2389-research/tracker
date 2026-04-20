// ABOUTME: Tests for the audit subcommand — verifies report generation from on-disk artifacts.
// ABOUTME: Covers success, retries, restarts, not-found, and flag parsing for audit mode.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// makeCheckpoint creates a checkpoint.json in the given run directory.
func makeCheckpoint(t *testing.T, runDir string, cp map[string]interface{}) {
	t.Helper()
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		t.Fatalf("marshal checkpoint: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "checkpoint.json"), data, 0o600); err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}
}

// makeActivity creates an activity.jsonl in the given run directory.
func makeActivity(t *testing.T, runDir string, lines []map[string]interface{}) {
	t.Helper()
	var buf strings.Builder
	for _, line := range lines {
		data, err := json.Marshal(line)
		if err != nil {
			t.Fatalf("marshal activity line: %v", err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(runDir, "activity.jsonl"), []byte(buf.String()), 0o644); err != nil {
		t.Fatalf("write activity.jsonl: %v", err)
	}
}

// setupTestRun creates a run directory with checkpoint and activity log for testing.
func setupTestRun(t *testing.T, runID string) (workdir string, runDir string) {
	t.Helper()
	workdir = t.TempDir()
	runDir = filepath.Join(workdir, ".tracker", "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}
	return workdir, runDir
}

func TestRunAudit_Success(t *testing.T) {
	workdir, runDir := setupTestRun(t, "abc123def456")

	now := time.Now()
	makeCheckpoint(t, runDir, map[string]interface{}{
		"run_id":          "abc123def456",
		"current_node":    "",
		"completed_nodes": []string{"Start", "Implement", "Review"},
		"retry_counts":    map[string]int{},
		"context":         map[string]string{},
		"timestamp":       now.Format(time.RFC3339),
		"restart_count":   0,
	})

	startTime := now.Add(-3 * time.Minute)
	makeActivity(t, runDir, []map[string]interface{}{
		{"ts": startTime.Format(time.RFC3339Nano), "type": "pipeline_started", "run_id": "abc123def456", "message": "pipeline started"},
		{"ts": startTime.Add(1 * time.Second).Format(time.RFC3339Nano), "type": "stage_started", "run_id": "abc123def456", "node_id": "Start", "message": "executing node \"Start\""},
		{"ts": startTime.Add(2 * time.Second).Format(time.RFC3339Nano), "type": "stage_completed", "run_id": "abc123def456", "node_id": "Start", "message": "node \"Start\" completed"},
		{"ts": startTime.Add(3 * time.Second).Format(time.RFC3339Nano), "type": "stage_started", "run_id": "abc123def456", "node_id": "Implement", "message": "executing node \"Implement\""},
		{"ts": startTime.Add(2*time.Minute + 32*time.Second).Format(time.RFC3339Nano), "type": "stage_completed", "run_id": "abc123def456", "node_id": "Implement", "message": "node \"Implement\" completed"},
		{"ts": startTime.Add(2*time.Minute + 33*time.Second).Format(time.RFC3339Nano), "type": "stage_started", "run_id": "abc123def456", "node_id": "Review", "message": "executing node \"Review\""},
		{"ts": startTime.Add(3 * time.Minute).Format(time.RFC3339Nano), "type": "stage_completed", "run_id": "abc123def456", "node_id": "Review", "message": "node \"Review\" completed"},
		{"ts": startTime.Add(3*time.Minute + 1*time.Second).Format(time.RFC3339Nano), "type": "pipeline_completed", "run_id": "abc123def456", "message": "pipeline completed"},
	})

	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runAudit(workdir, "abc123def456")

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("runAudit returned error: %v", err)
	}

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	// Verify header section.
	if !strings.Contains(output, "abc123def456") {
		t.Fatalf("expected run ID in output, got:\n%s", output)
	}
	if !strings.Contains(output, "3 completed") {
		t.Fatalf("expected '3 completed' in output, got:\n%s", output)
	}
	if !strings.Contains(output, "Restarts:  0") {
		t.Fatalf("expected restarts count in output, got:\n%s", output)
	}

	// Verify timeline section.
	if !strings.Contains(output, "Timeline") {
		t.Fatalf("expected Timeline section in output, got:\n%s", output)
	}
	if !strings.Contains(output, "pipeline_started") {
		t.Fatalf("expected pipeline_started in timeline, got:\n%s", output)
	}
	if !strings.Contains(output, "Implement") {
		t.Fatalf("expected Implement node in timeline, got:\n%s", output)
	}

	// Verify no retries or errors sections with content.
	if !strings.Contains(output, "Retries") {
		t.Fatalf("expected Retries section in output, got:\n%s", output)
	}
	if !strings.Contains(output, "Errors") {
		t.Fatalf("expected Errors section in output, got:\n%s", output)
	}
	if !strings.Contains(output, "Recommendations") {
		t.Fatalf("expected Recommendations section in output, got:\n%s", output)
	}
}

func TestRunAudit_WithRetries(t *testing.T) {
	workdir, runDir := setupTestRun(t, "retry123")

	now := time.Now()
	makeCheckpoint(t, runDir, map[string]interface{}{
		"run_id":          "retry123",
		"current_node":    "",
		"completed_nodes": []string{"Start", "Implement"},
		"retry_counts":    map[string]int{"Implement": 3, "SpecReview": 1},
		"context":         map[string]string{},
		"timestamp":       now.Format(time.RFC3339),
		"restart_count":   0,
	})

	startTime := now.Add(-5 * time.Minute)
	makeActivity(t, runDir, []map[string]interface{}{
		{"ts": startTime.Format(time.RFC3339Nano), "type": "pipeline_started", "run_id": "retry123", "message": "pipeline started"},
		{"ts": startTime.Add(5 * time.Minute).Format(time.RFC3339Nano), "type": "pipeline_completed", "run_id": "retry123", "message": "pipeline completed"},
	})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runAudit(workdir, "retry123")

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("runAudit returned error: %v", err)
	}

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	// Verify retries section shows retry counts.
	if !strings.Contains(output, "Implement") || !strings.Contains(output, "3 retries") {
		t.Fatalf("expected Implement with 3 retries in output, got:\n%s", output)
	}
	if !strings.Contains(output, "SpecReview") || !strings.Contains(output, "1 retry") {
		t.Fatalf("expected SpecReview with 1 retry in output, got:\n%s", output)
	}

	// Verify recommendations mention retries.
	if !strings.Contains(output, "retry_policy") {
		t.Fatalf("expected retry_policy recommendation in output, got:\n%s", output)
	}
}

func TestRunAudit_WithRestarts(t *testing.T) {
	workdir, runDir := setupTestRun(t, "restart456")

	now := time.Now()
	makeCheckpoint(t, runDir, map[string]interface{}{
		"run_id":          "restart456",
		"current_node":    "",
		"completed_nodes": []string{"Start"},
		"retry_counts":    map[string]int{},
		"context":         map[string]string{},
		"timestamp":       now.Format(time.RFC3339),
		"restart_count":   2,
	})

	startTime := now.Add(-1 * time.Minute)
	makeActivity(t, runDir, []map[string]interface{}{
		{"ts": startTime.Format(time.RFC3339Nano), "type": "pipeline_started", "run_id": "restart456", "message": "pipeline started"},
		{"ts": now.Format(time.RFC3339Nano), "type": "pipeline_completed", "run_id": "restart456", "message": "pipeline completed"},
	})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runAudit(workdir, "restart456")

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("runAudit returned error: %v", err)
	}

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	// Verify restarts header.
	if !strings.Contains(output, "Restarts:  2") {
		t.Fatalf("expected 'Restarts:  2' in output, got:\n%s", output)
	}

	// Verify restart recommendation.
	if !strings.Contains(output, "restarted 2 time") {
		t.Fatalf("expected restart recommendation in output, got:\n%s", output)
	}
}

func TestRunAudit_NotFound(t *testing.T) {
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, ".tracker", "runs"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	err := runAudit(workdir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent run ID")
	}
	if !strings.Contains(err.Error(), "no run found") {
		t.Fatalf("expected 'no run found' error, got: %v", err)
	}
}

func TestParseFlagsAudit(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "audit", "abc123"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.mode != modeAudit {
		t.Fatalf("mode = %q, want %q", cfg.mode, modeAudit)
	}
	if cfg.resumeID != "abc123" {
		t.Fatalf("resumeID = %q, want %q", cfg.resumeID, "abc123")
	}
}

func TestParseFlagsAuditNoRunID(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "audit"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.mode != modeAudit {
		t.Fatalf("mode = %q, want %q", cfg.mode, modeAudit)
	}
	if cfg.resumeID != "" {
		t.Fatalf("resumeID = %q, want empty", cfg.resumeID)
	}
}

func TestRunAudit_WithErrors(t *testing.T) {
	workdir, runDir := setupTestRun(t, "error789")

	now := time.Now()
	makeCheckpoint(t, runDir, map[string]interface{}{
		"run_id":          "error789",
		"current_node":    "Implement",
		"completed_nodes": []string{"Start"},
		"retry_counts":    map[string]int{},
		"context":         map[string]string{},
		"timestamp":       now.Format(time.RFC3339),
		"restart_count":   0,
	})

	startTime := now.Add(-2 * time.Minute)
	makeActivity(t, runDir, []map[string]interface{}{
		{"ts": startTime.Format(time.RFC3339Nano), "type": "pipeline_started", "run_id": "error789", "message": "pipeline started"},
		{"ts": startTime.Add(30 * time.Second).Format(time.RFC3339Nano), "type": "stage_started", "run_id": "error789", "node_id": "Implement", "message": "executing node \"Implement\""},
		{"ts": startTime.Add(60 * time.Second).Format(time.RFC3339Nano), "type": "stage_failed", "run_id": "error789", "node_id": "Implement", "message": "node failed", "error": "handler error: exit code 1"},
		{"ts": startTime.Add(61 * time.Second).Format(time.RFC3339Nano), "type": "pipeline_failed", "run_id": "error789", "message": "pipeline failed"},
	})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runAudit(workdir, "error789")

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("runAudit returned error: %v", err)
	}

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	// Verify error appears in errors section.
	if !strings.Contains(output, "handler error: exit code 1") {
		t.Fatalf("expected error message in output, got:\n%s", output)
	}

	// Verify failed pipeline recommendation.
	if !strings.Contains(output, "Pipeline failed at") {
		t.Fatalf("expected pipeline failed recommendation, got:\n%s", output)
	}
}

func TestRunAudit_MalformedActivityLines(t *testing.T) {
	workdir, runDir := setupTestRun(t, "malformed123")

	now := time.Now()
	makeCheckpoint(t, runDir, map[string]interface{}{
		"run_id":          "malformed123",
		"current_node":    "",
		"completed_nodes": []string{"Start"},
		"retry_counts":    map[string]int{},
		"context":         map[string]string{},
		"timestamp":       now.Format(time.RFC3339),
		"restart_count":   0,
	})

	// Write activity with some malformed lines mixed in.
	content := `not valid json
{"ts":"` + now.Format(time.RFC3339Nano) + `","type":"pipeline_started","run_id":"malformed123","message":"pipeline started"}
{broken
{"ts":"` + now.Format(time.RFC3339Nano) + `","type":"pipeline_completed","run_id":"malformed123","message":"pipeline completed"}
`
	if err := os.WriteFile(filepath.Join(runDir, "activity.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatalf("write activity: %v", err)
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runAudit(workdir, "malformed123")

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("runAudit should tolerate malformed lines, got error: %v", err)
	}

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	// Should still have the valid events.
	if !strings.Contains(output, "pipeline_started") {
		t.Fatalf("expected valid events to still appear, got:\n%s", output)
	}
}

func TestExecuteCommandRoutesAuditMode(t *testing.T) {
	workdir, runDir := setupTestRun(t, "auditcmd123")

	now := time.Now()
	makeCheckpoint(t, runDir, map[string]interface{}{
		"run_id":          "auditcmd123",
		"current_node":    "",
		"completed_nodes": []string{"Start"},
		"retry_counts":    map[string]int{},
		"context":         map[string]string{},
		"timestamp":       now.Format(time.RFC3339),
		"restart_count":   0,
	})

	makeActivity(t, runDir, []map[string]interface{}{
		{"ts": now.Format(time.RFC3339Nano), "type": "pipeline_started", "run_id": "auditcmd123"},
		{"ts": now.Format(time.RFC3339Nano), "type": "pipeline_completed", "run_id": "auditcmd123"},
	})

	// Redirect stdout so audit output doesn't pollute test output.
	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := executeCommand(runConfig{
		mode:     modeAudit,
		workdir:  workdir,
		resumeID: "auditcmd123",
	}, commandDeps{})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("executeCommand audit mode returned error: %v", err)
	}
}

func TestExecuteCommandAuditNoRunIDListsRuns(t *testing.T) {
	workdir, runDir := setupTestRun(t, "listabc123")

	now := time.Now()
	makeCheckpoint(t, runDir, map[string]interface{}{
		"run_id":          "listabc123",
		"current_node":    "",
		"completed_nodes": []string{"Start", "Build"},
		"retry_counts":    map[string]int{},
		"context":         map[string]string{},
		"timestamp":       now.Format(time.RFC3339),
		"restart_count":   0,
	})
	makeActivity(t, runDir, []map[string]interface{}{
		{"ts": now.Add(-1 * time.Minute).Format(time.RFC3339Nano), "type": "pipeline_started", "run_id": "listabc123"},
		{"ts": now.Format(time.RFC3339Nano), "type": "pipeline_completed", "run_id": "listabc123"},
	})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := executeCommand(runConfig{
		mode:    modeAudit,
		workdir: workdir,
	}, commandDeps{})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if !strings.Contains(output, "listabc123") {
		t.Fatalf("expected run ID in listing, got:\n%s", output)
	}
	if !strings.Contains(output, "ok") {
		t.Fatalf("expected 'ok' status in listing, got:\n%s", output)
	}
	if !strings.Contains(output, "1 runs total") {
		t.Fatalf("expected '1 runs total' in listing, got:\n%s", output)
	}
}

func TestListRunsMultipleRuns(t *testing.T) {
	workdir := t.TempDir()
	runsDir := filepath.Join(workdir, ".tracker", "runs")

	now := time.Now()

	// Create a successful run.
	successDir := filepath.Join(runsDir, "success111")
	if err := os.MkdirAll(successDir, 0o755); err != nil {
		t.Fatal(err)
	}
	makeCheckpoint(t, successDir, map[string]interface{}{
		"run_id":          "success111",
		"current_node":    "",
		"completed_nodes": []string{"A", "B", "C"},
		"retry_counts":    map[string]int{},
		"context":         map[string]string{},
		"timestamp":       now.Format(time.RFC3339),
		"restart_count":   0,
	})
	makeActivity(t, successDir, []map[string]interface{}{
		{"ts": now.Add(-2 * time.Minute).Format(time.RFC3339Nano), "type": "pipeline_started", "run_id": "success111"},
		{"ts": now.Format(time.RFC3339Nano), "type": "pipeline_completed", "run_id": "success111"},
	})

	// Create a failed run.
	failDir := filepath.Join(runsDir, "fail222")
	if err := os.MkdirAll(failDir, 0o755); err != nil {
		t.Fatal(err)
	}
	makeCheckpoint(t, failDir, map[string]interface{}{
		"run_id":          "fail222",
		"current_node":    "Implement",
		"completed_nodes": []string{"Start"},
		"retry_counts":    map[string]int{"Implement": 2},
		"context":         map[string]string{},
		"timestamp":       now.Add(-5 * time.Minute).Format(time.RFC3339),
		"restart_count":   0,
	})
	makeActivity(t, failDir, []map[string]interface{}{
		{"ts": now.Add(-10 * time.Minute).Format(time.RFC3339Nano), "type": "pipeline_started", "run_id": "fail222"},
		{"ts": now.Add(-5 * time.Minute).Format(time.RFC3339Nano), "type": "pipeline_failed", "run_id": "fail222"},
	})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := listRuns(workdir)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if !strings.Contains(output, "success111") {
		t.Fatalf("expected success run in listing, got:\n%s", output)
	}
	if !strings.Contains(output, "fail222") {
		t.Fatalf("expected failed run in listing, got:\n%s", output)
	}
	if !strings.Contains(output, "FAIL") {
		t.Fatalf("expected FAIL status in listing, got:\n%s", output)
	}
	if !strings.Contains(output, "Implement") {
		t.Fatalf("expected failed node name in listing, got:\n%s", output)
	}
	if !strings.Contains(output, "2 runs total") {
		t.Fatalf("expected '2 runs total' in listing, got:\n%s", output)
	}
}

func TestListRunsEmptyDir(t *testing.T) {
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, ".tracker", "runs"), 0o755); err != nil {
		t.Fatal(err)
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := listRuns(workdir)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if !strings.Contains(output, "No runs found") {
		t.Fatalf("expected 'No runs found' for empty dir, got:\n%s", output)
	}
}

func TestListRunsNoRunsDir(t *testing.T) {
	workdir := t.TempDir()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := listRuns(workdir)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	output := string(buf[:n])

	if !strings.Contains(output, "No runs found") {
		t.Fatalf("expected 'No runs found' when dir missing, got:\n%s", output)
	}
}

func TestListRuns_SuppressesLibraryWarningsOnStderr(t *testing.T) {
	workdir, runDir := setupTestRun(t, "bad-activity")

	now := time.Now()
	makeCheckpoint(t, runDir, map[string]interface{}{
		"run_id":          "bad-activity",
		"current_node":    "",
		"completed_nodes": []string{"Start"},
		"retry_counts":    map[string]int{},
		"context":         map[string]string{},
		"timestamp":       now.Format(time.RFC3339),
		"restart_count":   0,
	})
	activityPath := filepath.Join(runDir, "activity.jsonl")
	if err := os.WriteFile(activityPath, []byte(`{"ts":"2026-04-17T10:00:00Z","type":"pipeline_started"}`), 0o644); err != nil {
		t.Fatalf("write activity: %v", err)
	}
	if err := os.Chmod(activityPath, 0o000); err != nil {
		t.Fatalf("chmod activity: %v", err)
	}

	oldStdout := os.Stdout
	stdoutR, stdoutW, _ := os.Pipe()
	os.Stdout = stdoutW
	oldStderr := os.Stderr
	stderrR, stderrW, _ := os.Pipe()
	os.Stderr = stderrW

	err := listRuns(workdir)

	stdoutW.Close()
	os.Stdout = oldStdout
	stderrW.Close()
	os.Stderr = oldStderr

	if err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}

	stdoutBuf := make([]byte, 8192)
	nOut, _ := stdoutR.Read(stdoutBuf)
	stdout := string(stdoutBuf[:nOut])
	if !strings.Contains(stdout, "bad-activity") {
		t.Fatalf("expected run in stdout listing, got:\n%s", stdout)
	}

	stderrBuf := make([]byte, 8192)
	nErr, _ := stderrR.Read(stderrBuf)
	stderr := string(stderrBuf[:nErr])
	if strings.Contains(stderr, "warning: run bad-activity: cannot read activity log") {
		t.Fatalf("expected warning to be suppressed on stderr, got:\n%s", stderr)
	}
}
