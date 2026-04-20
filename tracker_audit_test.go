package tracker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAudit_CompletedRun(t *testing.T) {
	r, err := Audit(context.Background(), "testdata/runs/ok")
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if r.Status != "success" {
		t.Errorf("status = %q, want success", r.Status)
	}
	if len(r.Timeline) == 0 {
		t.Error("empty timeline")
	}
	if r.TotalDuration <= 0 {
		t.Error("expected positive total duration")
	}
}

func TestAudit_FailedRun(t *testing.T) {
	r, err := Audit(context.Background(), "testdata/runs/failed")
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if r.Status != "fail" {
		t.Errorf("status = %q, want fail", r.Status)
	}
	var foundRetry bool
	for _, rec := range r.Retries {
		if rec.NodeID == "Build" && rec.Attempts == 2 {
			foundRetry = true
		}
	}
	if !foundRetry {
		t.Errorf("missing Build retry record: %+v", r.Retries)
	}
	if len(r.Errors) == 0 {
		t.Error("expected error entries")
	}
}

func TestListRuns_MultipleRuns(t *testing.T) {
	workdir := t.TempDir()
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	must(t, os.MkdirAll(filepath.Join(runsDir, "r1"), 0o755))
	must(t, os.WriteFile(filepath.Join(runsDir, "r1", "checkpoint.json"),
		[]byte(`{"run_id":"r1","completed_nodes":["A"],"timestamp":"2026-04-17T10:00:00Z"}`), 0o644))
	must(t, os.MkdirAll(filepath.Join(runsDir, "r2"), 0o755))
	must(t, os.WriteFile(filepath.Join(runsDir, "r2", "checkpoint.json"),
		[]byte(`{"run_id":"r2","completed_nodes":["A","B"],"timestamp":"2026-04-17T11:00:00Z"}`), 0o644))

	runs, err := ListRuns(workdir)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2", len(runs))
	}
	if runs[0].RunID != "r2" {
		t.Errorf("first = %q, want r2 (newest first)", runs[0].RunID)
	}
}

func TestListRuns_LogWriterSilencesWarnings(t *testing.T) {
	// Build a run directory whose checkpoint loads fine but whose activity.jsonl
	// is unreadable (EISDIR). buildRunSummary should emit a warning to the
	// LogWriter rather than os.Stderr.
	workdir := t.TempDir()
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	must(t, os.MkdirAll(filepath.Join(runsDir, "r1"), 0o755))
	must(t, os.WriteFile(filepath.Join(runsDir, "r1", "checkpoint.json"),
		[]byte(`{"run_id":"r1","completed_nodes":["A"],"timestamp":"2026-04-17T10:00:00Z"}`), 0o644))
	// Make activity.jsonl a directory so os.ReadFile fails with EISDIR.
	must(t, os.MkdirAll(filepath.Join(runsDir, "r1", "activity.jsonl"), 0o755))

	var logBuf bytes.Buffer
	runs, err := ListRuns(workdir, AuditConfig{LogWriter: &logBuf})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	if logBuf.Len() == 0 {
		t.Error("expected log writer to receive a warning about activity.jsonl")
	}
}

// TestAudit_CtxCancelledAtEntry verifies Audit returns the caller's
// cancellation error immediately rather than silently proceeding with the
// expensive checkpoint + activity log reads.
func TestAudit_CtxCancelledAtEntry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Audit(ctx, "testdata/runs/ok")
	if err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
