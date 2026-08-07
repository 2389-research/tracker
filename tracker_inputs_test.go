// ABOUTME: Tests for the public pipeline-inputs API — introspection, validation,
// ABOUTME: and bind-at-run-start fail-closed behavior (#553).
package tracker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

const inputsFileDip = `workflow SpecBuild
  goal: "x"
  start: Plan
  exit: Done
  inputs
    spec: file
  agent Plan
    label: p
    prompt:
      Read the spec at ${inputs.spec}.
  agent Done
    label: d
    prompt:
      done
  edges
    Plan -> Done
`

const inputsDip = `workflow IdeaToPR
  goal: "x"
  start: Plan
  exit: Done
  inputs
    idea: text
      required: true
      max_length: 4000
    risk: enum
      default: medium
      options: low, medium, high
  agent Plan
    label: "p"
    prompt:
      Build ${inputs.idea} at risk ${inputs.risk}.
  agent Done
    label: d
    prompt:
      done
  edges
    Plan -> Done
`

func TestDescribeInputs(t *testing.T) {
	specs, err := DescribeInputs(inputsDip, "dip")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("want 2 specs, got %d", len(specs))
	}
	if specs[0].Name != "idea" || !specs[0].Required || specs[0].MaxLength != 4000 {
		t.Fatalf("idea spec wrong: %+v", specs[0])
	}
	if specs[1].Name != "risk" || specs[1].Default != "medium" || len(specs[1].Options) != 3 {
		t.Fatalf("risk spec wrong: %+v", specs[1])
	}
}

// TestValidateSource_InputsRefsNotFlagged guards that inputs.* is treated as a
// runtime-produced ambient namespace: neither a ${inputs.x} interpolation nor a
// `when inputs.x` condition operand should warn about an undefined variable.
func TestValidateSource_InputsRefsNotFlagged(t *testing.T) {
	res, err := ValidateSource(inputsDip)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "inputs.") {
			t.Fatalf("inputs.* reference wrongly flagged: %s", w)
		}
	}
}

// TestBindInputs_ResumeSkipsMissingRequired guards resume: a required input that
// is not re-supplied must fail a FRESH run but NOT a resume (the checkpoint
// restores the original run's inputs and staged files persist in the run dir).
func TestBindInputs_ResumeSkipsMissingRequired(t *testing.T) {
	graph := &pipeline.Graph{Inputs: []pipeline.InputSpec{
		{Name: "idea", Kind: pipeline.InputText, Required: true},
		{Name: "n", Kind: pipeline.InputNumber},
	}}

	if _, err := bindInputs(graph, Config{}, t.TempDir()); err == nil {
		t.Fatal("fresh run with an unsatisfied required input should fail closed")
	}
	if _, err := bindInputs(graph, Config{ResumeRunID: "r1"}, t.TempDir()); err != nil {
		t.Fatalf("resume must not fail on an un-resupplied required input: %v", err)
	}
	// A re-supplied value with a genuine (non-missing) constraint violation still
	// fails on resume — only missing_required is tolerated.
	bad := Config{ResumeRunID: "r1", Inputs: []Input{StringInput("n", "not-a-number")}}
	if _, err := bindInputs(graph, bad, t.TempDir()); err == nil {
		t.Fatal("resume must still reject a re-supplied invalid value")
	}
}

func TestDescribeInputs_NoBlockIsEmpty(t *testing.T) {
	specs, err := DescribeInputs(quickDip, "dip")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if len(specs) != 0 {
		t.Fatalf("want no inputs for a workflow with no inputs block, got %v", specs)
	}
}

func TestValidateInputs_Public(t *testing.T) {
	specs, err := DescribeInputs(inputsDip, "dip")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	// Missing required "idea" and an out-of-set risk both reported.
	errs := ValidateInputs(specs, []Input{StringInput("risk", "extreme")})
	if len(errs) == 0 {
		t.Fatal("expected validation errors")
	}
	// Valid set passes.
	if errs := ValidateInputs(specs, []Input{StringInput("idea", "ship"), StringInput("risk", "low")}); len(errs) != 0 {
		t.Fatalf("expected valid, got %v", errs)
	}
}

// TestRun_FailsClosedOnMissingRequiredInput asserts a run with an unsatisfied
// required input never executes a node — it fails at construction with a typed
// InputValidationError rather than expanding ${inputs.idea} to empty string.
func TestRun_FailsClosedOnMissingRequiredInput(t *testing.T) {
	_, err := NewEngineWithContext(context.Background(), inputsDip, Config{
		Format:    "dip",
		LLMClient: successStub(),
		Inputs:    []Input{StringInput("risk", "low")}, // "idea" (required) omitted
	})
	if err == nil {
		t.Fatal("expected fail-closed error for missing required input")
	}
	var ive *InputValidationError
	if !errors.As(err, &ive) {
		t.Fatalf("want *InputValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "idea") {
		t.Fatalf("error should name the missing input: %v", err)
	}
}

// TestRun_StagesFileInputToFixedPath asserts a file input supplied as inline
// bytes is staged to <workDir>/.tracker/inputs/<name> (the fixed path a
// workflow's shell reads) and exposed as its relative path in the context.
func TestRun_StagesFileInputToFixedPath(t *testing.T) {
	workDir := t.TempDir()
	res, err := Run(context.Background(), inputsFileDip, Config{
		Format:     "dip",
		WorkingDir: workDir,
		LLMClient:  successStub(),
		Inputs:     []Input{FileInputBytes("spec", []byte("build a widget"))},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	staged := filepath.Join(workDir, ".tracker", "inputs", "spec")
	got, rerr := os.ReadFile(staged)
	if rerr != nil {
		t.Fatalf("staged spec not written: %v", rerr)
	}
	if string(got) != "build a widget" {
		t.Fatalf("staged contents = %q", got)
	}
	if res.Context["inputs.spec"] != ".tracker/inputs/spec" {
		t.Fatalf("inputs.spec = %q, want the relative staged path", res.Context["inputs.spec"])
	}
}

// TestRun_BindsInputsIntoContext asserts a valid input set is seeded under the
// inputs. prefix so ${inputs.name} resolves during the run.
func TestRun_BindsInputsIntoContext(t *testing.T) {
	res, err := Run(context.Background(), inputsDip, Config{
		Format:     "dip",
		WorkingDir: t.TempDir(),
		LLMClient:  successStub(),
		Inputs:     []Input{StringInput("idea", "ship it"), StringInput("risk", "high")},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := res.Context["inputs.idea"]; got != "ship it" {
		t.Fatalf("inputs.idea in context = %q, want %q", got, "ship it")
	}
	if got := res.Context["inputs.risk"]; got != "high" {
		t.Fatalf("inputs.risk in context = %q, want %q", got, "high")
	}
}
