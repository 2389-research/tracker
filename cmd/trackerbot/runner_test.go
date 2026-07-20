// ABOUTME: Tests the Runner's thread→run routing, including concurrent independence.
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tracker "github.com/2389-research/tracker"
)

// uiRegistry hands out a fresh fakeUI per thread and remembers them by thread_ts.
type uiRegistry struct {
	mu sync.Mutex
	m  map[string]*fakeUI
}

func newUIRegistry() *uiRegistry { return &uiRegistry{m: map[string]*fakeUI{}} }

func (u *uiRegistry) newUI(_ /*channel*/, threadTS string) ThreadUI {
	u.mu.Lock()
	defer u.mu.Unlock()
	if f := u.m[threadTS]; f != nil {
		return f // one thread = one UI
	}
	f := newFakeUI()
	u.m[threadTS] = f
	return f
}

func (u *uiRegistry) ui(threadTS string) *fakeUI {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.m[threadTS]
}

func writeWorkflow(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
}

func newTestRunner(t *testing.T, workDir string) (*Runner, *tracker.RunManager, *uiRegistry) {
	t.Helper()
	rm := tracker.NewRunManager()
	uis := newUIRegistry()
	r := NewRunner(rm, RunnerDeps{
		NewThreadUI: uis.newUI,
		WorkDir:     workDir,
		RunsBase:    t.TempDir(),
		NewID:       seqIDs(),
		ConfigBase:  tracker.Config{Format: "dip", LLMClient: stubCompleter{}},
	})
	return r, rm, uis
}

func waitDone(t *testing.T, run *tracker.ManagedRun) {
	t.Helper()
	select {
	case <-run.Done():
	case <-time.After(5 * time.Second):
		t.Fatalf("run %q did not finish", run.Key)
	}
}

// TestRunner_RoutesGateAndDelivers: a mention starts a gated run, the gate posts
// to the thread, OnInteraction resolves it, and the run finishes.
func TestRunner_RoutesGateAndDelivers(t *testing.T) {
	workDir := t.TempDir()
	writeWorkflow(t, workDir, "gate.dip", gateDip)
	r, rm, uis := newTestRunner(t, workDir)

	r.OnMention(context.Background(), "C1", "T1", "gate")
	ui := uis.ui("T1")
	if ui == nil {
		t.Fatal("no UI created for thread T1")
	}
	g := awaitGate(t, ui) // the run blocked at its human gate

	if !r.OnInteraction("T1", g.ID, GateAnswer{Freeform: "go"}) {
		t.Fatal("OnInteraction did not resolve the pending gate")
	}

	run, ok := rm.Get("T1")
	if !ok {
		t.Fatal("run T1 not tracked")
	}
	waitDone(t, run)
	if run.State() != tracker.RunSucceeded {
		t.Fatalf("run state = %s, want succeeded", run.State())
	}
}

// TestRunner_ConcurrentThreadsRouteIndependently proves inbound routing: two runs
// block at gates simultaneously, and each thread's answer reaches ITS run — not
// the other's.
func TestRunner_ConcurrentThreadsRouteIndependently(t *testing.T) {
	workDir := t.TempDir()
	writeWorkflow(t, workDir, "gate.dip", gateDip)
	r, rm, uis := newTestRunner(t, workDir)

	r.OnMention(context.Background(), "C", "T1", "gate")
	r.OnMention(context.Background(), "C", "T2", "gate")

	g1 := awaitGate(t, uis.ui("T1"))
	g2 := awaitGate(t, uis.ui("T2"))

	// Answer T2 first, then T1 — with distinct answers.
	r.OnInteraction("T2", g2.ID, GateAnswer{Freeform: "answer-two"})
	r.OnInteraction("T1", g1.ID, GateAnswer{Freeform: "answer-one"})

	run1, _ := rm.Get("T1")
	run2, _ := rm.Get("T2")
	waitDone(t, run1)
	waitDone(t, run2)

	res1, _ := run1.Result()
	res2, _ := run2.Result()
	if got := res1.Context["human_response"]; !strings.Contains(got, "answer-one") {
		t.Fatalf("T1 human_response = %q, want answer-one (routing crossed!)", got)
	}
	if got := res2.Context["human_response"]; !strings.Contains(got, "answer-two") {
		t.Fatalf("T2 human_response = %q, want answer-two (routing crossed!)", got)
	}
}

// TestRunner_DuplicateThreadRejected: a second mention in an active thread is
// rejected (ErrRunKeyActive), surfaced to the thread.
func TestRunner_DuplicateThreadRejected(t *testing.T) {
	workDir := t.TempDir()
	writeWorkflow(t, workDir, "gate.dip", gateDip)
	r, rm, uis := newTestRunner(t, workDir)

	r.OnMention(context.Background(), "C", "T1", "gate")
	ui := uis.ui("T1")
	g := awaitGate(t, ui) // first run is active, blocked at its gate

	r.OnMention(context.Background(), "C", "T1", "gate") // duplicate
	found := false
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		ui.mu.Lock()
		for _, p := range ui.posts {
			if strings.Contains(p, "already active") {
				found = true
			}
		}
		ui.mu.Unlock()
		if found {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !found {
		t.Fatal("expected an 'already active' rejection message")
	}

	// Clean up: resolve the captured gate so the run finishes.
	r.OnInteraction("T1", g.ID, GateAnswer{Freeform: "go"})
	run, _ := rm.Get("T1")
	waitDone(t, run)
}
