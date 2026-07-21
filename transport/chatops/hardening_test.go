// ABOUTME: Tests the runner's workdir reaper and orphan sweep.
package chatops

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRunner_Reap asserts a finished run's workdir is reclaimed by default.
func TestRunner_Reap(t *testing.T) {
	runsBase := t.TempDir()
	r := &Runner{deps: RunnerDeps{RunsBase: runsBase}}
	wd, _ := r.runPaths("T1")
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, "artifact"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	r.reap("T1")

	if _, err := os.Stat(wd); !os.IsNotExist(err) {
		t.Fatalf("workdir should be reaped, stat err = %v", err)
	}
}

// TestRunner_ReapKeepsWorkdir asserts retention keeps artifacts but still drops
// the checkpoint so a later run in the thread starts fresh.
func TestRunner_ReapKeepsWorkdir(t *testing.T) {
	runsBase := t.TempDir()
	r := &Runner{deps: RunnerDeps{RunsBase: runsBase, KeepWorkdirs: true}}
	wd, cp := r.runPaths("T1")
	if err := os.MkdirAll(wd, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(cp, []byte("{}"), 0o600)
	_ = os.WriteFile(filepath.Join(wd, "artifact"), []byte("x"), 0o600)

	r.reap("T1")

	if _, err := os.Stat(cp); !os.IsNotExist(err) {
		t.Fatal("checkpoint should be dropped even under retention")
	}
	if _, err := os.Stat(filepath.Join(wd, "artifact")); err != nil {
		t.Fatalf("artifact should be kept under retention, got %v", err)
	}
}

// TestRunner_SweepOrphans keeps workdirs referenced by a live store record and
// removes the rest, without touching the (non-dir) state file.
func TestRunner_SweepOrphans(t *testing.T) {
	runsBase := t.TempDir()
	r := &Runner{deps: RunnerDeps{RunsBase: runsBase}}
	live, _ := r.runPaths("T1")
	orphan, _ := r.runPaths("T2")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(runsBase, "trackerbot-state.json")
	_ = os.WriteFile(stateFile, []byte("[]"), 0o600)

	r.SweepOrphans([]RunRecord{{ThreadTS: "T1"}})

	if _, err := os.Stat(live); err != nil {
		t.Fatalf("live workdir must be kept, got %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan workdir must be swept")
	}
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("state file must be preserved, got %v", err)
	}
}
