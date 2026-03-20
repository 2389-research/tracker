// ABOUTME: Tests for the validate subcommand — verifies DOT file validation output and exit behavior.
// ABOUTME: Covers valid pipelines, validation errors, warnings, and CLI flag parsing.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

const warningOnlyDOT = `digraph test {
	Start [shape=Mdiamond];
	Check [shape=diamond];
	EndA [shape=Msquare];
	Start -> Check;
	Check -> EndA [label="yes" condition="outcome=success"];
}`

func TestValidateValid(t *testing.T) {
	path := writeTestDOT(t, validDOT)
	var buf bytes.Buffer
	err := runValidateCmd(path, &buf)
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
	err := runValidateCmd(path, &buf)
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
	err := runValidateCmd(path, &buf)
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
	err := runValidateCmd("/nonexistent/file.dot", &buf)
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
	err := runValidateCmd(path, &buf)
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
	if cfg.dotFile != "pipeline.dot" {
		t.Errorf("dotFile = %q, want pipeline.dot", cfg.dotFile)
	}
}

func TestExecuteCommandValidateMissingDotFile(t *testing.T) {
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
