// ABOUTME: Tests for the concurrency-witness helpers (Witness, Overlap, Eventually).
// ABOUTME: Each helper must fail LOUDLY when the interaction it guards was not seen.

package testutil

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// recordTB captures Fatalf/Errorf calls instead of ending the test, so the
// helpers' failure paths can be asserted. It embeds the real *testing.T for
// Helper() and the unexported testing.TB marker method.
type recordTB struct {
	testing.TB
	fatals []string
	errs   []string
}

func (r *recordTB) Fatalf(format string, args ...any) {
	r.fatals = append(r.fatals, fmt.Sprintf(format, args...))
}

func (r *recordTB) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func TestWitness_CountsAndString(t *testing.T) {
	w := NewWitness("ok", "rejected", "flips")
	w.Hit("ok")
	w.Hit("ok")
	w.Add("flips", 20)
	w.Hit("unexpected") // a side not declared up front is still counted and printed

	if got := w.Count("ok"); got != 2 {
		t.Errorf("Count(ok) = %d, want 2", got)
	}
	if got := w.Count("rejected"); got != 0 {
		t.Errorf("Count(rejected) = %d, want 0", got)
	}
	if got := w.Count("never"); got != 0 {
		t.Errorf("Count(never) = %d, want 0 for an unknown side", got)
	}
	s := w.String()
	// Declared order first, then undeclared sides; every declared side is
	// printed even at zero so a failure message shows the whole picture.
	want := "ok=2 rejected=0 flips=20 unexpected=1"
	if s != want {
		t.Errorf("String() = %q, want %q", s, want)
	}
}

func TestWitness_Require_FailsWhenASideIsNeverSeen(t *testing.T) {
	w := NewWitness("ok", "rejected")
	w.Hit("ok")
	rec := &recordTB{TB: t}

	w.Require(rec, 1, "ok", "rejected")

	if len(rec.fatals) != 1 {
		t.Fatalf("Require with an unseen side: %d Fatalf calls, want 1 (errs=%v)", len(rec.fatals), rec.errs)
	}
	msg := rec.fatals[0]
	for _, needle := range []string{"rejected", "ok=1 rejected=0"} {
		if !strings.Contains(msg, needle) {
			t.Errorf("Require failure %q does not mention %q", msg, needle)
		}
	}
	if strings.Contains(msg, "\"ok\"") {
		t.Errorf("Require failure %q names the side that WAS seen as missing", msg)
	}
}

func TestWitness_Require_FailsBelowMinimum(t *testing.T) {
	w := NewWitness("flips")
	w.Add("flips", 3)
	rec := &recordTB{TB: t}

	w.Require(rec, 20, "flips")

	if len(rec.fatals) != 1 {
		t.Fatalf("Require below min: %d Fatalf calls, want 1", len(rec.fatals))
	}
	if !strings.Contains(rec.fatals[0], "flips=3") || !strings.Contains(rec.fatals[0], "20") {
		t.Errorf("Require failure %q should print the count and the minimum", rec.fatals[0])
	}
}

func TestWitness_Require_PassesWhenAllSidesSeen(t *testing.T) {
	w := NewWitness("ok", "rejected")
	w.Hit("ok")
	w.Add("rejected", 5)
	rec := &recordTB{TB: t}

	w.Require(rec, 1, "ok", "rejected")

	if len(rec.fatals) != 0 || len(rec.errs) != 0 {
		t.Errorf("Require with every side seen failed: fatals=%v errs=%v", rec.fatals, rec.errs)
	}
}

func TestOverlap_ReturnsWhenWitnessed(t *testing.T) {
	calls := 0
	witnessed, iterations := Overlap(t, 5*time.Second, func() bool {
		calls++
		return calls == 3
	})
	if !witnessed {
		t.Fatal("Overlap = not witnessed; step returned true on the 3rd call")
	}
	if iterations != 3 || calls != 3 {
		t.Errorf("iterations = %d, calls = %d; want 3 and 3 (stop on the first true)", iterations, calls)
	}
}

func TestOverlap_ReturnsAtDeadline(t *testing.T) {
	const deadline = 60 * time.Millisecond
	start := time.Now()
	calls := 0
	witnessed, iterations := Overlap(t, deadline, func() bool {
		calls++
		return false
	})
	elapsed := time.Since(start)
	if witnessed {
		t.Fatal("Overlap = witnessed; step never returned true")
	}
	if elapsed < deadline {
		t.Errorf("Overlap returned after %s, before the %s deadline", elapsed, deadline)
	}
	if elapsed > 10*deadline {
		t.Errorf("Overlap returned after %s; it should stop promptly at the %s deadline", elapsed, deadline)
	}
	if iterations == 0 || iterations != calls {
		t.Errorf("iterations = %d, calls = %d; want a positive, equal count", iterations, calls)
	}
}

func TestEventually_ReturnsWhenConditionHolds(t *testing.T) {
	n := 0
	rec := &recordTB{TB: t}
	Eventually(rec, time.Second, time.Millisecond, func() bool {
		n++
		return n >= 3
	}, "counter never reached 3")
	if len(rec.fatals) != 0 {
		t.Errorf("Eventually failed although the condition held: %v", rec.fatals)
	}
	if n < 3 {
		t.Errorf("condition polled %d times, want >= 3", n)
	}
}

func TestEventually_TimesOutWithMessage(t *testing.T) {
	const deadline = 40 * time.Millisecond
	rec := &recordTB{TB: t}
	start := time.Now()
	Eventually(rec, deadline, 2*time.Millisecond, func() bool { return false }, "the flag was never raised")
	elapsed := time.Since(start)
	if len(rec.fatals) != 1 {
		t.Fatalf("Eventually past deadline: %d Fatalf calls, want 1", len(rec.fatals))
	}
	if !strings.Contains(rec.fatals[0], "the flag was never raised") {
		t.Errorf("timeout message %q does not carry the caller's message", rec.fatals[0])
	}
	if !strings.Contains(rec.fatals[0], deadline.String()) {
		t.Errorf("timeout message %q does not print the deadline %s", rec.fatals[0], deadline)
	}
	if elapsed < deadline {
		t.Errorf("Eventually gave up after %s, before the %s deadline", elapsed, deadline)
	}
}
