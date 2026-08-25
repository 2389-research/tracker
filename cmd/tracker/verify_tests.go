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

	// Advisory (#532 H1): structural near-duplicates never affect the exit code.
	printStructuralAdvisory(rep)

	// Advisory (#532 H2/H3): opt-in coverage-attribution pass, never gate-failing.
	if err := maybeVerifyCoverage(cfg, dir); err != nil {
		return err
	}

	raceErr := maybeVerifyRace(cfg, dir)

	// Report both checks; fail if either found a problem. Duplicates take
	// precedence in the returned error message since they were checked first.
	if dupCount > 0 {
		return fmt.Errorf("%d duplicate test group(s) found — see above", dupCount)
	}
	return raceErr
}

// maybeVerifyCoverage runs the opt-in coverage-attribution pass (#532 H2/H3) and
// prints its ADVISORY findings. It never returns a gate-failing error for a
// finding — coverage can't see subprocess/cgo execution, so the signal is
// reviewer-facing. It returns an error only when the pass itself cannot run.
func maybeVerifyCoverage(cfg runConfig, dir string) error {
	if !cfg.verifyCoverage {
		return nil
	}
	rep, err := tracker.AnalyzeTestReachability(context.Background(), dir)
	if err != nil {
		return fmt.Errorf("run coverage attribution: %w", err)
	}
	if !rep.Ran {
		fmt.Printf("• coverage check skipped: %s\n", rep.SkipReason)
		return nil
	}
	printReachabilityAdvisory(rep)
	return nil
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

// printStructuralAdvisory prints the #532 H1 structural near-duplicate findings.
// Advisory-only: it goes to stdout and never affects the exit code.
func printStructuralAdvisory(rep *tracker.TestFidelityReport) {
	if len(rep.StructuralGroups) == 0 {
		return
	}
	fmt.Printf("\n⚠ advisory — %d group(s) of tests are structural near-duplicates (reordered/renamed copies calling the same production symbols):\n", len(rep.StructuralGroups))
	for _, g := range rep.StructuralGroups {
		fmt.Printf("\n  • ~%.0f%% similar, shared calls %v:\n", g.Similarity*100, g.SharedCalls)
		for _, t := range g.Tests {
			fmt.Printf("      %s  (%s:%d)\n", t.Name, t.File, t.Line)
		}
	}
	fmt.Println("\n  Advisory only (does not fail the gate). Confirm each exercises a genuinely different path.")
}

// printReachabilityAdvisory prints the #532 H2/H3 coverage-attribution findings.
// All advisory — reviewer-facing evidence, never gate-failing.
func printReachabilityAdvisory(rep *tracker.TestReachabilityReport) {
	if len(rep.ZeroCoverage) == 0 && len(rep.Reimplemented) == 0 && len(rep.NameUnreached) == 0 {
		fmt.Printf("✓ coverage attribution: every non-skipped test reaches production code\n")
		return
	}
	printZeroCoverage(rep)
	printReimplemented(rep)
	printNameUnreached(rep)
	fmt.Println("\n  Advisory only (does not fail the gate). Coverage can't see subprocess/cgo execution — confirm each finding.")
}

func printZeroCoverage(rep *tracker.TestReachabilityReport) {
	if len(rep.ZeroCoverage) == 0 {
		return
	}
	fmt.Printf("\n⚠ advisory — %d test(s) executed ZERO production code (assert on a copy, or the path is guarded out):\n", len(rep.ZeroCoverage))
	for _, t := range rep.ZeroCoverage {
		fmt.Printf("      %s  (%s:%d)\n", t.Name, t.File, t.Line)
	}
}

func printReimplemented(rep *tracker.TestReachabilityReport) {
	if len(rep.Reimplemented) == 0 {
		return
	}
	fmt.Printf("\n⚠ advisory — %d test-local function(s) re-implement uncovered production logic:\n", len(rep.Reimplemented))
	for _, r := range rep.Reimplemented {
		fmt.Printf("      %s  (%s:%d) mirrors %s (%s:%d), ~%.0f%% similar\n",
			r.Helper.Name, r.Helper.File, r.Helper.Line, r.ProductionSymbol, r.ProductionFile, r.ProductionLine, r.Similarity*100)
	}
}

func printNameUnreached(rep *tracker.TestReachabilityReport) {
	if len(rep.NameUnreached) == 0 {
		return
	}
	fmt.Printf("\n⚠ advisory — %d test(s) named for a production symbol they never reach:\n", len(rep.NameUnreached))
	for _, n := range rep.NameUnreached {
		fmt.Printf("      %s names %q but never covers it  (%s:%d)\n", n.Test.Name, n.Symbol, n.Test.File, n.Test.Line)
	}
}
