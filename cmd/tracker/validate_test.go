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
// (empty agent prompt) on a middle codergen node. Start and exit are exempt
// because the adapter retypes bare agent-shaped start/exit nodes to dedicated
// start/exit handlers, so the middle agent is the one that lands as a
// codergen-with-no-prompt and trips the lint.
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
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- b
	}()
	defer func() {
		_ = w.Close()
		os.Stderr = orig
	}()
	fn()
	_ = w.Close()
	os.Stderr = orig
	return string(<-done)
}

// TestValidateNoDuplicateLintWarnings is the regression test for #244:
// `tracker validate` was printing every DIP1XX warning twice — once in long
// form from the loader's stderr diagnostic path, once in short form from
// LintWarnings folded into the validator's warnings channel. The fix removes
// the second emission. Capture both streams and assert each DIP code appears
// exactly once across the combined output.
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

	combined := stderr + stdout.String()

	dipRE := regexp.MustCompile(`warning\[(DIP\d+)\]`)
	counts := map[string]int{}
	for _, m := range dipRE.FindAllStringSubmatch(combined, -1) {
		counts[m[1]]++
	}
	if len(counts) == 0 {
		t.Fatalf("expected at least one DIP1XX warning, got none.\ncombined:\n%s", combined)
	}
	for code, n := range counts {
		if n != 1 {
			t.Errorf("%s appeared %d times in combined output, want exactly 1.\ncombined:\n%s", code, n, combined)
		}
	}

	// The summary line must still report the DIP warning in its count even
	// though it no longer flows through ve.Warnings. Drop to 0 would mean
	// the printValidationResult lintCount add-in regressed.
	if !strings.Contains(stdout.String(), "valid with ") {
		t.Errorf("expected 'valid with N warning(s)' summary on stdout, got:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "valid with 0 warning(s)") {
		t.Errorf("summary undercounted DIP warnings (reported 0):\n%s", stdout.String())
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
