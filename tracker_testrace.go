// ABOUTME: Test-race analysis — runs the Go race detector so a verify gate can't
// ABOUTME: bless tests that pass only by timing luck and fail under -race (#489).
package tracker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// TestRaceReport is the outcome of running the Go race detector over a tree.
type TestRaceReport struct {
	// Ran is false when the race pass was skipped (no Go toolchain or no
	// go.mod under dir) — a skip is not a pass and not a failure.
	Ran bool `json:"ran"`
	// SkipReason explains why Ran is false (empty when Ran is true).
	SkipReason string `json:"skip_reason,omitempty"`
	// RaceDetected is true when the race detector fired (a genuine data race).
	RaceDetected bool `json:"race_detected"`
	// Passed is true when `go test -race ./...` exited 0 (tests green, no race).
	Passed bool `json:"passed"`
	// Output is the tail of the combined build/test output (capped).
	Output string `json:"output,omitempty"`
}

// raceMarker is the string the Go race detector prints when it fires. It appears
// as "==================\nWARNING: DATA RACE" — matching "DATA RACE" is enough.
const raceMarker = "DATA RACE"

// raceOutputCap bounds the captured output tail so a large test log can't blow up
// a gate message. Race reports are near the end, so the tail is the useful part.
const raceOutputCap = 16 * 1024

// DetectTestRaces runs `go test -race ./...` in dir and reports whether the race
// detector fired. It is intentionally opt-in (heavyweight: it compiles and runs
// the whole suite under the race detector). When dir has no go.mod or the `go`
// toolchain is unavailable, it returns Ran=false rather than an error — a
// non-Go milestone simply has no Go race pass to run.
func DetectTestRaces(ctx context.Context, dir string) (*TestRaceReport, error) {
	if dir == "" {
		dir = "."
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return &TestRaceReport{Ran: false, SkipReason: "no go.mod under " + dir}, nil
	}
	if _, err := exec.LookPath("go"); err != nil {
		return &TestRaceReport{Ran: false, SkipReason: "go toolchain not found on PATH"}, nil
	}

	cmd := exec.CommandContext(ctx, "go", "test", "-race", "./...")
	cmd.Dir = dir
	out, runErr := cmd.CombinedOutput()

	report := &TestRaceReport{
		Ran:          true,
		RaceDetected: strings.Contains(string(out), raceMarker),
		Passed:       runErr == nil,
		Output:       tailString(string(out), raceOutputCap),
	}
	return report, nil
}

// tailString returns the last n bytes of s, prefixed with a truncation notice
// when it trims. Kept local so the race pass has no cross-file coupling.
func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…(truncated)…\n" + s[len(s)-n:]
}
