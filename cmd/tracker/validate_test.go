// ABOUTME: Tests for the validate subcommand — verifies DOT file validation output and exit behavior.
// ABOUTME: Covers valid pipelines, validation errors, warnings, and CLI flag parsing.
package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/2389-research/tracker/internal/dipxtest"
	"github.com/2389-research/tracker/pipeline"
)

const validDOT = `digraph test {
	Start [shape=Mdiamond];
	Work [shape=box];
	End [shape=Msquare];
	Start -> Work;
	Work -> End;
}`

const invalidDOTNoStart = `digraph test {
	Work [shape=box];
	End [shape=Msquare];
	Work -> End;
}`

// warningOnlyDOT exercises tracker's structural lint without firing any
// errors: Check is a diamond (conditional) node with a labeled "yes" edge
// and an unlabeled fallthrough — that triggers validateEdgeLabelConsistency
// ("inconsistent edge label usage"). DIP1XX warnings are owned by
// dippin-lang and don't apply to DOT graphs.
const warningOnlyDOT = `digraph test {
	Start [shape=Mdiamond];
	Check [shape=diamond];
	EndA [shape=Msquare];
	Start -> Check;
	Check -> EndA [label="yes" condition="outcome=success"];
	Check -> EndA [condition="outcome=fail"];
}`

func TestValidateValid(t *testing.T) {
	path := writeTestDOT(t, validDOT)
	var buf bytes.Buffer
	err := runValidateCmd(path, "", &buf)
	if err != nil {
		t.Fatalf("expected no error for valid DOT, got: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "valid") {
		t.Errorf("expected 'valid' in output, got: %s", output)
	}
	if !strings.Contains(output, "3 nodes") {
		t.Errorf("expected '3 nodes' in output, got: %s", output)
	}
}

func TestValidateErrors(t *testing.T) {
	path := writeTestDOT(t, invalidDOTNoStart)
	var buf bytes.Buffer
	err := runValidateCmd(path, "", &buf)
	if err == nil {
		t.Fatal("expected error for invalid DOT")
	}
	output := buf.String()
	if !strings.Contains(output, "error") {
		t.Errorf("expected 'error' in output, got: %s", output)
	}
	if !strings.Contains(output, "start node") {
		t.Errorf("expected 'start node' error, got: %s", output)
	}
}

func TestValidateWarningsOnly(t *testing.T) {
	path := writeTestDOT(t, warningOnlyDOT)
	var buf bytes.Buffer
	err := runValidateCmd(path, "", &buf)
	if err != nil {
		t.Fatalf("warnings should not cause error, got: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "warning") {
		t.Errorf("expected 'warning' in output, got: %s", output)
	}
}

func TestValidateMissingFile(t *testing.T) {
	var buf bytes.Buffer
	err := runValidateCmd("/nonexistent/file.dot", "", &buf)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestValidateInvalidSyntax(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.dot")
	if err := os.WriteFile(path, []byte("not valid{{{"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	err := runValidateCmd(path, "", &buf)
	if err == nil {
		t.Fatal("expected error for invalid syntax")
	}
	if !strings.Contains(err.Error(), "load pipeline") {
		t.Errorf("expected load pipeline error, got: %v", err)
	}
}

func TestParseFlagsValidate(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "validate", "pipeline.dot"})
	if err != nil {
		t.Fatalf("parseFlags error: %v", err)
	}
	if cfg.mode != modeValidate {
		t.Errorf("mode = %q, want validate", cfg.mode)
	}
	if cfg.pipelineFile != "pipeline.dot" {
		t.Errorf("pipelineFile = %q, want pipeline.dot", cfg.pipelineFile)
	}
}

// TestValidateDipxBundle is the regression test for the Task 5 dispatch
// path on validate. After validate.go was migrated to route through
// loadPipelineAndBundle, a future refactor that re-routed it back to a
// plain file-read + ValidateSource pair would silently break .dipx
// validation. Pack a real bundle and assert the command exits clean with
// "valid" in the output.
func TestValidateDipxBundle(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "entry.dip")
	if err := os.WriteFile(entry, []byte(dipxtest.MinimalDip("validate_dipx", "start", "exit")), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	bundlePath := dipxtest.PackTestBundle(t, entry)

	var buf bytes.Buffer
	if err := runValidateCmd(bundlePath, "", &buf); err != nil {
		t.Fatalf("runValidateCmd on .dipx: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "valid") {
		t.Errorf("expected 'valid' in .dipx output, got:\n%s", output)
	}
}

// dipWithLintWarning is a minimal valid .dip workflow that triggers DIP110
// (empty agent prompt) on a middle agent node. dippin-lang's DIP110 check
// exempts start and exit lifecycle nodes by design (see dippin README), so
// a middle agent with no prompt is needed to actually trip the lint.
const dipWithLintWarning = `workflow validate_lint_dup
  start: a
  exit: c

  agent a
    label: "Start"

  agent b
    label: "Middle"

  agent c
    label: "Exit"

  edges
    a -> b
    b -> c
`

// captureStderr redirects os.Stderr for the duration of fn and returns the
// bytes written. Used by TestValidateNoDuplicateLintWarnings to assert the
// long-form diagnostic (printed to stderr by loadDippinPipeline) does not get
// re-emitted on stdout by printValidationResult — the bug #244 fixed.
//
// Cleanup is single-pass: an inner func() wraps fn so a deferred close of
// the pipe write end and restore of os.Stderr fires whether fn returns
// normally or panics. The reader goroutine closes the read end via its own
// defer before sending the result, so neither end of the pipe leaks. Any
// io.ReadAll error is surfaced via t.Fatalf rather than swallowed.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("captureStderr: pipe: %v", err)
	}
	os.Stderr = w

	type readResult struct {
		bytes []byte
		err   error
	}
	done := make(chan readResult, 1)
	go func() {
		defer func() { _ = r.Close() }()
		b, err := io.ReadAll(r)
		done <- readResult{bytes: b, err: err}
	}()

	func() {
		defer func() {
			_ = w.Close()
			os.Stderr = orig
		}()
		fn()
	}()

	result := <-done
	if result.err != nil {
		t.Fatalf("captureStderr: read pipe: %v", result.err)
	}
	return string(result.bytes)
}

// TestValidateNoDuplicateLintWarnings is the regression test for #244:
// `tracker validate` was printing every DIP1XX warning twice — once in long
// form from the loader's stderr diagnostic path, once in short form from
// LintWarnings folded into the validator's warnings channel. The fix
// suppresses the short-form copy from stdout by skipping entries that match
// the pre-formatted strings on graph.LintWarnings.
//
// We assert the precise contract of the fix:
//  1. stderr carries at least one `warning[DIP` line (loader did its job)
//  2. stdout carries ZERO `warning[DIP` lines (CLI suppressed the duplicate)
//  3. summary line still reports a non-zero warning count (DIP warnings
//     are counted via len(result.Warnings), not via what we printed)
//
// This is intentionally not a "one occurrence per DIP code" check —
// multiple nodes can legitimately trip the same code in a real workflow.
// The dedup target is "loader-emitted strings", not "code uniqueness".
func TestValidateNoDuplicateLintWarnings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lint_dup.dip")
	if err := os.WriteFile(path, []byte(dipWithLintWarning), 0o644); err != nil {
		t.Fatalf("write dip: %v", err)
	}

	var stdout bytes.Buffer
	stderr := captureStderr(t, func() {
		if err := runValidateCmd(path, "", &stdout); err != nil {
			t.Fatalf("runValidateCmd: %v", err)
		}
	})

	dipRE := regexp.MustCompile(`warning\[DIP\d+\]`)

	if !dipRE.MatchString(stderr) {
		t.Errorf("expected loader to emit at least one warning[DIPnnn] line on stderr, got:\n%s", stderr)
	}

	if loc := dipRE.FindStringIndex(stdout.String()); loc != nil {
		t.Errorf("stdout still contains a warning[DIPnnn] line that the loader already printed to stderr (#244 regression).\nstdout:\n%s", stdout.String())
	}

	// The summary line must still report the DIP warning in its count.
	// "valid with 0 warning(s)" would mean the dedup accidentally subtracted
	// the warning from the total.
	if !strings.Contains(stdout.String(), "valid with ") {
		t.Errorf("expected 'valid with N warning(s)' summary on stdout, got:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "valid with 0 warning(s)") {
		t.Errorf("summary undercounted DIP warnings (reported 0):\n%s", stdout.String())
	}
}

// TestValidateLintWarningsStillInWarningsChannel guards against accidental
// regressions of the API contract that PR review (Codex P2 on #245) flagged:
// pipeline.ValidateAll / ValidateAllWithLint must continue to expose DIP1XX
// warnings via ValidationError.Warnings so non-CLI consumers
// (tracker.ValidateSource, tracker_doctor.go::checkPipelineFile,
// cmd/tracker-conformance) keep seeing them. The CLI dedup happens at print
// time and must NOT alter this shared validation result.
func TestValidateLintWarningsStillInWarningsChannel(t *testing.T) {
	graph, diags, err := pipeline.LoadDippinWorkflow(dipWithLintWarning, "lint_dup.dip")
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v", err)
	}
	if len(diags) == 0 {
		t.Fatalf("expected at least one diagnostic from LoadDippinWorkflow, got none")
	}
	if len(graph.LintWarnings) == 0 {
		t.Fatalf("expected graph.LintWarnings to be populated, got empty")
	}
	ve := pipeline.ValidateAll(graph)
	if ve == nil {
		t.Fatalf("ValidateAll returned nil; expected DIP1XX warnings to surface in ve.Warnings")
	}
	wantPrefix := "warning[DIP"
	foundDIP := false
	for _, w := range ve.Warnings {
		if strings.HasPrefix(w, wantPrefix) {
			foundDIP = true
			break
		}
	}
	if !foundDIP {
		t.Errorf("ValidateAll dropped DIP1XX warnings from ve.Warnings; non-CLI consumers (tracker.ValidateSource, doctor) would lose this signal.\nve.Warnings: %v\ngraph.LintWarnings: %v", ve.Warnings, graph.LintWarnings)
	}
}

func TestExecuteCommandValidateMissingPipelineFile(t *testing.T) {
	err := executeCommand(runConfig{
		mode: modeValidate,
	}, commandDeps{})
	if err == nil {
		t.Fatal("expected error for missing dot file")
	}
	if !strings.Contains(err.Error(), "usage") {
		t.Errorf("expected usage error, got: %v", err)
	}
}
