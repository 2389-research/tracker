// ABOUTME: Tests the pre-run cost-confirmation gate (needsConfirm + confirmRun).
package chatops

import (
	"context"
	"testing"

	tracker "github.com/2389-research/tracker"
)

func TestRunner_NeedsConfirm(t *testing.T) {
	over := &Runner{deps: RunnerDeps{ConfirmOverUSD: 2.0}}
	if !over.needsConfirm(&tracker.RunEstimate{ExpectedUSD: 2.0}) {
		t.Error("expected 2.00 (>= threshold 2.00) to require confirmation")
	}
	if over.needsConfirm(&tracker.RunEstimate{ExpectedUSD: 1.0}) {
		t.Error("1.00 is below the threshold — no confirmation")
	}
	off := &Runner{deps: RunnerDeps{ConfirmOverUSD: 0}}
	if off.needsConfirm(&tracker.RunEstimate{ExpectedUSD: 100}) {
		t.Error("threshold 0 disables confirmation")
	}
}

func TestRunner_ConfirmRun(t *testing.T) {
	r := &Runner{deps: RunnerDeps{NewID: seqIDs()}, byThread: map[string]*ThreadInterviewer{}}
	ui := newFakeUI()
	est := &tracker.RunEstimate{ExpectedUSD: 3.0, HighUSD: 8.0}

	// Cancel → false, no run.
	res := make(chan bool, 1)
	go func() { res <- r.confirmRun(context.Background(), ui, "T1", est) }()
	g := awaitGate(t, ui)
	if g.Kind != GateChoice {
		t.Fatalf("confirm gate kind = %q, want choice", g.Kind)
	}
	r.OnInteraction("T1", g.ID, GateAnswer{Choice: "Cancel"})
	if <-res {
		t.Fatal("Cancel must return false (do not run)")
	}

	// Run it → true.
	go func() { res <- r.confirmRun(context.Background(), ui, "T1", est) }()
	g = awaitGate(t, ui)
	r.OnInteraction("T1", g.ID, GateAnswer{Choice: "Run it"})
	if !<-res {
		t.Fatal("Run it must return true (proceed)")
	}
}
