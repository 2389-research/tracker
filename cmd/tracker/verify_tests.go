// ABOUTME: `tracker verify-tests <dir>` — flags duplicate/near-duplicate Go test
// ABOUTME: bodies so a verify gate can't bless copied "distinct" tests (#489).
package main

import (
	"context"
	"fmt"
	"os"

	tracker "github.com/2389-research/tracker"
)

// executeVerifyTests scans a directory for duplicate test bodies (and, with
// --race, runs the Go race detector) and prints any findings. It returns a
// non-zero-exit error when a fidelity problem is found, so a workflow's
// VerifyMilestone gate can `tracker verify-tests .` (optionally `--race`) and
// fail the milestone on hollow/duplicated/racy tests.
func executeVerifyTests(cfg runConfig) error {
	dir := cfg.pipelineFile // the positional arg (reused slot); default to cwd
	if dir == "" {
		dir = "."
	}
	rep, err := tracker.AnalyzeTestFidelity(dir)
	if err != nil {
		return fmt.Errorf("analyze test fidelity: %w", err)
	}
	dupCount := len(rep.DuplicateGroups)
	if dupCount == 0 {
		fmt.Printf("✓ no duplicate test bodies found under %s\n", dir)
	} else {
		printTestFidelity(rep)
	}

	raceErr := maybeVerifyRace(cfg, dir)

	// Report both checks; fail if either found a problem. Duplicates take
	// precedence in the returned error message since they were checked first.
	if dupCount > 0 {
		return fmt.Errorf("%d duplicate test group(s) found — see above", dupCount)
	}
	return raceErr
}

// maybeVerifyRace runs the opt-in `go test -race` pass and prints its result.
// Returns a non-nil error when a race is detected or the suite fails under the
// race detector; nil when the pass is clean or skipped (non-Go milestone).
func maybeVerifyRace(cfg runConfig, dir string) error {
	if !cfg.verifyRace {
		return nil
	}
	race, err := tracker.DetectTestRaces(context.Background(), dir)
	if err != nil {
		return fmt.Errorf("run race detector: %w", err)
	}
	if !race.Ran {
		fmt.Printf("• race check skipped: %s\n", race.SkipReason)
		return nil
	}
	if race.RaceDetected {
		fmt.Fprintf(os.Stderr, "✗ test-fidelity: the Go race detector fired — a test passes only by timing luck:\n\n%s\n", race.Output)
		return fmt.Errorf("data race detected under `go test -race` — see above")
	}
	if !race.Passed {
		fmt.Fprintf(os.Stderr, "✗ tests do not pass under `go test -race ./...`:\n\n%s\n", race.Output)
		return fmt.Errorf("`go test -race` failed — see above")
	}
	fmt.Printf("✓ no data races under `go test -race ./...`\n")
	return nil
}

func printTestFidelity(rep *tracker.TestFidelityReport) {
	fmt.Fprintf(os.Stderr, "✗ test-fidelity: %d group(s) of tests share a body — likely copy-paste, not distinct coverage:\n", len(rep.DuplicateGroups))
	for _, g := range rep.DuplicateGroups {
		label := "byte-for-byte identical"
		if g.Kind == "near-identical" {
			label = "identical except for literal values"
		}
		fmt.Fprintf(os.Stderr, "\n  • %s:\n", label)
		for _, t := range g.Tests {
			fmt.Fprintf(os.Stderr, "      %s  (%s:%d)\n", t.Name, t.File, t.Line)
		}
	}
	fmt.Fprintln(os.Stderr, "\n  A required/distinct test that copies another's body gives false coverage credit.")
	fmt.Fprintln(os.Stderr, "  Make each test exercise a genuinely different path, or drop the duplicate.")
}
