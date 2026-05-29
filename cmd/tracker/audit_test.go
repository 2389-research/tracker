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

	tracker "github.com/2389-research/tracker"
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

// TestPrintAuditHeader_BundleLine verifies the Bundle: line is printed in the
// audit header when the AuditReport has a non-empty BundleIdentity.
func TestPrintAuditHeader_BundleLine(t *testing.T) {
	r := &tracker.AuditReport{
		RunID:          "test-run",
		Status:         "success",
		BundleIdentity: "sha256:efb5648d28e6c2",
	}
	out := captureStdout(t, func() { printAuditHeader(r) })
	if !strings.Contains(out, "Bundle:") {
		t.Errorf("Bundle: line missing:\n%s", out)
	}
	if !strings.Contains(out, "sha256:efb5648d28e6c2") {
		t.Errorf("identity not in header:\n%s", out)
	}
}

// TestPrintAuditHeader_NoBundleLine_WhenIdentityEmpty verifies the Bundle: line
// is omitted entirely when BundleIdentity is empty (plain .dip runs).
func TestPrintAuditHeader_NoBundleLine_WhenIdentityEmpty(t *testing.T) {
	r := &tracker.AuditReport{
		RunID:  "test-run",
		Status: "success",
	}
	out := captureStdout(t, func() { printAuditHeader(r) })
	if strings.Contains(out, "Bundle:") {
		t.Errorf("Bundle: line should not appear when identity empty:\n%s", out)
	}
}

// TestPrintRunList_BundleColumn verifies the Bundle column shows the truncated
// sha256 identity for runs from .dipx bundles, and stays empty for plain .dip
// runs.
func TestPrintRunList_BundleColumn(t *testing.T) {
	runs := []tracker.RunSummary{
		{RunID: "aaaaaaaa1234567890", Status: "success", Nodes: 1, BundleIdentity: "sha256:efb5648d28e6c250dfad5411651d427f4f62ca24e185ce6cfc51478a4c6711ab"},
		{RunID: "bbbbbbbb1234567890", Status: "success", Nodes: 1, BundleIdentity: ""},
	}
	out := captureStdout(t, func() { printRunList(runs) })
	if !strings.Contains(out, "Bundle") {
		t.Errorf("Bundle header missing:\n%s", out)
	}
	if !strings.Contains(out, "sha256:efb5648d28e6c2") {
		t.Errorf("truncated bundle hash should appear:\n%s", out)
	}
	// The plain .dip run should NOT have a hash on its line.
	// Look for "bbbbbbbb" and verify that line doesn't contain "sha256:".
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.Contains(line, "bbbbbbbb") {
			if strings.Contains(line, "sha256:") {
				t.Errorf("plain .dip run row should not have sha256:\n%s", line)
			}
		}
	}
}

// TestPrintRunList_StatusIcons verifies the Status column icon mapping for
// every status the runtime can emit: success → "ok", validation_overridden →
// "override", budget_exceeded → "budget", fail → "FAIL". Pins Gap 5.2 Task 21:
// the override/budget icons were silently rendered as the default "+" before
// classifyStatus (Task 15) and the icon switch widening here.
func TestPrintRunList_StatusIcons(t *testing.T) {
	runs := []tracker.RunSummary{
		{RunID: "successrun0000", Status: "success", Nodes: 1},
		{RunID: "overriderun000", Status: "validation_overridden", Nodes: 1},
		{RunID: "budgetrun00000", Status: "budget_exceeded", Nodes: 1, FailedAt: "BuildNode"},
		{RunID: "failrun0000000", Status: "fail", Nodes: 1, FailedAt: "ImplNode"},
	}
	out := captureStdout(t, func() { printRunList(runs) })

	lines := strings.Split(out, "\n")
	findLine := func(runIDPrefix string) string {
		t.Helper()
		for _, ln := range lines {
			if strings.Contains(ln, runIDPrefix) {
				return ln
			}
		}
		t.Fatalf("no row for runID %q in output:\n%s", runIDPrefix, out)
		return ""
	}

	cases := []struct {
		runID    string
		wantIcon string
	}{
		{"successrun0000", "ok"},
		{"overriderun000", "override"},
		{"budgetrun00000", "budget"},
		{"failrun0000000", "FAIL"},
	}
	for _, c := range cases {
		line := findLine(c.runID)
		if !strings.Contains(line, c.wantIcon) {
			t.Errorf("row for %s missing icon %q: %q", c.runID, c.wantIcon, line)
		}
	}

	// Sanity: the override row must NOT render as FAIL (regression guard for
	// the pre-Task-21 silent fallthrough).
	overrideLine := findLine("overriderun000")
	if strings.Contains(overrideLine, "FAIL") {
		t.Errorf("override row should not contain FAIL: %q", overrideLine)
	}
	budgetLine := findLine("budgetrun00000")
	if strings.Contains(budgetLine, "FAIL") {
		t.Errorf("budget row should not contain FAIL: %q", budgetLine)
	}
}

// TestPrintRunList_StatusColumnWidth verifies the Status header is padded to
// at least 10 characters (Gap 5.2 Task 21 widening) so the "override" (8) and
// "budget" (6) icons render without column collision into Nodes.
func TestPrintRunList_StatusColumnWidth(t *testing.T) {
	runs := []tracker.RunSummary{
		{RunID: "overriderun000", Status: "validation_overridden", Nodes: 7},
	}
	out := captureStdout(t, func() { printRunList(runs) })

	// Find the row for the override run and verify "override" and the Nodes
	// count are separated by the column's whitespace gap, not jammed adjacent.
	var row string
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "overriderun000") {
			row = ln
			break
		}
	}
	if row == "" {
		t.Fatalf("override row missing from output:\n%s", out)
	}
	// "override" is 8 chars; the format string pads to %-10s, leaving 2
	// trailing spaces. Combined with the 2-space inter-column gap, "override"
	// must be followed by at least 4 spaces before the Nodes value "7".
	if !strings.Contains(row, "override    ") {
		t.Errorf("Status column not widened to %%-10s — expected 4+ trailing spaces after \"override\":\n%q", row)
	}
}
