package tracker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnose_CleanRun(t *testing.T) {
	r, err := Diagnose(context.Background(), "testdata/runs/ok")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if r.RunID != "ok-run" {
		t.Errorf("run_id = %q", r.RunID)
	}
	if len(r.Failures) != 0 {
		t.Errorf("got %d failures on clean run", len(r.Failures))
	}
	if r.BudgetHalt != nil {
		t.Errorf("unexpected budget halt: %+v", r.BudgetHalt)
	}
	if len(r.Suggestions) != 0 {
		t.Errorf("got %d suggestions on clean run", len(r.Suggestions))
	}
}

func TestDiagnose_FailureWithRetries(t *testing.T) {
	r, err := Diagnose(context.Background(), "testdata/runs/failed")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(r.Failures) != 1 {
		t.Fatalf("got %d failures, want 1", len(r.Failures))
	}
	f := r.Failures[0]
	if f.NodeID != "Build" {
		t.Errorf("node = %q, want Build", f.NodeID)
	}
	if f.RetryCount != 2 {
		t.Errorf("retries = %d, want 2", f.RetryCount)
	}
	if !f.IdenticalRetries {
		t.Error("expected identical-retry detection")
	}
	if f.Handler != "tool" {
		t.Errorf("handler = %q", f.Handler)
	}
	kinds := map[SuggestionKind]bool{}
	for _, s := range r.Suggestions {
		kinds[s.Kind] = true
	}
	if !kinds["retry_pattern"] {
		t.Error("expected retry_pattern suggestion")
	}
	if !kinds["shell_command"] {
		t.Error("expected shell_command suggestion")
	}
}

func TestDiagnose_BudgetHalt(t *testing.T) {
	r, err := Diagnose(context.Background(), "testdata/runs/budget_halted")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if r.BudgetHalt == nil {
		t.Fatal("expected budget halt")
	}
	if r.BudgetHalt.TotalTokens != 120000 {
		t.Errorf("tokens = %d", r.BudgetHalt.TotalTokens)
	}
	if r.BudgetHalt.Message == "" {
		t.Error("empty breach message")
	}
}

// TestDiagnose_CtxCancelled verifies that a cancelled context propagates
// out of Diagnose — a partial report is never returned as a success, so
// automation with deadlines can distinguish complete from truncated output.
func TestDiagnose_CtxCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the call
	_, err := Diagnose(ctx, "testdata/runs/failed")
	if err == nil {
		t.Fatal("expected ctx.Err() to propagate, got nil")
	}
	if err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestDiagnoseMostRecent_SelectsNewestRun(t *testing.T) {
	workdir := t.TempDir()
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	if err := os.MkdirAll(filepath.Join(runsDir, "r1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runsDir, "r1", "checkpoint.json"),
		[]byte(`{"run_id":"r1","completed_nodes":["A"],"timestamp":"2026-04-17T10:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runsDir, "r2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runsDir, "r2", "checkpoint.json"),
		[]byte(`{"run_id":"r2","completed_nodes":["A","B"],"timestamp":"2026-04-17T11:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := DiagnoseMostRecent(context.Background(), workdir)
	if err != nil {
		t.Fatalf("DiagnoseMostRecent: %v", err)
	}
	if r.RunID != "r2" {
		t.Fatalf("run_id = %q, want r2", r.RunID)
	}
}

func TestDiagnoseMostRecent_WarnsOnMalformedCheckpointViaLogWriter(t *testing.T) {
	workdir := t.TempDir()
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	if err := os.MkdirAll(filepath.Join(runsDir, "bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runsDir, "bad", "checkpoint.json"), []byte(`{not json}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runsDir, "good"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runsDir, "good", "checkpoint.json"),
		[]byte(`{"run_id":"good","completed_nodes":["A"],"timestamp":"2026-04-17T11:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var logBuf bytes.Buffer
	r, err := DiagnoseMostRecent(context.Background(), workdir, DiagnoseConfig{LogWriter: &logBuf})
	if err != nil {
		t.Fatalf("DiagnoseMostRecent: %v", err)
	}
	if r.RunID != "good" {
		t.Fatalf("run_id = %q, want good", r.RunID)
	}
	if !strings.Contains(logBuf.String(), "warning: cannot load checkpoint for run bad") {
		t.Fatalf("expected warning in log writer, got: %q", logBuf.String())
	}
}

func TestDiagnose_MalformedStatusWarningContinues(t *testing.T) {
	runDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runDir, "checkpoint.json"),
		[]byte(`{"run_id":"run-1","completed_nodes":["Build"],"timestamp":"2026-04-17T11:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(runDir, "Build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "Build", "status.json"), []byte(`{not json}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var logBuf bytes.Buffer
	r, err := Diagnose(context.Background(), runDir, DiagnoseConfig{LogWriter: &logBuf})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if r.RunID != "run-1" {
		t.Fatalf("run_id = %q, want run-1", r.RunID)
	}
	if len(r.Failures) != 0 {
		t.Fatalf("failures = %d, want 0", len(r.Failures))
	}
	if !strings.Contains(logBuf.String(), "warning: cannot parse") {
		t.Fatalf("expected malformed status warning, got %q", logBuf.String())
	}
}
