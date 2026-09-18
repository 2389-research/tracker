// ABOUTME: Pins that the CLI load path prints dippin errors + warnings only —
// ABOUTME: hint-severity diagnostics (e.g. DIP125's sourced-function PATH probe) stay silent.
package main

import (
	"strings"
	"testing"

	"github.com/2389-research/dippin-lang/validator"
	"github.com/2389-research/tracker/pipeline"
)

// build_product's scripts call shell functions sourced from lib/ (and the
// `:` builtin), which dippin's DIP125 PATH probe reports as missing binaries
// (dippin-lang#315). Loading the built-in must not print those (or any other
// hint) on every run.
func TestLoadEmbeddedBuiltin_PrintsNoHints(t *testing.T) {
	info, ok := lookupBuiltinWorkflow("build_product")
	if !ok {
		t.Fatal("build_product not embedded")
	}
	out := captureStderr(t, func() {
		if _, err := loadEmbeddedPipeline(info); err != nil {
			t.Errorf("load: %v", err)
		}
	})
	if strings.Contains(out, "DIP125") || strings.Contains(out, "hint[") {
		t.Errorf("hint diagnostics leaked onto the CLI load path:\n%s", out)
	}
}

func TestPrintLoadDiagnostics_DropsHintsKeepsWarningsAndErrors(t *testing.T) {
	diags := []validator.Diagnostic{
		{Code: "DIP125", Severity: validator.SeverityHint, Message: "a hint"},
		{Code: "DIP102", Severity: validator.SeverityWarning, Message: "a warning"},
		{Code: "DIP001", Severity: validator.SeverityError, Message: "an error"},
	}
	out := captureStderr(t, func() { printLoadDiagnostics(diags) })
	if strings.Contains(out, "a hint") {
		t.Errorf("hint printed:\n%s", out)
	}
	for _, want := range []string{"a warning", "an error"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from:\n%s", want, out)
		}
	}
}

// TestDIP125_PlaceholderNoLongerMisreadAsBinary pins the dippin-lang#305 fix
// (v0.74.0): a `set -eu` body that interpolates `${graph.workflow_dir}` used
// to report the bogus binary "-eu"; the probe now substitutes the placeholder
// and walks to the real first command. If this regresses, the CLI's hint
// filter in printLoadDiagnostics is the only thing hiding it from users.
func TestDIP125_PlaceholderNoLongerMisreadAsBinary(t *testing.T) {
	src := "workflow Repro\n  goal: \"dip125\"\n  start: A\n  exit: B\n\n  tool A\n    command:\n      set -eu\n      LIB=\"${graph.workflow_dir}/scripts/lib\"\n      mkdir -p \"$LIB\"\n      ls \"$LIB\"\n\n  tool B\n    command: \"true\"\n\n  edges\n    A -> B\n"
	_, diags, err := pipeline.LoadDippinWorkflow(src, "repro.dip")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, d := range diags {
		if d.Code == "DIP125" && strings.Contains(d.Message, `"-eu"`) {
			t.Fatalf("dippin-lang#305 regressed — DIP125 misread the placeholder body: %s", d.String())
		}
	}
}
