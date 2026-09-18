// ABOUTME: Pins that the three shipped built-ins load DIP125-clean under the pinned
// ABOUTME: dippin-lang, and that the CLI load path prints every diagnostic severity.
package main

import (
	"strings"
	"testing"

	"github.com/2389-research/dippin-lang/validator"
	"github.com/2389-research/tracker/pipeline"
)

// TestLoadEmbeddedBuiltins_NoDIP125Hints pins the dippin-lang#315 fix
// (v0.76.0): DIP125's PATH probe now knows the `:` special builtin and skips
// functions loaded via `.`/`source`, so the shell functions build_product's
// scripts source from lib/ are no longer reported as missing binaries. Before
// the fix the three built-ins produced 25 bogus DIP125 hints (10 / 14 / 1)
// and the CLI load path hid every hint to keep them off the console; that
// filter is gone, so a regression here would print on every run. The only
// hint a built-in may still emit is build_product's accurate DIP165
// (FinalCommit's deliberate `writable_paths_mode: prefer`, #648).
func TestLoadEmbeddedBuiltins_NoDIP125Hints(t *testing.T) {
	for _, name := range []string{"build_product", "build_product_with_superspec", "ask_and_execute"} {
		t.Run(name, func(t *testing.T) {
			info, ok := lookupBuiltinWorkflow(name)
			if !ok {
				t.Fatalf("%s not embedded", name)
			}
			out := captureStderr(t, func() {
				if _, err := loadEmbeddedPipeline(info); err != nil {
					t.Errorf("load: %v", err)
				}
			})
			if strings.Contains(out, "DIP125") {
				t.Errorf("dippin-lang#315 regressed — DIP125 hints on the built-in load path:\n%s", out)
			}
			for _, line := range strings.Split(out, "\n") {
				if strings.HasPrefix(line, "hint[") && !strings.HasPrefix(line, "hint[DIP165]") {
					t.Errorf("unexpected hint on the built-in load path: %s", line)
				}
			}
		})
	}
}

func TestPrintLoadDiagnostics_PrintsEverySeverity(t *testing.T) {
	diags := []validator.Diagnostic{
		{Code: "DIP165", Severity: validator.SeverityHint, Message: "a hint"},
		{Code: "DIP102", Severity: validator.SeverityWarning, Message: "a warning"},
		{Code: "DIP001", Severity: validator.SeverityError, Message: "an error"},
	}
	out := captureStderr(t, func() { printLoadDiagnostics(diags) })
	for _, want := range []string{"a hint", "a warning", "an error"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from:\n%s", want, out)
		}
	}
}

// TestDIP125_PlaceholderNoLongerMisreadAsBinary pins the dippin-lang#305 fix
// (v0.74.0): a `set -eu` body that interpolates `${graph.workflow_dir}` used
// to report the bogus binary "-eu"; the probe now substitutes the placeholder
// and walks to the real first command. Nothing hides a regression from users
// any more — printLoadDiagnostics prints hints.
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

// TestDIP125_SourcedFunctionAndColonBuiltinNotFlagged pins dippin-lang#315
// (v0.76.0) directly: a body that sources a lib file, calls a function from
// it, and uses the `:` builtin must not produce DIP125 hints.
func TestDIP125_SourcedFunctionAndColonBuiltinNotFlagged(t *testing.T) {
	src := "workflow Repro\n  goal: \"dip125\"\n  start: A\n  exit: B\n\n  tool A\n    command:\n      set -eu\n      . \"${graph.workflow_dir}/scripts/lib/gitignore.sh\"\n      : \"${FOO:=bar}\"\n      ensure_gitignore_entry \".ai/\"\n\n  tool B\n    command: \"true\"\n\n  edges\n    A -> B\n"
	_, diags, err := pipeline.LoadDippinWorkflow(src, "repro.dip")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, d := range diags {
		if d.Code == "DIP125" {
			t.Fatalf("dippin-lang#315 regressed — DIP125 flagged a sourced function or the `:` builtin: %s", d.String())
		}
	}
}
