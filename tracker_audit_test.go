package tracker

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/pipeline"
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

func TestAudit_MissingCheckpoint(t *testing.T) {
	_, err := Audit(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("expected missing checkpoint error")
	}
	if !strings.Contains(err.Error(), "load checkpoint") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAudit_MalformedCheckpointJSON(t *testing.T) {
	runDir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(runDir, "checkpoint.json"), []byte(`{not json}`), 0o644))

	_, err := Audit(context.Background(), runDir)
	if err == nil {
		t.Fatal("expected malformed checkpoint error")
	}
	if !strings.Contains(err.Error(), "load checkpoint") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAudit_EmptyRunDirectory(t *testing.T) {
	runDir := t.TempDir()
	_, err := Audit(context.Background(), runDir)
	if err == nil {
		t.Fatal("expected error for empty run directory")
	}
	if !strings.Contains(err.Error(), "load checkpoint") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListRuns_PopulatesBundleIdentity(t *testing.T) {
	workdir := t.TempDir()
	runDir := filepath.Join(workdir, ".tracker", "runs", "test-run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cp := &pipeline.Checkpoint{
		RunID:          "test-run-1",
		BundleIdentity: "sha256:listruns_test",
		Timestamp:      time.Now(),
	}
	if err := pipeline.SaveCheckpoint(cp, filepath.Join(runDir, "checkpoint.json")); err != nil {
		t.Fatal(err)
	}

	runs, err := ListRuns(workdir, AuditConfig{LogWriter: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("want 1 run, got %d", len(runs))
	}
	if runs[0].BundleIdentity != "sha256:listruns_test" {
		t.Errorf("BundleIdentity not populated: %q", runs[0].BundleIdentity)
	}
}

func TestAudit_PopulatesBundleIdentityFromCheckpoint(t *testing.T) {
	workdir := t.TempDir()
	runDir := filepath.Join(workdir, ".tracker", "runs", "audit-bundle-test")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cp := &pipeline.Checkpoint{
		RunID:          "audit-bundle-test",
		BundleIdentity: "sha256:audit_test_identity",
		Timestamp:      time.Now(),
	}
	if err := pipeline.SaveCheckpoint(cp, filepath.Join(runDir, "checkpoint.json")); err != nil {
		t.Fatal(err)
	}

	report, err := Audit(context.Background(), runDir)
	if err != nil {
		t.Fatal(err)
	}
	if report.BundleIdentity != "sha256:audit_test_identity" {
		t.Errorf("AuditReport.BundleIdentity = %q, want %q", report.BundleIdentity, "sha256:audit_test_identity")
	}
}

func TestAudit_EmptyBundleIdentity_ForPlainDipRuns(t *testing.T) {
	workdir := t.TempDir()
	runDir := filepath.Join(workdir, ".tracker", "runs", "plain-dip-audit")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cp := &pipeline.Checkpoint{
		RunID:     "plain-dip-audit",
		Timestamp: time.Now(),
		// BundleIdentity intentionally left empty (plain .dip)
	}
	if err := pipeline.SaveCheckpoint(cp, filepath.Join(runDir, "checkpoint.json")); err != nil {
		t.Fatal(err)
	}

	report, err := Audit(context.Background(), runDir)
	if err != nil {
		t.Fatal(err)
	}
	if report.BundleIdentity != "" {
		t.Errorf("AuditReport.BundleIdentity should be empty for plain .dip run, got %q", report.BundleIdentity)
	}
}

// TestClassifyStatus_Scenarios pins the spec §6.4 reverse-scan algorithm:
// failure dominates (pipeline_failed / budget_exceeded short-circuit);
// pipeline_completed + validation_overridden anywhere in scan resolves to
// validation_overridden; checkpoint fallback applies when no terminal
// activity event is found. D12 fix: budget_exceeded no longer collapses to
// "fail".
func TestClassifyStatus_Scenarios(t *testing.T) {
	cases := []struct {
		name   string
		events []ActivityEntry
		cp     *pipeline.Checkpoint
		want   string
	}{
		{
			name: "override then complete",
			events: []ActivityEntry{
				{Type: "validation_overridden"},
				{Type: "pipeline_completed"},
			},
			cp:   &pipeline.Checkpoint{CurrentNode: ""},
			want: "validation_overridden",
		},
		{
			name: "override then fail (failure dominates)",
			events: []ActivityEntry{
				{Type: "validation_overridden"},
				{Type: "pipeline_failed"},
			},
			cp:   &pipeline.Checkpoint{CurrentNode: ""},
			want: "fail",
		},
		{
			name: "override then budget (failure dominates)",
			events: []ActivityEntry{
				{Type: "validation_overridden"},
				{Type: "budget_exceeded"},
			},
			cp:   &pipeline.Checkpoint{CurrentNode: ""},
			want: "budget_exceeded",
		},
		{
			name: "budget_exceeded alone — D12 fix (was 'fail')",
			events: []ActivityEntry{
				{Type: "budget_exceeded"},
			},
			cp:   &pipeline.Checkpoint{CurrentNode: ""},
			want: "budget_exceeded",
		},
		{
			name:   "no terminal, CurrentNode set → fail",
			events: nil,
			cp:     &pipeline.Checkpoint{CurrentNode: "Mid"},
			want:   "fail",
		},
		{
			name:   "no terminal, CurrentNode empty, sticky has overrides → validation_overridden",
			events: nil,
			cp: &pipeline.Checkpoint{
				CurrentNode:         "",
				ValidationOverrides: []pipeline.OverrideDetail{{GateNodeID: "G"}},
			},
			want: "validation_overridden",
		},
		{
			name:   "no terminal, CurrentNode empty, no overrides → success",
			events: nil,
			cp:     &pipeline.Checkpoint{CurrentNode: ""},
			want:   "success",
		},
		{
			name: "resumed run: override on attempt 1, complete on attempt 2",
			events: []ActivityEntry{
				{Type: "pipeline_started"},
				{Type: "validation_overridden"},
				{Type: "pipeline_started"},
				{Type: "pipeline_completed"},
			},
			cp:   &pipeline.Checkpoint{CurrentNode: ""},
			want: "validation_overridden",
		},
		{
			name: "lone override + halted (CurrentNode != \"\")",
			events: []ActivityEntry{
				{Type: "validation_overridden"},
			},
			cp:   &pipeline.Checkpoint{CurrentNode: "Mid"},
			want: "fail",
		},
		{
			name: "lone override + completed run with sticky overrides (CurrentNode empty)",
			events: []ActivityEntry{
				{Type: "validation_overridden"},
			},
			cp: &pipeline.Checkpoint{
				CurrentNode:         "",
				ValidationOverrides: []pipeline.OverrideDetail{{GateNodeID: "G"}},
			},
			want: "validation_overridden",
		},
		{
			name: "lone override event, no sticky on checkpoint, CurrentNode empty → success",
			events: []ActivityEntry{
				{Type: "validation_overridden"},
			},
			cp:   &pipeline.Checkpoint{CurrentNode: ""},
			want: "success",
		},
		{
			name: "2x override then complete",
			events: []ActivityEntry{
				{Type: "validation_overridden"},
				{Type: "validation_overridden"},
				{Type: "pipeline_completed"},
			},
			cp:   &pipeline.Checkpoint{CurrentNode: ""},
			want: "validation_overridden",
		},
		{
			name: "success last",
			events: []ActivityEntry{
				{Type: "pipeline_completed"},
			},
			cp:   &pipeline.Checkpoint{CurrentNode: ""},
			want: "success",
		},
		{
			name: "fail last",
			events: []ActivityEntry{
				{Type: "pipeline_failed"},
			},
			cp:   &pipeline.Checkpoint{CurrentNode: ""},
			want: "fail",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyStatus(tc.cp, tc.events)
			if got != tc.want {
				t.Errorf("classifyStatus = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestListRuns_EmptyBundleIdentity_ForPlainDipRuns(t *testing.T) {
	workdir := t.TempDir()
	runDir := filepath.Join(workdir, ".tracker", "runs", "plain-dip-run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cp := &pipeline.Checkpoint{
		RunID:     "plain-dip-run",
		Timestamp: time.Now(),
		// BundleIdentity intentionally left empty (plain .dip)
	}
	if err := pipeline.SaveCheckpoint(cp, filepath.Join(runDir, "checkpoint.json")); err != nil {
		t.Fatal(err)
	}

	runs, err := ListRuns(workdir, AuditConfig{LogWriter: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("want 1 run, got %d", len(runs))
	}
	if runs[0].BundleIdentity != "" {
		t.Errorf("BundleIdentity should be empty for plain .dip run, got %q", runs[0].BundleIdentity)
	}
}
