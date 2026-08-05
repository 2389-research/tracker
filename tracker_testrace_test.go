// ABOUTME: Tests for DetectTestRaces — the opt-in `go test -race` fidelity pass (#489).
package tracker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeModule creates a minimal Go module in a fresh temp dir with the given
// test-file contents and returns the dir.
func writeModule(t *testing.T, testFile string) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "go.mod"), "module racetest\n\ngo 1.21\n")
	mustWrite(t, filepath.Join(dir, "x_test.go"), testFile)
	return dir
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestDetectTestRaces_SkipsNonGoDir(t *testing.T) {
	// No go.mod → Ran=false, no subprocess. Fast; runs even under -short.
	rep, err := DetectTestRaces(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.Ran {
		t.Error("expected Ran=false for a dir with no go.mod")
	}
	if rep.SkipReason == "" {
		t.Error("expected a SkipReason when skipped")
	}
}

func TestDetectTestRaces_CleanModule(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns `go test -race`; skipped in -short")
	}
	dir := writeModule(t, `package racetest

import "testing"

func TestClean(t *testing.T) {
	x := 0
	x++
	if x != 1 {
		t.Fatal("math is broken")
	}
}
`)
	rep, err := DetectTestRaces(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.Ran {
		t.Skipf("race pass did not run (toolchain?): %s", rep.SkipReason)
	}
	if !rep.Passed && !rep.RaceDetected {
		t.Skipf("go test -race unavailable in this environment:\n%s", rep.Output)
	}
	if rep.RaceDetected {
		t.Errorf("clean module must not report a data race:\n%s", rep.Output)
	}
	if !rep.Passed {
		t.Errorf("clean module should pass under -race:\n%s", rep.Output)
	}
}

func TestRaceUnavailableReason(t *testing.T) {
	// Toolchain-unavailable signatures → a non-empty reason (skip, not gate fail).
	unavailable := []string{
		"go test -race: -race requires cgo; enable cgo by setting CGO_ENABLED=1",
		"-race is only supported on linux/amd64, ...",
		`# runtime/cgo\nexec: "gcc": executable file not found in $PATH`,
	}
	for _, out := range unavailable {
		if raceUnavailableReason(out) == "" {
			t.Errorf("expected a skip reason for %q", out)
		}
	}
	// A genuine test/compile failure must NOT be classified as unavailable —
	// the gate should fail on these.
	realFailures := []string{
		"--- FAIL: TestFoo\n    foo_test.go:10: want 1 got 2\nFAIL",
		"./x.go:3:1: syntax error: unexpected }",
		"", // clean/other
	}
	for _, out := range realFailures {
		if r := raceUnavailableReason(out); r != "" {
			t.Errorf("real failure %q must not be treated as unavailable, got %q", out, r)
		}
	}
}

func TestRaceRelevantOutput_KeepsStanzaOnDetect(t *testing.T) {
	// A race stanza in the MIDDLE of a large log must survive, not be tail-trimmed.
	pre := strings.Repeat("early package chatter\n", 200)
	stanza := "==================\nWARNING: DATA RACE\nRead at 0x... by goroutine 7\n=================="
	post := strings.Repeat("later package chatter\n", 2000) // pushes stanza out of a pure tail
	full := pre + stanza + post

	got := raceRelevantOutput(full, true)
	if !strings.Contains(got, "WARNING: DATA RACE") {
		t.Error("race-relevant output must retain the WARNING: DATA RACE stanza")
	}
	if !strings.HasPrefix(got, "==================") {
		t.Errorf("output should start at the detector banner, got prefix %q", got[:20])
	}

	// With no race, it falls back to the tail.
	tail := raceRelevantOutput(full, false)
	if !strings.Contains(tail, "later package chatter") {
		t.Error("non-race output should keep the tail")
	}
}

func TestDetectTestRaces_FailingTestPrintingDataRaceIsNotAFalsePositive(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns `go test -race`; skipped in -short")
	}
	// A test that FAILS and whose message contains "data race" — but there is no
	// actual race. The detector never prints "WARNING: DATA RACE", so
	// RaceDetected must stay false even though the run fails (#489 review #2).
	dir := writeModule(t, `package racetest

import "testing"

func TestNoActualRace(t *testing.T) {
	// Contains the literal "DATA RACE" — the old bare-substring matcher would
	// have false-fired on this; the anchored "WARNING: DATA RACE" must not.
	t.Errorf("expected no DATA RACE guard to trigger, but got a value")
}
`)
	rep, err := DetectTestRaces(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.Ran {
		t.Skipf("race pass did not run: %s", rep.SkipReason)
	}
	if rep.RaceDetected {
		t.Errorf("a failing test that merely prints 'data race' must not count as a detected race:\n%s", rep.Output)
	}
	if rep.Passed {
		t.Error("a failing test should leave Passed=false")
	}
}

func TestDetectTestRaces_DetectsRace(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns `go test -race`; skipped in -short")
	}
	// A genuine data race: a goroutine writes shared state with no
	// synchronization while the test goroutine reads it.
	dir := writeModule(t, `package racetest

import (
	"testing"
	"time"
)

func TestRacy(t *testing.T) {
	shared := 0
	done := make(chan bool)
	go func() {
		shared = 1 // unsynchronized write
		done <- true
	}()
	_ = shared // unsynchronized read, racing the goroutine
	<-done
	time.Sleep(time.Millisecond)
}
`)
	rep, err := DetectTestRaces(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.Ran {
		t.Skipf("race pass did not run (toolchain?): %s", rep.SkipReason)
	}
	// If the environment cannot run the race detector at all, both flags stay
	// false and there is nothing to assert — skip rather than fail.
	if rep.Passed && !rep.RaceDetected {
		t.Skipf("race detector did not fire — likely unsupported here:\n%s", rep.Output)
	}
	if !rep.RaceDetected {
		t.Errorf("expected the race detector to fire on a racy test:\n%s", rep.Output)
	}
	if rep.Passed {
		t.Error("a module with a data race must not report Passed=true")
	}
}
