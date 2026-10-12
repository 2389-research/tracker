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
	runs := recordRuns(r)

	// First run.
	r.OnMention(context.Background(), "C", "T1", "quick")
	waitDone(t, runs.nth(t, "T1", 0))
	waitForForget(t, rm, "T1", 3*time.Second) // reaped → thread free

	// Retry re-runs the same workflow.
	r.OnMention(context.Background(), "C", "T1", "retry")
	waitForPost(t, uis.ui("T1"), "re-running", 2*time.Second)
	waitDone(t, runs.nth(t, "T1", 1)) // the retry's own run, not the first
}
