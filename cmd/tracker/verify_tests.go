// ABOUTME: `tracker verify-tests <dir>` — flags duplicate/near-duplicate Go test
// ABOUTME: bodies so a verify gate can't bless copied "distinct" tests (#489).
package main

import (
	"fmt"
	"os"

	tracker "github.com/2389-research/tracker"
)

// executeVerifyTests scans a directory for duplicate test bodies and prints any
// findings. It returns a non-zero-exit error when duplicates are found, so a
// workflow's VerifyMilestone gate can `tracker verify-tests .` and fail the
// milestone on hollow/duplicated tests.
func executeVerifyTests(cfg runConfig) error {
	dir := cfg.pipelineFile // the positional arg (reused slot); default to cwd
	if dir == "" {
		dir = "."
	}
	rep, err := tracker.AnalyzeTestFidelity(dir)
	if err != nil {
		return fmt.Errorf("analyze test fidelity: %w", err)
	}
	if len(rep.DuplicateGroups) == 0 {
		fmt.Printf("✓ no duplicate test bodies found under %s\n", dir)
		return nil
	}
	printTestFidelity(rep)
	return fmt.Errorf("%d duplicate test group(s) found — see above", len(rep.DuplicateGroups))
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
