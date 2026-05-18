// ABOUTME: Tests for the validate subcommand — verifies DOT file validation output and exit behavior.
// ABOUTME: Covers valid pipelines, validation errors, warnings, and CLI flag parsing.
package main

import (
	"bytes"
	"os"
	"path/filepath"
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
