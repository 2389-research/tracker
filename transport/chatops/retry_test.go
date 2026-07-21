// ABOUTME: Tests the `retry` command re-runs a thread's last workflow.
package chatops

import (
	"context"
	"testing"
	"time"
)

func TestRunner_RetryNothingYet(t *testing.T) {
	workDir := t.TempDir()
	r, _, uis := newTestRunner(t, workDir)
	r.OnMention(context.Background(), "C", "T1", "retry")
	waitForPost(t, uis.ui("T1"), "Nothing to retry", time.Second)
}

func TestRunner_RetryReRunsLast(t *testing.T) {
	workDir := t.TempDir()
	writeWorkflow(t, workDir, "quick.dip", quickDip)
	r, rm, uis := newTestRunner(t, workDir)

	// First run.
	r.OnMention(context.Background(), "C", "T1", "quick")
	run, ok := rm.Get("T1")
	if !ok {
		t.Fatal("first run did not start")
	}
	waitDone(t, run)
	waitForForget(t, rm, "T1", 3*time.Second) // reaped → thread free

	// Retry re-runs the same workflow.
	r.OnMention(context.Background(), "C", "T1", "retry")
	waitForPost(t, uis.ui("T1"), "re-running", 2*time.Second)
	run2, ok := rm.Get("T1")
	if !ok {
		t.Fatal("retry did not start a run")
	}
	waitDone(t, run2)
}
