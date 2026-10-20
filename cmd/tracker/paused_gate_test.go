// ABOUTME: Tests the CLI side of a gate pause: exit code 4, the paused summary
// ABOUTME: with its answer command, and the invocation record tracker answer reuses.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func TestInterpretRunResult_PausedGate(t *testing.T) {
	res := &pipeline.EngineResult{RunID: "r1", Status: pipeline.OutcomePausedGate}
	err := interpretRunResult(res, pipeline.NewPausedGateError(pipeline.PausedGate{NodeID: "G"}), &runConfig{})
	if !errors.Is(err, errPausedAtGate) {
		t.Fatalf("err = %v, want errPausedAtGate", err)
	}
	billing := &pipeline.EngineResult{RunID: "r2", Status: pipeline.OutcomePausedBilling}
	if err := interpretRunResult(billing, errors.New("credit balance too low"), &runConfig{}); errors.Is(err, errPausedAtGate) {
		t.Error("paused_billing must keep its failure path (exit 1)")
	}
}

func TestExitCode_PausedGateIs4(t *testing.T) {
	cases := map[error]int{
		errPausedAtGate: 4,
		fmt.Errorf("wrapped: %w", errPausedAtGate): 4,
		pipeline.ErrValidationOverridden:           2,
		&DoctorWarningsError{}:                     2,
		errors.New("pipeline execution: boom"):     1,
	}
	for err, want := range cases {
		if got := exitCodeFor(err); got != want {
			t.Errorf("exitCodeFor(%v) = %d, want %d", err, got, want)
		}
	}
}

func pausedResult() *pipeline.EngineResult {
	return &pipeline.EngineResult{
		RunID:  "5d82a6d50f15",
		Status: pipeline.OutcomePausedGate,
		PausedGates: []pipeline.PausedGate{{
			NodeID: "ReviewDiff", Mode: "freeform", Label: "Review the change",
			Prompt: "Review the change\n\n## Diff\n+fixed typo", Choices: []string{"reject", "revise", "approve"}, Default: "reject",
		}},
	}
}

func TestSummary_PausedGate(t *testing.T) {
	out := captureStdout(t, func() {
		printRunSummary(pausedResult(), errPausedAtGate, runHints{resume: "x.dip", answerFlags: "-w /x"}, nil)
	})
	for _, want := range []string{"⏸ Paused at gate ReviewDiff", "Review the change", "options: reject (default) · revise · approve · or free text", "## Diff", "+fixed typo"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
	for _, not := range []string{"tracker -r", "Pipeline failed", "✗"} {
		if strings.Contains(out, not) {
			t.Errorf("summary contains %q:\n%s", not, out)
		}
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	last := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" && !strings.HasPrefix(l, "═") {
			last = l
			break
		}
	}
	if !strings.HasPrefix(last, "tracker answer -w /x 5d82a6d50f15 reject") {
		t.Errorf("last line = %q, want the answer command", last)
	}
}

func TestInvocation_RoundTrip(t *testing.T) {
	t.Setenv("TRACKER_AUDIT_DIR", t.TempDir())
	rec := invocationRecord{Version: 1, Cwd: "/w", Args: []string{"quickfix", "--param", "change=x"}, Digest: "abc"}
	if err := writeInvocation("run1", rec); err != nil {
		t.Fatal(err)
	}
	path, _ := pipeline.SecureInvocationPath("run1")
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("invocation file: %v, %v", info, err)
	}
	if filepath.Base(path) != "invocation.json" {
		t.Errorf("path = %s", path)
	}
	got, err := readInvocation("run1")
	if err != nil || got.Cwd != rec.Cwd || !slices.Equal(got.Args, rec.Args) || got.Digest != rec.Digest {
		t.Fatalf("read back %+v, %v", got, err)
	}
}

func TestStripResumeArgs(t *testing.T) {
	in := []string{"-r", "abc", "--resume=abc", "--from", "N", "--resume-no-rewind", "--json", "quickfix", "--param", "change=-r"}
	want := []string{"--json", "quickfix", "--param", "change=-r"}
	if got := stripResumeArgs(in); !slices.Equal(got, want) {
		t.Errorf("stripResumeArgs = %v, want %v", got, want)
	}
}

// A paused run with no invocation record (started by a program using the
// library, or the process died before recording it) gets a way forward.
func TestReadInvocation_MissingNamesRecovery(t *testing.T) {
	t.Setenv("TRACKER_AUDIT_DIR", t.TempDir())
	_, err := readInvocation("nosuchrun")
	if err == nil || !strings.Contains(err.Error(), "tracker -r nosuchrun") || !strings.Contains(err.Error(), "tracker.Answer") {
		t.Fatalf("err = %v, want the -r and library recovery paths", err)
	}
}
