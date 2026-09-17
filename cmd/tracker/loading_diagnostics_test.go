// ABOUTME: Pins that the CLI load path prints dippin errors + warnings only —
// ABOUTME: hint-severity diagnostics (e.g. DIP125's placeholder-confused PATH probe) stay silent.
package main

import (
	"strings"
	"testing"

	"github.com/2389-research/dippin-lang/validator"
)

// build_product's scripts interpolate ${graph.workflow_dir}, which dippin's
// binary probe cannot parse — it emits a DIP125 hint per node. Loading the
// built-in must not print those (or any other hint) on every run.
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
