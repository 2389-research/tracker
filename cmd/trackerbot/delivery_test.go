// ABOUTME: Tests that a finished run's outcome is delivered to its thread.
package main

import (
	"context"
	"testing"
	"time"
)

const quickDip = `workflow quick
  start: A
  exit: B

  agent A
    label: A

  agent B
    label: B

  edges
    A -> B
`

const failDip = `workflow failrun
  start: T
  exit: Done

  tool T
    command:
      exit 1

  agent Done
    label: Done

  edges
    T -> Done
`

func waitForPost(t *testing.T, ui *fakeUI, sub string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if lastContains(ui, sub) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no post containing %q; posts=%v", sub, fakePosts(ui))
}

func TestRunner_DeliversSuccess(t *testing.T) {
	workDir := t.TempDir()
	writeWorkflow(t, workDir, "quick.dip", quickDip)
	r, rm, uis := newTestRunner(t, workDir)

	r.OnMention(context.Background(), "C", "T1", "quick")
	run, ok := rm.Get("T1")
	if !ok {
		t.Fatal("run not tracked")
	}
	waitDone(t, run)
	waitForPost(t, uis.ui("T1"), "🏁", 3*time.Second)
}

func TestRunner_DeliversFailure(t *testing.T) {
	workDir := t.TempDir()
	writeWorkflow(t, workDir, "failrun.dip", failDip)
	r, rm, uis := newTestRunner(t, workDir)

	r.OnMention(context.Background(), "C", "T1", "failrun")
	run, ok := rm.Get("T1")
	if !ok {
		t.Fatal("run not tracked")
	}
	waitDone(t, run)
	// Either a real diagnosis or the terse fallback — both lead with ❌.
	waitForPost(t, uis.ui("T1"), "❌", 3*time.Second)
}
