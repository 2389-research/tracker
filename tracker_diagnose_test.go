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

// TestDiagnose_ToolMarkerMissing verifies that the activity.jsonl parser
// picks up tool_marker_missing events and that the suggestion builder
// emits SuggestionToolMarkerMissing with distinct copy for the
// no-match vs. compile-error paths (#210), AND that it de-dupes per
// node when the same node emits the event multiple times (retry/loop
// scenario). The fixture has RunTests emitting twice (retry) plus
// BadRegex emitting once — so the suggestion list should have exactly
// 2 entries, with the RunTests entry noting the occurrence count and
// surfacing the LATEST captured tail.
func TestDiagnose_ToolMarkerMissing(t *testing.T) {
	r, err := Diagnose(context.Background(), "testdata/runs/marker_missing")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}

	var markerSuggestions []Suggestion
	for _, s := range r.Suggestions {
		if s.Kind == SuggestionToolMarkerMissing {
			markerSuggestions = append(markerSuggestions, s)
		}
	}
	if len(markerSuggestions) != 2 {
		t.Fatalf("got %d marker-missing suggestions, want 2 (one per node — RunTests retry de-duped)", len(markerSuggestions))
	}

	byNode := map[string]Suggestion{}
	for _, s := range markerSuggestions {
		byNode[s.NodeID] = s
	}

	runTests, ok := byNode["RunTests"]
	if !ok {
		t.Fatal("missing suggestion for RunTests (no-match path)")
	}
	if !strings.Contains(runTests.Message, "matched nothing") {
		t.Errorf("RunTests suggestion missing 'matched nothing' copy: %q", runTests.Message)
	}
	if !strings.Contains(runTests.Message, "second attempt") {
		t.Errorf("RunTests suggestion should include the LATEST CapturedTail (retry surface), got: %q", runTests.Message)
	}
	if !strings.Contains(runTests.Message, `^tests-(pass|fail)$`) {
		t.Errorf("RunTests suggestion should echo the configured pattern: %q", runTests.Message)
	}
	if !strings.Contains(runTests.Message, "2 occurrences") {
		t.Errorf("RunTests suggestion should note the retry count, got: %q", runTests.Message)
	}

	badRegex, ok := byNode["BadRegex"]
	if !ok {
		t.Fatal("missing suggestion for BadRegex (compile-error path)")
	}
	if !strings.Contains(badRegex.Message, "failed to compile") {
		t.Errorf("BadRegex suggestion missing 'failed to compile' copy: %q", badRegex.Message)
	}
	if !strings.Contains(badRegex.Message, "missing closing") {
		t.Errorf("BadRegex suggestion should include the regex compile error detail: %q", badRegex.Message)
	}
	if strings.Contains(badRegex.Message, "occurrences") {
		t.Errorf("BadRegex (single occurrence) should not have a retry-count suffix, got: %q", badRegex.Message)
	}
}

// TestDiagnose_ToolRouteMissing pins activity.jsonl parsing and the
// SuggestionToolRouteMissing emission for the route sentinel (#212).
// Mirrors TestDiagnose_ToolMarkerMissing in shape but the underlying
// mechanism is different (built-in sentinel vs. node-attribute regex).
func TestDiagnose_ToolRouteMissing(t *testing.T) {
	r, err := Diagnose(context.Background(), "testdata/runs/route_missing")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}

	var routeSuggestions []Suggestion
	for _, s := range r.Suggestions {
		if s.Kind == SuggestionToolRouteMissing {
			routeSuggestions = append(routeSuggestions, s)
		}
	}
	if len(routeSuggestions) != 1 {
		t.Fatalf("got %d route-missing suggestions, want 1", len(routeSuggestions))
	}

	s := routeSuggestions[0]
	if s.NodeID != "StrictRunTests" {
		t.Errorf("NodeID = %q, want StrictRunTests", s.NodeID)
	}
	if !strings.Contains(s.Message, "_TRACKER_ROUTE=") {
		t.Errorf("suggestion should mention the sentinel format, got: %q", s.Message)
	}
	if !strings.Contains(s.Message, "no sentinel") {
		t.Errorf("suggestion should include the CapturedTail content, got: %q", s.Message)
	}
	if !strings.Contains(s.Message, "ctx.tool_route") {
		t.Errorf("suggestion should mention the ctx.tool_route routing pattern, got: %q", s.Message)
	}
}
