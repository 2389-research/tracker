// ABOUTME: Overlap drives one side of a concurrency test until every witness holds.
// ABOUTME: It terminates on witness-or-deadline, never on "the other side finished".

package testutil

import (
	"testing"
	"time"
)

// Overlap calls step repeatedly until it returns true (every witness the
// test needs has been observed) or deadline elapses. It reports whether
// the overlap was witnessed and how many steps ran.
//
// The caller decides what step does — typically one attempt of the racing
// operation plus a check of the counters — and what to do on a false
// return (fail with every counter printed). Overlap itself never fails the
// test: a deadline is a verdict for the caller to render with its own
// diagnostics, not a bare timeout.
func Overlap(t testing.TB, deadline time.Duration, step func() bool) (witnessed bool, iterations int) {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		iterations++
		if step() {
			return true, iterations
		}
	}
	return false, iterations
}
