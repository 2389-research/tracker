package tracker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
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

// TestDiagnose_AuditLogInjection_SecureLines pins that mixing
// sentinel-prefixed runtime lines and bare attacker-injected lines in
// the secure activity log fires SuggestionAuditLogInjection with the
// expected count and an explicit "detection, not authentication"
// caveat. The fixture is built inline because the secure path is
// resolved via TRACKER_AUDIT_DIR and can't live as a checked-in
// testdata file.
func TestDiagnose_AuditLogInjection_SecureLines(t *testing.T) {
	secureBase := t.TempDir()
	t.Setenv("TRACKER_AUDIT_DIR", secureBase)
	t.Setenv("XDG_STATE_HOME", "")

	runID := "diagnose-injection-test"
	runDir := filepath.Join(t.TempDir(), ".tracker", "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir runDir: %v", err)
	}
	writeCheckpoint(t, runDir, runID)

	secureDir := filepath.Join(secureBase, runID)
	if err := os.MkdirAll(secureDir, 0o700); err != nil {
		t.Fatalf("mkdir secureDir: %v", err)
	}
	securePath := filepath.Join(secureDir, "activity.jsonl")
	content := pipeline.ActivityLogSentinel + `{"ts":"2026-05-13T20:30:00Z","type":"pipeline_started"}` + "\n" +
		`{"ts":"2026-05-13T20:30:01Z","type":"pipeline_completed","message":"forged completion"}` + "\n" +
		pipeline.ActivityLogSentinel + `{"ts":"2026-05-13T20:30:02Z","type":"stage_started","node_id":"Build"}` + "\n" +
		`{"ts":"2026-05-13T20:30:03Z","type":"decision_edge","edge_from":"x","edge_to":"y"}` + "\n"
	if err := os.WriteFile(securePath, []byte(content), 0o600); err != nil {
		t.Fatalf("write secure log: %v", err)
	}

	r, err := Diagnose(context.Background(), runDir)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}

	var injections []Suggestion
	for _, s := range r.Suggestions {
		if s.Kind == SuggestionAuditLogInjection {
			injections = append(injections, s)
		}
	}
	if len(injections) != 1 {
		t.Fatalf("got %d injection suggestions, want 1; suggestions=%+v", len(injections), r.Suggestions)
	}
	msg := injections[0].Message
	if !strings.Contains(msg, "2 lines") {
		t.Errorf("message should report 2 injected lines, got: %q", msg)
	}
	if !strings.Contains(msg, securePath) {
		t.Errorf("message should include the secure audit log path %q, got: %q", securePath, msg)
	}
	if !strings.Contains(msg, "detection-only") {
		t.Errorf("message should call out detection-only / not authentication, got: %q", msg)
	}
}

// TestDiagnose_AuditLogInjection_LegacyPathNoSignal pins that a legacy
// run (no secure file, activity.jsonl directly under runDir) does NOT
// fire SuggestionAuditLogInjection regardless of whether the lines
// have sentinels. The legacy file is the post-#213 snapshot or a
// pre-#213 historical run; absence of sentinel there is not a signal.
func TestDiagnose_AuditLogInjection_LegacyPathNoSignal(t *testing.T) {
	// Point TRACKER_AUDIT_DIR at an empty dir so no secure file exists
	// for this runID — forces fallback to <runDir>/activity.jsonl.
	t.Setenv("TRACKER_AUDIT_DIR", t.TempDir())
	t.Setenv("XDG_STATE_HOME", "")

	runID := "diagnose-legacy-only"
	runDir := filepath.Join(t.TempDir(), ".tracker", "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir runDir: %v", err)
	}
	writeCheckpoint(t, runDir, runID)
	// Plain JSONL with no sentinel — simulates a post-snapshot copy or
	// a pre-#213 archived run.
	legacy := filepath.Join(runDir, "activity.jsonl")
	body := `{"ts":"2026-05-13T20:30:00Z","type":"pipeline_started"}` + "\n" +
		`{"ts":"2026-05-13T20:30:01Z","type":"pipeline_completed"}` + "\n"
	if err := os.WriteFile(legacy, []byte(body), 0o644); err != nil {
		t.Fatalf("write legacy log: %v", err)
	}

	r, err := Diagnose(context.Background(), runDir)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	for _, s := range r.Suggestions {
		if s.Kind == SuggestionAuditLogInjection {
			t.Errorf("legacy path should not fire SuggestionAuditLogInjection, got: %+v", s)
		}
	}
}

// TestDiagnose_PopulatesValidationOverrides verifies the override fields on
// DiagnoseReport are sourced from validation_overridden activity entries,
// even when the checkpoint sticky slice is empty. The override section is
// informational (per spec §9.4) — Diagnose() must NOT emit a Suggestion
// for the override itself.
func TestDiagnose_PopulatesValidationOverrides(t *testing.T) {
	runDir := t.TempDir()
	runID := "diag-overrides-activity"
	if err := os.WriteFile(filepath.Join(runDir, "checkpoint.json"),
		[]byte(fmt.Sprintf(`{"run_id":%q,"completed_nodes":[],"current_node":"","retry_counts":{},"restart_count":0,"timestamp":"2026-05-29T12:00:00Z"}`, runID)),
		0o644); err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}
	lines := `{"ts":"2026-05-29T12:00:00Z","type":"pipeline_started","run_id":"diag-overrides-activity"}` + "\n" +
		`{"ts":"2026-05-29T12:00:01Z","type":"validation_overridden","run_id":"diag-overrides-activity","override_gate":"GateA","override_label":"accept","override_actor":"human","override_subgraph_path":["Outer","Inner"]}` + "\n" +
		`{"ts":"2026-05-29T12:00:02Z","type":"pipeline_completed","run_id":"diag-overrides-activity"}` + "\n"
	if err := os.WriteFile(filepath.Join(runDir, "activity.jsonl"), []byte(lines), 0o644); err != nil {
		t.Fatalf("write activity: %v", err)
	}

	r, err := Diagnose(context.Background(), runDir)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
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
	// Override must NOT raise a Suggestion (spec §9.4: informational only).
	for _, s := range r.Suggestions {
		if strings.Contains(strings.ToLower(string(s.Kind)), "override") {
			t.Errorf("override should not raise a Suggestion, got: %+v", s)
		}
	}
}

// TestDiagnose_FallsBackToCheckpointForOverrides verifies that when no
// validation_overridden events live in the activity log,
// Checkpoint.ValidationOverrides feeds the report.
func TestDiagnose_FallsBackToCheckpointForOverrides(t *testing.T) {
	runDir := t.TempDir()
	runID := "diag-overrides-checkpoint"

	cp := &pipeline.Checkpoint{
		RunID: runID,
		ValidationOverrides: []pipeline.OverrideDetail{
			{GateNodeID: "GateB", Label: "mark done", Actor: pipeline.ActorAutopilot},
		},
	}
	if err := pipeline.SaveCheckpoint(cp, filepath.Join(runDir, "checkpoint.json")); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	// No activity.jsonl written — LoadActivityLog returns nil entries, so
	// extractOverridesFromActivity is empty and the checkpoint fallback wins.

	r, err := Diagnose(context.Background(), runDir)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
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

func writeCheckpoint(t *testing.T, runDir, runID string) {
	t.Helper()
	cp := fmt.Sprintf(`{"run_id":%q,"completed_nodes":[],"current_node":"","retry_counts":{},"restart_count":0,"timestamp":"2026-05-13T20:30:00Z"}`, runID)
	if err := os.WriteFile(filepath.Join(runDir, "checkpoint.json"), []byte(cp), 0o644); err != nil {
		t.Fatalf("write checkpoint: %v", err)
	}
}

// TestDiagnose_AutoStatusMissing pins activity.jsonl parsing and the
// SuggestionAutoStatusMissing emission for #346: an auto_status agent node
// that completed normally without a parseable STATUS line. The fixture has
// a fail-closed goal gate (VerifyMilestone) and a default-success plain node
// (Summarize) — the suggestion copy must distinguish the two.
func TestDiagnose_AutoStatusMissing(t *testing.T) {
	r, err := Diagnose(context.Background(), "testdata/runs/auto_status_missing")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}

	var suggestions []Suggestion
	for _, s := range r.Suggestions {
		if s.Kind == SuggestionAutoStatusMissing {
			suggestions = append(suggestions, s)
		}
	}
	if len(suggestions) != 2 {
		t.Fatalf("got %d auto-status-missing suggestions, want 2", len(suggestions))
	}

	byNode := map[string]Suggestion{}
	for _, s := range suggestions {
		byNode[s.NodeID] = s
	}

	gate, ok := byNode["VerifyMilestone"]
	if !ok {
		t.Fatal("missing suggestion for VerifyMilestone (fail-closed gate path)")
	}
	if !strings.Contains(gate.Message, "failed closed") {
		t.Errorf("gate suggestion should explain the fail-closed flip: %q", gate.Message)
	}
	if !strings.Contains(gate.Message, "0 of 8 criteria met") {
		t.Errorf("gate suggestion should include the response tail: %q", gate.Message)
	}

	plain, ok := byNode["Summarize"]
	if !ok {
		t.Fatal("missing suggestion for Summarize (default-success path)")
	}
	if !strings.Contains(plain.Message, "defaulted to success") {
		t.Errorf("plain suggestion should warn about the success default: %q", plain.Message)
	}
}
