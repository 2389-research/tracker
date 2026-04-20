package tracker

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnose_CleanRun(t *testing.T) {
	r, err := Diagnose("testdata/runs/ok")
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
	r, err := Diagnose("testdata/runs/failed")
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
	kinds := map[string]bool{}
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
	r, err := Diagnose("testdata/runs/budget_halted")
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

func TestDiagnoseWithConfig_LogsMalformedStatusJSON(t *testing.T) {
	runDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runDir, "checkpoint.json"),
		[]byte(`{"run_id":"bad-status","completed_nodes":["Start"],"timestamp":"2026-04-17T10:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(runDir, "Build"), 0o755); err != nil {
		t.Fatalf("mkdir node: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "Build", "status.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("write status: %v", err)
	}

	var logBuf bytes.Buffer
	r, err := DiagnoseWithConfig(runDir, DiagnoseConfig{LogWriter: &logBuf})
	if err != nil {
		t.Fatalf("DiagnoseWithConfig: %v", err)
	}
	if r.RunID != "bad-status" {
		t.Fatalf("run_id = %q", r.RunID)
	}
	if !strings.Contains(logBuf.String(), "warning: cannot parse") {
		t.Fatalf("missing warning in log output: %q", logBuf.String())
	}
}
