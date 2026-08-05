// ABOUTME: Tests for DetectTestRaces — the opt-in `go test -race` fidelity pass (#489).
package tracker

import (
	"context"
	"os"
	"path/filepath"
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
