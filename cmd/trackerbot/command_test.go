// ABOUTME: Tests for the runner's control commands (help/status/cancel/runs).
package main

import (
	"context"
	"strings"
	"testing"
	"time"

	tracker "github.com/2389-research/tracker"
)

func fakePosts(ui *fakeUI) []string {
	ui.mu.Lock()
	defer ui.mu.Unlock()
	return append([]string(nil), ui.posts...)
}

func lastContains(ui *fakeUI, sub string) bool {
	for _, p := range fakePosts(ui) {
		if strings.Contains(p, sub) {
			return true
		}
	}
	return false
}

func TestRunner_HelpCommand(t *testing.T) {
	r, _, uis := newTestRunner(t, t.TempDir())
	r.OnMention(context.Background(), "C", "T1", "<@BOT> help")
	if !lastContains(uis.ui("T1"), "trackerbot") {
		t.Fatalf("help not posted: %v", fakePosts(uis.ui("T1")))
	}
	// An empty mention also yields help — and starts no run.
	r.OnMention(context.Background(), "C", "T2", "<@BOT>")
	if !lastContains(uis.ui("T2"), "trackerbot") {
		t.Fatal("empty mention should post help")
	}
}

func TestRunner_StatusAndCancel(t *testing.T) {
	workDir := t.TempDir()
	writeWorkflow(t, workDir, "gate.dip", gateDip)
	r, rm, uis := newTestRunner(t, workDir)

	r.OnMention(context.Background(), "C", "T1", "gate")
	ui := uis.ui("T1")
	awaitGate(t, ui) // run is active, blocked at its gate

	// status reports the run.
	r.OnMention(context.Background(), "C", "T1", "status")
	if !lastContains(ui, "running") && !lastContains(ui, "starting") {
		t.Fatalf("status not reported: %v", fakePosts(ui))
	}

	// cancel stops it.
	r.OnMention(context.Background(), "C", "T1", "cancel")
	if !lastContains(ui, "cancelling") {
		t.Fatalf("cancel not acknowledged: %v", fakePosts(ui))
	}
	run, _ := rm.Get("T1")
	select {
	case <-run.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("run did not stop after cancel")
	}
	if run.State() != tracker.RunCanceled {
		t.Fatalf("state after cancel = %s, want canceled", run.State())
	}
}

func TestRunner_RunsListAndUnknownStatus(t *testing.T) {
	r, _, uis := newTestRunner(t, t.TempDir())

	// No runs yet.
	r.OnMention(context.Background(), "C", "T0", "runs")
	if !lastContains(uis.ui("T0"), "No runs") {
		t.Fatalf("expected empty runs list: %v", fakePosts(uis.ui("T0")))
	}
	// status in a thread with no run.
	r.OnMention(context.Background(), "C", "T0", "status")
	if !lastContains(uis.ui("T0"), "No run in this thread") {
		t.Fatalf("expected no-run status: %v", fakePosts(uis.ui("T0")))
	}
}
