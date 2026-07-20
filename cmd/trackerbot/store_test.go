// ABOUTME: Tests for the resume store and Runner.Resume from a checkpoint.
package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	tracker "github.com/2389-research/tracker"
)

func TestStore_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := openStore(path)
	s.put(RunRecord{ThreadTS: "T1", Channel: "C", Workflow: "quick"})
	s.put(RunRecord{ThreadTS: "T2", Channel: "C", Workflow: "build"})
	s.remove("T1")

	// Reload from disk — only T2 survives.
	reloaded := openStore(path)
	recs := reloaded.list()
	if len(recs) != 1 || recs[0].ThreadTS != "T2" || recs[0].Workflow != "build" {
		t.Fatalf("reloaded records = %+v", recs)
	}
}

func TestStore_NilSafe(t *testing.T) {
	var s *store
	s.put(RunRecord{ThreadTS: "x"}) // must not panic
	s.remove("x")
	if s.list() != nil {
		t.Fatal("nil store should list nothing")
	}
}

// TestRunner_Resume drives a resume from a real checkpoint. The runner pins a
// deterministic per-thread workdir + checkpoint; we seed a run there first, then
// Resume — the launch recomputes the same checkpoint path and the engine replays
// from it, finishing and posting the resume + done messages.
func TestRunner_Resume(t *testing.T) {
	wfDir := t.TempDir()
	writeWorkflow(t, wfDir, "quick.dip", quickDip)

	runsBase := t.TempDir()
	rm := tracker.NewRunManager()
	uis := newUIRegistry()
	r := NewRunner(rm, RunnerDeps{
		NewThreadUI: uis.newUI,
		WorkDir:     wfDir,
		RunsBase:    runsBase,
		NewID:       seqIDs(),
		ConfigBase:  tracker.Config{Format: "dip", LLMClient: stubCompleter{}},
		Store:       openStore(filepath.Join(t.TempDir(), "state.json")),
	})

	// Seed a checkpoint at the exact path the runner will recompute for thread T1.
	workDir, checkpoint := r.runPaths("T1")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.Run(context.Background(), quickDip, tracker.Config{
		Format:        "dip",
		WorkingDir:    workDir,
		CheckpointDir: checkpoint,
		LLMClient:     stubCompleter{},
	}); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	if _, err := os.Stat(checkpoint); err != nil {
		t.Fatalf("seed did not write a checkpoint: %v", err)
	}

	r.Resume(context.Background(), RunRecord{ThreadTS: "T1", Channel: "C", Workflow: "quick"})

	run, ok := rm.Get("T1")
	if !ok {
		t.Fatal("resumed run not tracked")
	}
	waitDone(t, run)
	waitForPost(t, uis.ui("T1"), "🔄", 3*time.Second) // resume acknowledgement
	waitForPost(t, uis.ui("T1"), "🏁", 3*time.Second) // completed
}
