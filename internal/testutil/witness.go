// ABOUTME: Witness is a set of named, goroutine-safe counters for concurrency tests.
// ABOUTME: It prints every side in one line so a failure shows what was and was not seen.

// Package testutil holds helpers for tests that must prove a concurrent
// interaction actually happened, rather than passing because the scheduler
// happened to keep the two sides apart (#658).
//
// The rule these helpers encode: a concurrency test witnesses BOTH sides
// with positive counters, runs until every witness holds or a deadline
// fails it, and prints every counter in its failure message. A test that
// counts only one side, or stops when one side finishes, can pass
// vacuously on a broken implementation — which is exactly what the
// original TestParallelBranchSymlinkRace did.
//
// Lives in internal/ so it is not part of the public API surface.
package testutil

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Witness tracks named counters ("sides" of an interaction). Declared names
// print first, in declaration order, so a failure message reads the same
// way every time; sides hit without being declared are appended.
type Witness struct {
	mu     sync.Mutex
	order  []string
	counts map[string]int64
}

// NewWitness declares the sides the test intends to observe. Every declared
// side is printed by String even while its count is zero.
func NewWitness(names ...string) *Witness {
	w := &Witness{counts: make(map[string]int64, len(names))}
	for _, n := range names {
		w.order = append(w.order, n)
		w.counts[n] = 0
	}
	return w
}

// Hit records one observation of side name.
func (w *Witness) Hit(name string) { w.Add(name, 1) }

// Add records n observations of side name. Safe for concurrent use.
func (w *Witness) Add(name string, n int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, known := w.counts[name]; !known {
		w.order = append(w.order, name)
	}
	w.counts[name] += n
}

// Count returns how many times side name was observed (0 for an unknown side).
func (w *Witness) Count(name string) int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.counts[name]
}

// String renders every side as "name=count", space-separated, declared sides
// first. Use it in every failure message so the record shows all counters,
// not just the one that tripped.
func (w *Witness) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	parts := make([]string, 0, len(w.order))
	for _, n := range w.order {
		parts = append(parts, fmt.Sprintf("%s=%d", n, w.counts[n]))
	}
	return strings.Join(parts, " ")
}

// Require fails the test (one Fatalf) unless every named side was observed
// at least min times. The message names each side that fell short, its
// count, the minimum, and the full counter line.
func (w *Witness) Require(t testing.TB, min int64, sides ...string) {
	t.Helper()
	var short []string
	for _, s := range sides {
		if c := w.Count(s); c < min {
			short = append(short, fmt.Sprintf("%q seen %d time(s), want >= %d", s, c, min))
		}
	}
	if len(short) > 0 {
		t.Fatalf("witness not satisfied: %s — counters: %s", strings.Join(short, "; "), w.String())
	}
}
