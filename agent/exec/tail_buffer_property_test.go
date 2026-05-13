// ABOUTME: Property-based tests for tailBuffer using pgregory.net/rapid.
// ABOUTME: Covers the tail-window invariant across arbitrary write sequences (issue #214 / #208).
package exec

import (
	"testing"

	"pgregory.net/rapid"
)

// TestTailBuffer_TailInvariant_Rapid pins the core invariant: after any
// sequence of Write calls, tb.String() equals the last min(total, limit)
// bytes of the concatenation of those writes. This generalizes the
// example-based tests in tail_buffer_test.go — which pin ~12 specific
// boundary cases — and catches off-by-one boundary errors, write-
// boundary state corruption, and the ring-buffer wrap-around case that
// PR #215 went through several iterations to get right.
func TestTailBuffer_TailInvariant_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Limit bounded to keep the test fast while still exercising
		// boundaries around the realistic per-stream cap (64KB) plus
		// well below and a touch above it.
		limit := rapid.IntRange(1, 70000).Draw(t, "limit")
		// Each individual write up to 8KB; total writes up to ~30 so the
		// total bytes can comfortably exceed limit for the wrap path.
		writes := rapid.SliceOfN(
			rapid.SliceOfN(rapid.Byte(), 0, 8192),
			0, 30,
		).Draw(t, "writes")

		tb := newTailBuffer(limit)
		var concat []byte
		for _, w := range writes {
			if _, err := tb.Write(w); err != nil {
				t.Fatalf("Write returned error: %v", err)
			}
			concat = append(concat, w...)
		}

		want := concat
		if len(want) > limit {
			want = want[len(want)-limit:]
		}
		got := []byte(tb.String())
		if string(got) != string(want) {
			t.Fatalf("tail mismatch: limit=%d total=%d\n got len=%d\nwant len=%d",
				limit, len(concat), len(got), len(want))
		}
	})
}

// TestTailBuffer_TruncatedFlag_Rapid pins the Truncated() invariant: it
// reports true iff total bytes written exceeded limit. (BytesDropped is
// covered transitively by the tail invariant + this flag.)
func TestTailBuffer_TruncatedFlag_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		limit := rapid.IntRange(1, 70000).Draw(t, "limit")
		writes := rapid.SliceOfN(
			rapid.SliceOfN(rapid.Byte(), 0, 8192),
			0, 30,
		).Draw(t, "writes")

		tb := newTailBuffer(limit)
		total := 0
		for _, w := range writes {
			if _, err := tb.Write(w); err != nil {
				t.Fatalf("Write returned error: %v", err)
			}
			total += len(w)
		}

		wantTruncated := total > limit
		gotTruncated := tb.Truncated()
		if gotTruncated != wantTruncated {
			t.Fatalf("Truncated() = %v, want %v (limit=%d total=%d)",
				gotTruncated, wantTruncated, limit, total)
		}

		wantDropped := 0
		if total > limit {
			wantDropped = total - limit
		}
		gotDropped := tb.BytesDropped()
		if gotDropped != wantDropped {
			t.Fatalf("BytesDropped() = %d, want %d (limit=%d total=%d)",
				gotDropped, wantDropped, limit, total)
		}
	})
}
