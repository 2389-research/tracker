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

// TestAudit_PopulatesValidationOverrides verifies that override entries in
// the activity log land on AuditReport.ValidationOverrides with
// OverrideCount and StatusClass=succeeded. The checkpoint's empty sticky
// slice should not mask the activity-sourced entries.
func TestAudit_PopulatesValidationOverrides(t *testing.T) {
	workdir := t.TempDir()
	runDir := filepath.Join(workdir, ".tracker", "runs", "overrides-from-activity")
	must(t, os.MkdirAll(runDir, 0o755))

	cp := &pipeline.Checkpoint{
		RunID:     "overrides-from-activity",
		Timestamp: time.Now(),
		// ValidationOverrides intentionally empty — exercise the activity path.
	}
	must(t, pipeline.SaveCheckpoint(cp, filepath.Join(runDir, "checkpoint.json")))

	base := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	lines := []string{
		`{"ts":"` + base.Format(time.RFC3339Nano) + `","type":"pipeline_started","run_id":"overrides-from-activity"}`,
		`{"ts":"` + base.Add(time.Second).Format(time.RFC3339Nano) + `","type":"validation_overridden","run_id":"overrides-from-activity","override_gate":"GateA","override_label":"accept","override_actor":"human","override_subgraph_path":["Outer","Inner"]}`,
		`{"ts":"` + base.Add(2*time.Second).Format(time.RFC3339Nano) + `","type":"pipeline_completed","run_id":"overrides-from-activity"}`,
	}
	must(t, os.WriteFile(filepath.Join(runDir, "activity.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644))

	r, err := Audit(context.Background(), runDir)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if r.Status != "validation_overridden" {
		t.Errorf("Status = %q, want validation_overridden", r.Status)
	}
	if r.StatusClass != "succeeded" {
		t.Errorf("StatusClass = %q, want succeeded", r.StatusClass)
	}
	if r.OverrideCount != 1 {
		t.Fatalf("OverrideCount = %d, want 1", r.OverrideCount)
	}
	if len(r.ValidationOverrides) != 1 {
		t.Fatalf("ValidationOverrides len = %d, want 1", len(r.ValidationOverrides))
	}
	got := r.ValidationOverrides[0]
	if got.GateNodeID != "GateA" {
		t.Errorf("GateNodeID = %q, want GateA", got.GateNodeID)
	}
	if got.Label != "accept" {
		t.Errorf("Label = %q, want accept", got.Label)
	}
	if got.Actor != pipeline.ActorHuman {
		t.Errorf("Actor = %q, want %q", got.Actor, pipeline.ActorHuman)
	}
	if len(got.SubgraphPath) != 2 || got.SubgraphPath[0] != "Outer" || got.SubgraphPath[1] != "Inner" {
		t.Errorf("SubgraphPath = %v, want [Outer Inner]", got.SubgraphPath)
	}
	if got.Timestamp.IsZero() {
		t.Error("Timestamp not populated from activity entry")
	}
}

// TestAudit_FallsBackToCheckpointOverrides verifies that when the activity
// log has no validation_overridden entries but Checkpoint.ValidationOverrides
// is populated, Audit() falls back to the checkpoint.
func TestAudit_FallsBackToCheckpointOverrides(t *testing.T) {
	workdir := t.TempDir()
	runDir := filepath.Join(workdir, ".tracker", "runs", "overrides-from-checkpoint")
	must(t, os.MkdirAll(runDir, 0o755))

	cp := &pipeline.Checkpoint{
		RunID:     "overrides-from-checkpoint",
		Timestamp: time.Now(),
		// CurrentNode empty + sticky overrides → validation_overridden via
		// checkpoint fallback in classifyStatus.
		ValidationOverrides: []pipeline.OverrideDetail{
			{GateNodeID: "GateB", Label: "mark done", Actor: pipeline.ActorAutopilot},
		},
	}
	must(t, pipeline.SaveCheckpoint(cp, filepath.Join(runDir, "checkpoint.json")))
	// No activity.jsonl written — LoadActivityLog returns nil entries.

	r, err := Audit(context.Background(), runDir)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if r.Status != "validation_overridden" {
		t.Errorf("Status = %q, want validation_overridden", r.Status)
	}
	if r.StatusClass != "succeeded" {
		t.Errorf("StatusClass = %q, want succeeded", r.StatusClass)
	}
	if r.OverrideCount != 1 {
		t.Fatalf("OverrideCount = %d, want 1", r.OverrideCount)
	}
	if len(r.ValidationOverrides) != 1 {
		t.Fatalf("ValidationOverrides len = %d, want 1", len(r.ValidationOverrides))
	}
	if r.ValidationOverrides[0].GateNodeID != "GateB" {
		t.Errorf("GateNodeID = %q, want GateB", r.ValidationOverrides[0].GateNodeID)
	}
	if r.ValidationOverrides[0].Actor != pipeline.ActorAutopilot {
		t.Errorf("Actor = %q, want %q", r.ValidationOverrides[0].Actor, pipeline.ActorAutopilot)
	}
}

// TestRunSummary_OverrideCount verifies RunSummary.OverrideCount and
// StatusClass populate from activity-log override entries.
func TestRunSummary_OverrideCount(t *testing.T) {
	workdir := t.TempDir()
	runDir := filepath.Join(workdir, ".tracker", "runs", "run-with-overrides")
	must(t, os.MkdirAll(runDir, 0o755))

	cp := &pipeline.Checkpoint{
		RunID:     "run-with-overrides",
		Timestamp: time.Now(),
	}
	must(t, pipeline.SaveCheckpoint(cp, filepath.Join(runDir, "checkpoint.json")))

	base := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	lines := []string{
		`{"ts":"` + base.Format(time.RFC3339Nano) + `","type":"pipeline_started","run_id":"run-with-overrides"}`,
		`{"ts":"` + base.Add(time.Second).Format(time.RFC3339Nano) + `","type":"validation_overridden","run_id":"run-with-overrides","override_gate":"G1","override_actor":"human"}`,
		`{"ts":"` + base.Add(2*time.Second).Format(time.RFC3339Nano) + `","type":"validation_overridden","run_id":"run-with-overrides","override_gate":"G2","override_actor":"autopilot"}`,
		`{"ts":"` + base.Add(3*time.Second).Format(time.RFC3339Nano) + `","type":"pipeline_completed","run_id":"run-with-overrides"}`,
	}
	must(t, os.WriteFile(filepath.Join(runDir, "activity.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644))

	runs, err := ListRuns(workdir, AuditConfig{LogWriter: io.Discard})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	rs := runs[0]
	if rs.Status != "validation_overridden" {
		t.Errorf("Status = %q, want validation_overridden", rs.Status)
	}
	if rs.StatusClass != "succeeded" {
		t.Errorf("StatusClass = %q, want succeeded", rs.StatusClass)
	}
	if rs.OverrideCount != 2 {
		t.Errorf("OverrideCount = %d, want 2", rs.OverrideCount)
	}
}

// TestRunSummary_OverrideCountFromCheckpoint verifies that with no activity
// override entries, RunSummary.OverrideCount falls back to the checkpoint's
// sticky ValidationOverrides slice length.
func TestRunSummary_OverrideCountFromCheckpoint(t *testing.T) {
	workdir := t.TempDir()
	runDir := filepath.Join(workdir, ".tracker", "runs", "checkpoint-only-overrides")
	must(t, os.MkdirAll(runDir, 0o755))

	cp := &pipeline.Checkpoint{
		RunID:     "checkpoint-only-overrides",
		Timestamp: time.Now(),
		ValidationOverrides: []pipeline.OverrideDetail{
			{GateNodeID: "G1", Actor: pipeline.ActorHuman},
			{GateNodeID: "G2", Actor: pipeline.ActorHuman},
			{GateNodeID: "G3", Actor: pipeline.ActorAutopilot},
		},
	}
	must(t, pipeline.SaveCheckpoint(cp, filepath.Join(runDir, "checkpoint.json")))

	runs, err := ListRuns(workdir, AuditConfig{LogWriter: io.Discard})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	if runs[0].OverrideCount != 3 {
		t.Errorf("OverrideCount = %d, want 3", runs[0].OverrideCount)
	}
}

// TestBuildRunSummary_FailedAtForBudgetExceeded pins the Gap 5.2 D12
// downstream gate fix: budget_exceeded runs populate FailedAt the same way
// "fail" runs do. Pre-fix this branch was status == "fail" only, so a
// budget-halted run left FailedAt empty even though CurrentNode pointed at
// the node that hit the ceiling.
func TestBuildRunSummary_FailedAtForBudgetExceeded(t *testing.T) {
	workdir := t.TempDir()
	runDir := filepath.Join(workdir, ".tracker", "runs", "budget-halted")
	must(t, os.MkdirAll(runDir, 0o755))

	cp := &pipeline.Checkpoint{
		RunID:       "budget-halted",
		CurrentNode: "Mid",
		Timestamp:   time.Now(),
	}
	must(t, pipeline.SaveCheckpoint(cp, filepath.Join(runDir, "checkpoint.json")))

	base := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	lines := []string{
		`{"ts":"` + base.Format(time.RFC3339Nano) + `","type":"pipeline_started","run_id":"budget-halted"}`,
		`{"ts":"` + base.Add(time.Second).Format(time.RFC3339Nano) + `","type":"budget_exceeded","run_id":"budget-halted"}`,
	}
	must(t, os.WriteFile(filepath.Join(runDir, "activity.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644))

	runs, err := ListRuns(workdir, AuditConfig{LogWriter: io.Discard})
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	rs := runs[0]
	if rs.Status != "budget_exceeded" {
		t.Errorf("Status = %q, want budget_exceeded", rs.Status)
	}
	if rs.StatusClass != "failed" {
		t.Errorf("StatusClass = %q, want failed", rs.StatusClass)
	}
	if rs.FailedAt != "Mid" {
		t.Errorf("FailedAt = %q, want Mid", rs.FailedAt)
	}
}

// TestAuditRecommendations_HaltedAtForBudgetExceeded pins the matching
// downstream gate fix in buildAuditRecommendations: budget_exceeded with a
// non-empty CurrentNode now surfaces a "halted (budget exceeded) at <node>"
// recommendation. Pre-fix this branch was status == "fail" only.
func TestAuditRecommendations_HaltedAtForBudgetExceeded(t *testing.T) {
	workdir := t.TempDir()
	runDir := filepath.Join(workdir, ".tracker", "runs", "budget-rec")
	must(t, os.MkdirAll(runDir, 0o755))

	cp := &pipeline.Checkpoint{
		RunID:       "budget-rec",
		CurrentNode: "Build",
		Timestamp:   time.Now(),
	}
	must(t, pipeline.SaveCheckpoint(cp, filepath.Join(runDir, "checkpoint.json")))

	base := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	lines := []string{
		`{"ts":"` + base.Format(time.RFC3339Nano) + `","type":"pipeline_started","run_id":"budget-rec"}`,
		`{"ts":"` + base.Add(time.Second).Format(time.RFC3339Nano) + `","type":"budget_exceeded","run_id":"budget-rec"}`,
	}
	must(t, os.WriteFile(filepath.Join(runDir, "activity.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644))

	r, err := Audit(context.Background(), runDir)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if r.Status != "budget_exceeded" {
		t.Errorf("Status = %q, want budget_exceeded", r.Status)
	}
	if r.StatusClass != "failed" {
		t.Errorf("StatusClass = %q, want failed", r.StatusClass)
	}
	var foundHalted bool
	for _, rec := range r.Recommendations {
		if strings.Contains(rec, "halted (budget exceeded) at Build") {
			foundHalted = true
			break
		}
	}
	if !foundHalted {
		t.Errorf("missing halted-at recommendation; recs = %v", r.Recommendations)
	}
}

// TestAuditRecommendations_FailedAtForFailUnchanged guards the regression
// path the other direction: the original "fail" wording must keep firing for
// non-budget failures so existing scripts that grep for "Pipeline failed at"
// keep matching.
func TestAuditRecommendations_FailedAtForFailUnchanged(t *testing.T) {
	workdir := t.TempDir()
	runDir := filepath.Join(workdir, ".tracker", "runs", "fail-rec")
	must(t, os.MkdirAll(runDir, 0o755))

	cp := &pipeline.Checkpoint{
		RunID:       "fail-rec",
		CurrentNode: "Build",
		Timestamp:   time.Now(),
	}
	must(t, pipeline.SaveCheckpoint(cp, filepath.Join(runDir, "checkpoint.json")))

	base := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	lines := []string{
		`{"ts":"` + base.Format(time.RFC3339Nano) + `","type":"pipeline_started","run_id":"fail-rec"}`,
		`{"ts":"` + base.Add(time.Second).Format(time.RFC3339Nano) + `","type":"pipeline_failed","run_id":"fail-rec"}`,
	}
	must(t, os.WriteFile(filepath.Join(runDir, "activity.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644))

	r, err := Audit(context.Background(), runDir)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if r.Status != "fail" {
		t.Errorf("Status = %q, want fail", r.Status)
	}
	if r.StatusClass != "failed" {
		t.Errorf("StatusClass = %q, want failed", r.StatusClass)
	}
	var foundFailed bool
	for _, rec := range r.Recommendations {
		if strings.Contains(rec, "Pipeline failed at Build") {
			foundFailed = true
			break
		}
	}
	if !foundFailed {
		t.Errorf("missing failed-at recommendation; recs = %v", r.Recommendations)
	}
}

// TestAuditRecommendations_OverrideNotesAppearFirst verifies that when both
// an override and a retry condition fire, the override summary lands ahead of
// the retry note. Pre-D16 sort.Strings(recs) would have re-ordered them.
func TestAuditRecommendations_OverrideNotesAppearFirst(t *testing.T) {
	cp := &pipeline.Checkpoint{
		RunID: "override-first",
		// Retries on a node — "Consider adjusting retry_policy …" alphabetically
		// sorts BEFORE the override summary's "This run terminated …" line in
		// the legacy sort.Strings order ('C' < 'T'). The priority-order
		// contract requires the override line to win regardless.
		RetryCounts: map[string]int{"Build": 3},
	}
	overrides := []pipeline.OverrideDetail{
		{GateNodeID: "Gate1", Label: "accept", Actor: pipeline.ActorHuman},
	}

	recs := buildAuditRecommendations(cp, "validation_overridden", time.Second, overrides)
	if len(recs) < 3 {
		t.Fatalf("expected at least 3 recs (summary + per-override + retry), got %d: %v", len(recs), recs)
	}
	if !strings.HasPrefix(recs[0], "This run terminated via a validation override") {
		t.Errorf("recs[0] should be the override summary; got %q", recs[0])
	}
	if !strings.HasPrefix(recs[1], "Validation override at gate") {
		t.Errorf("recs[1] should be the per-override entry; got %q", recs[1])
	}
	if !strings.HasPrefix(recs[2], "Consider adjusting retry_policy for Build") {
		t.Errorf("recs[2] should be the retry note; got %q", recs[2])
	}
}

// TestAuditRecommendations_OverrideSummaryAndPerEntry verifies multiple
// override events produce a single summary line followed by one entry per
// OverrideDetail in input (chronological) order.
func TestAuditRecommendations_OverrideSummaryAndPerEntry(t *testing.T) {
	cp := &pipeline.Checkpoint{RunID: "multi-override"}
	overrides := []pipeline.OverrideDetail{
		{GateNodeID: "GateA", Label: "accept", Actor: pipeline.ActorHuman},
		{GateNodeID: "GateB", Label: "mark done", Actor: pipeline.ActorAutopilot},
		{GateNodeID: "GateC", Label: "ship", Actor: pipeline.ActorWebhook},
	}

	recs := buildAuditRecommendations(cp, "validation_overridden", time.Second, overrides)
	if len(recs) != 4 {
		t.Fatalf("want 4 recs (1 summary + 3 per-override), got %d: %v", len(recs), recs)
	}
	if !strings.HasPrefix(recs[0], "This run terminated via a validation override") {
		t.Errorf("recs[0] should be the summary; got %q", recs[0])
	}
	// Per-override entries in chronological order.
	for i, want := range []string{"GateA", "GateB", "GateC"} {
		got := recs[i+1]
		if !strings.Contains(got, want) {
			t.Errorf("recs[%d] = %q, want to contain gate %q", i+1, got, want)
		}
	}
	// Labels and actors should also land on the line.
	if !strings.Contains(recs[1], `label: "accept"`) || !strings.Contains(recs[1], "actor: human") {
		t.Errorf("recs[1] missing label/actor formatting: %q", recs[1])
	}
	if !strings.Contains(recs[2], `label: "mark done"`) || !strings.Contains(recs[2], "actor: autopilot") {
		t.Errorf("recs[2] missing label/actor formatting: %q", recs[2])
	}
	if !strings.Contains(recs[3], `label: "ship"`) || !strings.Contains(recs[3], "actor: webhook") {
		t.Errorf("recs[3] missing label/actor formatting: %q", recs[3])
	}
}

// TestAuditRecommendations_NoSort constructs a scenario where alphabetical
// sort would have reordered entries and verifies they appear in priority order
// (override > retry > restart > halted-at). This is the canary that pins the
// sort.Strings(recs) removal.
func TestAuditRecommendations_NoSort(t *testing.T) {
	cp := &pipeline.Checkpoint{
		RunID:        "no-sort",
		CurrentNode:  "Mid",
		RestartCount: 1,
		// "Consider adjusting …" ('C') and "Pipeline halted …" ('P') and
		// "Pipeline restarted …" ('P') would all alphabetically precede the
		// override summary "This run terminated …" ('T'). Pre-fix the override
		// entry was last in the sorted output; post-fix it is first.
		RetryCounts: map[string]int{"NodeX": 2},
	}
	overrides := []pipeline.OverrideDetail{
		{GateNodeID: "G1", Label: "accept", Actor: pipeline.ActorHuman},
	}

	recs := buildAuditRecommendations(cp, "budget_exceeded", time.Second, overrides)
	if len(recs) < 5 {
		t.Fatalf("want >=5 recs, got %d: %v", len(recs), recs)
	}
	wantOrder := []string{
		"This run terminated via a validation override",
		"Validation override at gate",
		"Consider adjusting retry_policy for NodeX",
		"Pipeline restarted 1 time",
		"Pipeline halted (budget exceeded) at Mid",
	}
	for i, prefix := range wantOrder {
		if !strings.HasPrefix(recs[i], prefix) {
			t.Errorf("recs[%d] = %q, want prefix %q", i, recs[i], prefix)
		}
	}
}

// TestAuditRecommendations_NoOverridesPreserveExisting guards the
// backward-compat path: with no overrides the existing retry / restart /
// halted-at logic still fires, and no override summary leaks through.
func TestAuditRecommendations_NoOverridesPreserveExisting(t *testing.T) {
	cp := &pipeline.Checkpoint{
		RunID:        "no-overrides",
		CurrentNode:  "Build",
		RestartCount: 2,
		RetryCounts:  map[string]int{"Test": 3},
	}

	recs := buildAuditRecommendations(cp, "fail", time.Second, nil)
	for _, r := range recs {
		if strings.HasPrefix(r, "This run terminated via a validation override") {
			t.Errorf("override summary leaked in non-override run: %q", r)
		}
		if strings.HasPrefix(r, "Validation override at gate") {
			t.Errorf("per-override entry leaked in non-override run: %q", r)
		}
	}
	var sawRetry, sawRestart, sawHaltedAt bool
	for _, r := range recs {
		switch {
		case strings.Contains(r, "Consider adjusting retry_policy for Test"):
			sawRetry = true
		case strings.Contains(r, "Pipeline restarted 2 times"):
			sawRestart = true
		case strings.Contains(r, "Pipeline failed at Build"):
			sawHaltedAt = true
		}
	}
	if !sawRetry {
		t.Errorf("missing retry rec; got %v", recs)
	}
	if !sawRestart {
		t.Errorf("missing restart rec; got %v", recs)
	}
	if !sawHaltedAt {
		t.Errorf("missing halted-at rec; got %v", recs)
	}
}

// TestAuditRecommendations_SubgraphPathInGate verifies that an
// OverrideDetail with a populated SubgraphPath renders the gate field as
// `outer/inner/gate` rather than dropping the path.
func TestAuditRecommendations_SubgraphPathInGate(t *testing.T) {
	cp := &pipeline.Checkpoint{RunID: "nested"}
	overrides := []pipeline.OverrideDetail{
		{
			GateNodeID:   "ApproveSpec",
			Label:        "accept",
			Actor:        pipeline.ActorHuman,
			SubgraphPath: []string{"Outer", "Inner"},
		},
	}

	recs := buildAuditRecommendations(cp, "validation_overridden", time.Second, overrides)
	if len(recs) < 2 {
		t.Fatalf("want >=2 recs (summary + entry), got %d: %v", len(recs), recs)
	}
	// The per-override entry is recs[1] (recs[0] is the summary).
	wantGate := `"Outer/Inner/ApproveSpec"`
	if !strings.Contains(recs[1], wantGate) {
		t.Errorf("expected gate path %s in entry; got %q", wantGate, recs[1])
	}
}

// TestAudit_StatusClass_ForKnownStatuses pins the StatusClass mapping for
// each of the four known Status values. Stable bucket categorization is the
// whole point of the StatusClass companion field — if this drifts, every
// downstream consumer that buckets on it drifts with it.
func TestAudit_StatusClass_ForKnownStatuses(t *testing.T) {
	cases := []struct {
		status string
		want   string
	}{
		{"success", "succeeded"},
		{"validation_overridden", "succeeded"},
		{"fail", "failed"},
		{"budget_exceeded", "failed"},
		{"unknown_future_status", "failed"}, // fail-closed for open enum extensions.
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			if got := statusClassFor(tc.status); got != tc.want {
				t.Errorf("statusClassFor(%q) = %q, want %q", tc.status, got, tc.want)
			}
		})
	}
}
