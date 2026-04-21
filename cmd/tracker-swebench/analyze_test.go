// ABOUTME: Tests for the `tracker-swebench analyze` subcommand — synthetic results-dir fixtures.
// ABOUTME: Covers predictions parsing, log scanning, empty-patch diagnostics, resolved_ids detection, and report shape.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeInstanceLog writes a minimal <id>.log in the format the harness emits.
func writeInstanceLog(t *testing.T, dir, id string, turns int, elapsed string, inTok, outTok int64, errMsg string) {
	t.Helper()
	content := fmt.Sprintf("instance_id: %s\nelapsed: %s\nturns: %d\ninput_tokens: %d\noutput_tokens: %d\npatch_lines: 0\n",
		id, elapsed, turns, inTok, outTok)
	if errMsg != "" {
		content += "error: " + errMsg + "\n"
	}
	path := filepath.Join(dir, id+".log")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// writeEmptyDiag writes an <id>.empty-patch.json.
func writeEmptyDiag(t *testing.T, dir, id string, turns int, reason, final string, calls []string) {
	t.Helper()
	if err := WriteEmptyPatchDiagnostic(dir, EmptyPatchDiagnostic{
		InstanceID:        id,
		Turns:             turns,
		TerminationReason: reason,
		FinalMessage:      final,
		LastToolCalls:     calls,
	}); err != nil {
		t.Fatalf("WriteEmptyPatchDiagnostic: %v", err)
	}
}

// buildFixture creates a synthetic results-dir covering all five report
// sections: resolved, patched-but-unresolved, empty (some with diagnostics),
// and errors split across setup/patch/harness classes.
func buildFixture(t *testing.T, withEvaluator bool) string {
	t.Helper()
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}

	// run_meta.json
	meta := RunMeta{
		Model:    "claude-sonnet-4-6",
		Provider: "anthropic",
		Dataset:  "swebench_lite.jsonl",
		MaxTurns: 50,
		Timeout:  "30m",
		Commit:   "abc1234",
	}
	if err := WriteRunMeta(filepath.Join(dir, "run_meta.json"), meta); err != nil {
		t.Fatalf("WriteRunMeta: %v", err)
	}

	// predictions.jsonl
	w, err := NewResultsWriter(filepath.Join(dir, "predictions.jsonl"), "claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("NewResultsWriter: %v", err)
	}
	// Resolved (django)
	_ = w.WritePrediction("django__django-11099", "diff --git a/x.py\n+x")
	// Unresolved but patched (django)
	_ = w.WritePrediction("django__django-11100", "diff --git a/y.py\n+y")
	// Patched (sympy) — would be resolved when evaluator says so
	_ = w.WritePrediction("sympy__sympy-14396", "diff --git a/z.py\n+z")
	// Empty patch with diagnostic (matplotlib)
	_ = w.WritePrediction("matplotlib__matplotlib-25079", "")
	// Empty patch without diagnostic (sphinx) — to test degraded mode per-instance
	_ = w.WritePrediction("sphinx-doc__sphinx-8435", "")
	// Error — setup (sympy)
	_ = w.WritePrediction("sympy__sympy-11870", "")
	// Error — patch (django)
	_ = w.WritePrediction("django__django-11910", "")
	// Error — harness (scikit)
	_ = w.WritePrediction("scikit-learn__scikit-learn-13496", "")
	_ = w.Close()

	// Per-instance .log files.
	writeInstanceLog(t, logsDir, "django__django-11099", 12, "2m3s", 120000, 4000, "")
	writeInstanceLog(t, logsDir, "django__django-11100", 48, "15m30s", 450000, 12000, "")
	writeInstanceLog(t, logsDir, "sympy__sympy-14396", 25, "5m10s", 230000, 8000, "")
	writeInstanceLog(t, logsDir, "matplotlib__matplotlib-25079", 50, "8m", 0, 0, "")
	writeInstanceLog(t, logsDir, "sphinx-doc__sphinx-8435", 22, "3m15s", 0, 0, "")
	writeInstanceLog(t, logsDir, "sympy__sympy-11870", 0, "10s", 0, 0, "clone repo: docker exec swe: exit status 128\nstderr: fatal: repository not found")
	writeInstanceLog(t, logsDir, "django__django-11910", 5, "30s", 0, 0, "agent-runner: git apply --index patch.diff failed: patch does not apply")
	writeInstanceLog(t, logsDir, "scikit-learn__scikit-learn-13496", 7, "1m", 0, 0, "agent-runner: panic: runtime error: invalid memory address")

	// Empty-patch diagnostic — only for matplotlib, not for sphinx.
	writeEmptyDiag(t, logsDir, "matplotlib__matplotlib-25079", 50, "max_turns_reached",
		"I believe I've explored the code sufficiently but can't identify the root cause of the bug reliably. The test expects X but the current code produces Y.",
		[]string{"read", "grep", "bash"})

	if withEvaluator {
		// Evaluator report with django__django-11099 resolved.
		eval := map[string]any{
			"resolved_ids": []string{"django__django-11099", "sympy__sympy-14396"},
			"error_ids":    []string{},
		}
		data, _ := json.MarshalIndent(eval, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "run.evaluation.json"), data, 0o644); err != nil {
			t.Fatalf("write evaluator report: %v", err)
		}
	}
	return dir
}

func TestBuildAnalyzeReport_WithEvaluator(t *testing.T) {
	dir := buildFixture(t, true)
	r, err := BuildAnalyzeReport(dir, 10)
	if err != nil {
		t.Fatalf("BuildAnalyzeReport: %v", err)
	}

	// Overall counts
	if !r.Counts.ResolvedKnown {
		t.Error("expected ResolvedKnown true with evaluator report")
	}
	if r.Counts.Total != 8 {
		t.Errorf("Total = %d, want 8", r.Counts.Total)
	}
	if r.Counts.Resolved != 2 {
		t.Errorf("Resolved = %d, want 2", r.Counts.Resolved)
	}
	if r.Counts.Unresolved != 1 {
		t.Errorf("Unresolved = %d, want 1", r.Counts.Unresolved)
	}
	if r.Counts.Empty != 2 {
		t.Errorf("Empty = %d, want 2", r.Counts.Empty)
	}
	if r.Counts.Errors != 3 {
		t.Errorf("Errors = %d, want 3", r.Counts.Errors)
	}
	if r.EvaluatorReportPath == "" {
		t.Error("expected EvaluatorReportPath populated")
	}

	// Per-repo breakdown — should have 5 repos
	repos := map[string]RepoBreakdown{}
	for _, rb := range r.PerRepo {
		repos[rb.Repo] = rb
	}
	if len(repos) != 5 {
		t.Errorf("expected 5 repos, got %d: %v", len(repos), keys(repos))
	}
	if d, ok := repos["django/django"]; !ok {
		t.Error("missing django/django repo row")
	} else if d.Total != 3 || d.Resolved != 1 || d.Errors != 1 {
		t.Errorf("django: total=%d resolved=%d errors=%d; want 3,1,1", d.Total, d.Resolved, d.Errors)
	}

	// Top empty — 2 empties, 1 has diagnostic
	if r.TopEmptyPatches.TotalEmpty != 2 {
		t.Errorf("TotalEmpty = %d, want 2", r.TopEmptyPatches.TotalEmpty)
	}
	if r.TopEmptyPatches.DiagnosticsFound != 1 {
		t.Errorf("DiagnosticsFound = %d, want 1", r.TopEmptyPatches.DiagnosticsFound)
	}
	if !r.TopEmptyPatches.DiagnosticsAvailable {
		t.Error("expected DiagnosticsAvailable true when at least one diagnostic is present")
	}
	if len(r.TopEmptyPatches.Instances) != 2 {
		t.Fatalf("expected 2 empty instances in top list, got %d", len(r.TopEmptyPatches.Instances))
	}
	// Diagnostic-bearing instance ranked first.
	if r.TopEmptyPatches.Instances[0].InstanceID != "matplotlib__matplotlib-25079" {
		t.Errorf("first empty = %q, want matplotlib__matplotlib-25079", r.TopEmptyPatches.Instances[0].InstanceID)
	}
	if r.TopEmptyPatches.Instances[0].TerminationReason != "max_turns_reached" {
		t.Errorf("TerminationReason = %q", r.TopEmptyPatches.Instances[0].TerminationReason)
	}
	if !strings.Contains(r.TopEmptyPatches.Instances[0].FinalMessage, "explored the code") {
		t.Errorf("FinalMessage not captured: %q", r.TopEmptyPatches.Instances[0].FinalMessage)
	}

	// Top longest unresolved — should exclude resolved, empty, error
	//   Candidates: django__django-11100 (48 turns) — the only unresolved patched instance
	if len(r.TopLongestUnresolved) != 1 {
		t.Fatalf("expected 1 longest unresolved, got %d", len(r.TopLongestUnresolved))
	}
	if r.TopLongestUnresolved[0].InstanceID != "django__django-11100" {
		t.Errorf("TopLongestUnresolved[0] = %q", r.TopLongestUnresolved[0].InstanceID)
	}
	if r.TopLongestUnresolved[0].Turns != 48 {
		t.Errorf("expected 48 turns, got %d", r.TopLongestUnresolved[0].Turns)
	}

	// Error classes — 1 setup, 1 patch, 1 harness
	ec := r.ErrorClasses
	if ec.Total != 3 {
		t.Errorf("ec.Total = %d, want 3", ec.Total)
	}
	if ec.Setup != 1 || ec.Patch != 1 || ec.Harness != 1 {
		t.Errorf("setup=%d patch=%d harness=%d; want 1,1,1", ec.Setup, ec.Patch, ec.Harness)
	}
	if len(ec.Samples) != 3 {
		t.Errorf("expected 3 samples, got %d", len(ec.Samples))
	}
}

func TestBuildAnalyzeReport_WithoutEvaluator(t *testing.T) {
	dir := buildFixture(t, false)
	r, err := BuildAnalyzeReport(dir, 10)
	if err != nil {
		t.Fatalf("BuildAnalyzeReport: %v", err)
	}
	if r.Counts.ResolvedKnown {
		t.Error("expected ResolvedKnown false without evaluator report")
	}
	if r.Counts.Resolved != 0 {
		t.Errorf("Resolved = %d, want 0", r.Counts.Resolved)
	}
	if r.Counts.PatchedUnverified != 3 {
		t.Errorf("PatchedUnverified = %d, want 3", r.Counts.PatchedUnverified)
	}
	if r.EvaluatorReportPath != "" {
		t.Errorf("expected no evaluator path, got %q", r.EvaluatorReportPath)
	}
	// Without evaluator, all 3 patched instances are candidates for "longest unresolved".
	if len(r.TopLongestUnresolved) != 3 {
		t.Errorf("expected 3 longest candidates, got %d", len(r.TopLongestUnresolved))
	}
	// Sorted by turns desc: django-11100 (48), sympy-14396 (25), django-11099 (12).
	want := []string{"django__django-11100", "sympy__sympy-14396", "django__django-11099"}
	for i, id := range want {
		if r.TopLongestUnresolved[i].InstanceID != id {
			t.Errorf("TopLongest[%d] = %q, want %q", i, r.TopLongestUnresolved[i].InstanceID, id)
		}
	}
}

func TestBuildAnalyzeReport_NoDiagnosticsDegradation(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Two empty-patch instances, NO diagnostic files.
	w, _ := NewResultsWriter(filepath.Join(dir, "predictions.jsonl"), "m")
	_ = w.WritePrediction("django__django-1", "")
	_ = w.WritePrediction("django__django-2", "")
	_ = w.Close()
	writeInstanceLog(t, logsDir, "django__django-1", 10, "1m", 0, 0, "")
	writeInstanceLog(t, logsDir, "django__django-2", 20, "2m", 0, 0, "")

	r, err := BuildAnalyzeReport(dir, 10)
	if err != nil {
		t.Fatalf("BuildAnalyzeReport: %v", err)
	}
	if r.TopEmptyPatches.DiagnosticsAvailable {
		t.Error("expected DiagnosticsAvailable false when no diagnostic files present")
	}
	if r.TopEmptyPatches.Notes == "" {
		t.Error("expected degradation note")
	}
	if !strings.Contains(r.TopEmptyPatches.Notes, "no diagnostic data") {
		t.Errorf("Notes = %q", r.TopEmptyPatches.Notes)
	}
}

func TestBuildAnalyzeReport_MissingPredictions(t *testing.T) {
	dir := t.TempDir()
	if _, err := BuildAnalyzeReport(dir, 10); err == nil {
		t.Fatal("expected error when predictions.jsonl is missing")
	}
}

func TestBuildAnalyzeReport_MissingResultsDir(t *testing.T) {
	if _, err := BuildAnalyzeReport("/nonexistent/path/does/not/exist", 10); err == nil {
		t.Fatal("expected error when results dir does not exist")
	}
}

func TestWriteAnalyzeText_Golden(t *testing.T) {
	dir := buildFixture(t, true)
	r, err := BuildAnalyzeReport(dir, 10)
	if err != nil {
		t.Fatalf("BuildAnalyzeReport: %v", err)
	}
	var buf bytes.Buffer
	if err := WriteAnalyzeText(&buf, r); err != nil {
		t.Fatalf("WriteAnalyzeText: %v", err)
	}
	out := buf.String()

	mustContain := []string{
		"tracker-swebench analyze",
		"## Overall counts",
		"## Per-repo breakdown",
		"## Top empty-patch instances",
		"## Top longest unresolved instances",
		"## Error class distribution",
		"django/django",                // repo row
		"matplotlib__matplotlib-25079", // empty with diagnostic
		"max_turns_reached",            // termination reason
		"django__django-11100",         // longest unresolved
		"sympy__sympy-11870",           // setup error sample
		"Setup:",                       // error class label
		"Patch:",
		"Harness:",
	}
	for _, s := range mustContain {
		if !strings.Contains(out, s) {
			t.Errorf("expected output to contain %q; got:\n%s", s, out)
		}
	}
}

func TestRunAnalyze_JSON(t *testing.T) {
	dir := buildFixture(t, true)
	var buf bytes.Buffer
	if err := runAnalyze([]string{"--json", dir}, &buf); err != nil {
		t.Fatalf("runAnalyze: %v", err)
	}
	var r AnalyzeReport
	if err := json.Unmarshal(buf.Bytes(), &r); err != nil {
		t.Fatalf("parse JSON: %v\noutput:\n%s", err, buf.String())
	}
	if r.Counts.Total != 8 {
		t.Errorf("Total = %d, want 8", r.Counts.Total)
	}
	if r.Counts.Resolved != 2 {
		t.Errorf("Resolved = %d, want 2", r.Counts.Resolved)
	}
}

func TestRunAnalyze_MissingArg(t *testing.T) {
	var buf bytes.Buffer
	err := runAnalyze([]string{}, &buf)
	if err == nil {
		t.Fatal("expected error for missing results-dir arg")
	}
}

func TestRepoFromInstanceID(t *testing.T) {
	tests := map[string]string{
		"django__django-11099":             "django/django",
		"scikit-learn__scikit-learn-13496": "scikit-learn/scikit-learn",
		"sphinx-doc__sphinx-8435":          "sphinx-doc/sphinx",
		"matplotlib__matplotlib-25079":     "matplotlib/matplotlib",
		"psf__requests-1963":               "psf/requests",
		"no-separator":                     "unknown",
		"weird__thing":                     "weird/thing",
	}
	for id, want := range tests {
		if got := repoFromInstanceID(id); got != want {
			t.Errorf("repoFromInstanceID(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestParseElapsedSecs(t *testing.T) {
	tests := map[string]int64{
		"10s":    10,
		"2m3s":   123,
		"1h2m3s": 3723,
		"45s":    45,
		"":       0,
		"abc":    0,
		"1h":     3600,
	}
	for in, want := range tests {
		if got := parseElapsedSecs(in); got != want {
			t.Errorf("parseElapsedSecs(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestFindEvaluatorReport_BareArrayFormat(t *testing.T) {
	dir := t.TempDir()
	// predictions.jsonl so analyze doesn't early-fail on missing preds
	if err := os.WriteFile(filepath.Join(dir, "predictions.jsonl"), []byte{}, 0o644); err != nil {
		t.Fatalf("write preds: %v", err)
	}
	// Bare array form.
	if err := os.WriteFile(filepath.Join(dir, "resolved_ids.json"),
		[]byte(`["a","b","c"]`), 0o644); err != nil {
		t.Fatalf("write ids: %v", err)
	}
	ids, path, err := findEvaluatorReport(dir)
	if err != nil {
		t.Fatalf("findEvaluatorReport: %v", err)
	}
	if ids == nil {
		t.Fatal("expected non-nil resolved set")
	}
	if len(ids) != 3 {
		t.Errorf("len(ids) = %d, want 3", len(ids))
	}
	if !strings.HasSuffix(path, "resolved_ids.json") {
		t.Errorf("path = %q", path)
	}
}

// keys returns the sorted keys of a map — used for test diagnostics.
func keys(m map[string]RepoBreakdown) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
