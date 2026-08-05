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
	// Ran is false when the race pass was skipped (no Go module, no toolchain,
	// or the race detector is unavailable in this environment, e.g. no cgo) — a
	// skip is not a pass and not a failure.
	Ran bool `json:"ran"`
	// SkipReason explains why Ran is false (empty when Ran is true).
	SkipReason string `json:"skip_reason,omitempty"`
	// RaceDetected is true when the race detector fired (a genuine data race).
	RaceDetected bool `json:"race_detected"`
	// Passed is true when `go test -race ./...` exited 0 (tests green, no race).
	Passed bool `json:"passed"`
	// Output is the portion of the combined output most relevant to the result:
	// the race stanza when a race fired, otherwise the tail (capped).
	Output string `json:"output,omitempty"`
}

// raceMarker is the banner the Go race detector prints when it fires:
// "==================\nWARNING: DATA RACE". Anchoring on the full "WARNING: DATA
// RACE" phrase (not a bare "DATA RACE") avoids a false positive from a failing
// test whose own output happens to contain the words "data race".
const raceMarker = "WARNING: DATA RACE"

// raceOutputCap bounds the captured output so a large test log can't blow up a
// gate message.
const raceOutputCap = 16 * 1024

// DetectTestRaces runs `go test -race ./...` in dir and reports whether the race
// detector fired. It is intentionally opt-in (heavyweight: it compiles and runs
// the whole suite under the race detector). It returns Ran=false — a skip, not a
// failure — when there is no Go module under dir, no `go` toolchain, or the race
// detector cannot run in this environment (no cgo / unsupported platform), so a
// non-Go milestone or a cgo-less CI image does not spuriously fail the gate.
func DetectTestRaces(ctx context.Context, dir string) (*TestRaceReport, error) {
	if dir == "" {
		dir = "."
	}
	if !hasGoModule(dir) {
		return &TestRaceReport{Ran: false, SkipReason: "no go.mod or go.work under " + dir}, nil
	}
	if _, err := exec.LookPath("go"); err != nil {
		return &TestRaceReport{Ran: false, SkipReason: "go toolchain not found on PATH"}, nil
	}

	cmd := exec.CommandContext(ctx, "go", "test", "-race", "./...")
	cmd.Dir = dir
	out, runErr := cmd.CombinedOutput()
	s := string(out)
	raceDetected := strings.Contains(s, raceMarker)

	// Distinguish "the race detector could not run here" (environment limitation
	// → skip) from "tests genuinely fail / a race fired" (→ gate result). Only a
	// failed run with no race can be a toolchain-unavailable skip; a compile
	// error or plain test failure keeps Passed=false so the gate still fails.
	if runErr != nil && !raceDetected {
		if reason := raceUnavailableReason(s); reason != "" {
			return &TestRaceReport{Ran: false, SkipReason: "race detector unavailable: " + reason}, nil
		}
	}

	return &TestRaceReport{
		Ran:          true,
		RaceDetected: raceDetected,
		Passed:       runErr == nil,
		Output:       raceRelevantOutput(s, raceDetected),
	}, nil
}

// hasGoModule reports whether dir looks like a Go build root (a go.mod or a
// go.work). go.work covers multi-module workspaces that have no top-level go.mod.
func hasGoModule(dir string) bool {
	for _, marker := range []string{"go.mod", "go.work"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// raceUnavailableReason returns a short reason when the combined output shows the
// race detector could not run at all (as opposed to a real test/compile failure),
// or "" when the failure is a genuine gate signal. It matches only the specific
// toolchain-unavailable signatures the Go tool emits for -race.
func raceUnavailableReason(out string) string {
	groups := []struct {
		reason string
		anyOf  []string
	}{
		{"-race requires cgo (set CGO_ENABLED=1 with a C compiler installed)", []string{"-race requires cgo", "race requires cgo"}},
		{"race detector not supported on this platform", []string{"-race is only supported on", "race detector not supported"}},
		{"no C compiler for cgo (needed by -race)", []string{`exec: "gcc"`, `exec: "cc"`}},
	}
	for _, g := range groups {
		for _, sig := range g.anyOf {
			if strings.Contains(out, sig) {
				return g.reason
			}
		}
	}
	return ""
}

// raceRelevantOutput returns the slice of the output that explains the result.
// When a race fired, it starts at the detector's banner so the stanza is never
// truncated away by a pure tail; otherwise it returns the tail (recent failures
// and the final summary line live at the end).
func raceRelevantOutput(s string, raceDetected bool) string {
	if !raceDetected {
		return tailString(s, raceOutputCap)
	}
	i := strings.Index(s, raceMarker)
	if banner := strings.LastIndex(s[:i], "=================="); banner >= 0 {
		i = banner // back up to the "====" banner that precedes the WARNING line
	}
	seg := s[i:]
	if len(seg) > raceOutputCap {
		return seg[:raceOutputCap] + "\n…(truncated)…"
	}
	return seg
}

// tailString returns the last n bytes of s, prefixed with a truncation notice
// when it trims.
func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…(truncated)…\n" + s[len(s)-n:]
}
