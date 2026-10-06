// ABOUTME: Eventually polls a condition until it holds or a deadline fails the test.
// ABOUTME: The failure carries the caller's message and the deadline, never a bare timeout.

package testutil

import (
	"testing"
	"time"
)

// Eventually polls cond every poll until it returns true. If deadline
// elapses first the test fails with msg and the deadline, so the record
// says WHAT never happened rather than just that something timed out.
func Eventually(t testing.TB, deadline, poll time.Duration, cond func() bool, msg string) {
	t.Helper()
	end := time.Now().Add(deadline)
	for {
		if cond() {
			return
		}
		if !time.Now().Before(end) {
			t.Fatalf("condition not met within %s: %s", deadline, msg)
			return
		}
		time.Sleep(poll)
	}
}
