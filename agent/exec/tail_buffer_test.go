// ABOUTME: Unit tests for tailBuffer — the tail-window byte capture used by
// ABOUTME: ExecCommandWithLimit. Covers boundary, wrap, single-giant-write,
// ABOUTME: many-tiny-writes, and marker-spanning-boundary cases.
package exec

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestTailBuffer_BelowLimit_NoTruncation(t *testing.T) {
	tb := newTailBuffer(64)
	if _, err := tb.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if got := tb.String(); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
	if tb.Truncated() {
		t.Error("Truncated should be false when under limit")
	}
	if tb.BytesDropped() != 0 {
		t.Errorf("BytesDropped = %d, want 0", tb.BytesDropped())
	}
}

func TestTailBuffer_ExactLimit_NoTruncation(t *testing.T) {
	tb := newTailBuffer(8)
	if _, err := tb.Write([]byte("12345678")); err != nil {
		t.Fatal(err)
	}
	if got := tb.String(); got != "12345678" {
		t.Errorf("got %q, want %q", got, "12345678")
	}
	if tb.Truncated() {
		t.Error("exactly-at-limit must not be Truncated")
	}
}

func TestTailBuffer_OneByteOverLimit_KeepsTail(t *testing.T) {
	tb := newTailBuffer(8)
	if _, err := tb.Write([]byte("123456789")); err != nil {
		t.Fatal(err)
	}
	if got := tb.String(); got != "23456789" {
		t.Errorf("got %q, want %q", got, "23456789")
	}
	if !tb.Truncated() {
		t.Error("expected Truncated=true")
	}
	if tb.BytesDropped() != 1 {
		t.Errorf("BytesDropped = %d, want 1", tb.BytesDropped())
	}
}

// The headline #208 case: a routing marker emitted after a flood of output
// must be preserved in the captured tail.
func TestTailBuffer_RoutingMarkerAfterFlood(t *testing.T) {
	tb := newTailBuffer(1024)
	flood := bytes.Repeat([]byte("."), 4096)
	if _, err := tb.Write(flood); err != nil {
		t.Fatal(err)
	}
	if _, err := tb.Write([]byte("tests-fail-cloud")); err != nil {
		t.Fatal(err)
	}
	got := tb.String()
	if !strings.HasSuffix(got, "tests-fail-cloud") {
		t.Errorf("expected captured tail to end with marker, got tail = %q", tailPreview(got, 32))
	}
	if !tb.Truncated() {
		t.Error("expected Truncated=true")
	}
	if tb.BytesDropped() != 4096+16-1024 {
		t.Errorf("BytesDropped = %d, want %d", tb.BytesDropped(), 4096+16-1024)
	}
}

// Marker that straddles the write boundary in a single Write call must
// appear contiguously in the captured output.
func TestTailBuffer_MarkerSpansWriteBoundary(t *testing.T) {
	tb := newTailBuffer(1024)
	// Build a single write of 1023 dots + 16-byte marker, total 1039 bytes
	// — past the limit. The last 1024 bytes are: 1008 dots + 16-byte marker.
	payload := append(bytes.Repeat([]byte("."), 1023), []byte("ROUTE-OK-MARKER!")...)
	if _, err := tb.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := tb.String()
	if !strings.HasSuffix(got, "ROUTE-OK-MARKER!") {
		t.Errorf("marker must appear contiguously at end; tail = %q", tailPreview(got, 32))
	}
	if len(got) != 1024 {
		t.Errorf("captured length = %d, want 1024", len(got))
	}
}

// tailPreview returns the last n bytes of s for failure-message previews,
// returning s unchanged when len(s) <= n to avoid an out-of-range panic
// when an assertion fails on a short input.
func tailPreview(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// Marker placed across the ring-wrap boundary via two writes must still
// appear contiguously in the captured output.
func TestTailBuffer_MarkerStraddlesRingWrap(t *testing.T) {
	tb := newTailBuffer(16)
	// Fill to byte 14 with "..............", then write "<<MARKER>>" (10 bytes)
	// — total 24 bytes written, last 16 = "............<<MARKER>>"... no, last 16
	// is bytes 8..23 of input. Input is 14 dots + 10-byte marker, so bytes 8..23
	// = "......<<MARKER>>" (6 dots + 10 marker bytes = 16 bytes).
	if _, err := tb.Write(bytes.Repeat([]byte("."), 14)); err != nil {
		t.Fatal(err)
	}
	if _, err := tb.Write([]byte("<<MARKER>>")); err != nil {
		t.Fatal(err)
	}
	got := tb.String()
	want := "......<<MARKER>>"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Single Write call larger than the limit must be handled by keeping only
// the trailing `limit` bytes of that write.
func TestTailBuffer_SingleWriteLargerThanLimit(t *testing.T) {
	tb := newTailBuffer(16)
	// One write of 100 bytes; last 16 of those should win.
	payload := []byte(strings.Repeat("a", 84) + "0123456789abcdef")
	if _, err := tb.Write(payload); err != nil {
		t.Fatal(err)
	}
	if got := tb.String(); got != "0123456789abcdef" {
		t.Errorf("got %q, want %q", got, "0123456789abcdef")
	}
	if tb.BytesDropped() != 84 {
		t.Errorf("BytesDropped = %d, want 84", tb.BytesDropped())
	}
}

// Many tiny writes must produce the same tail as one big write of the
// concatenation.
func TestTailBuffer_ManyTinyWrites_TailInvariant(t *testing.T) {
	const limit = 256
	tb := newTailBuffer(limit)
	var all []byte
	for i := 0; i < 1000; i++ {
		b := []byte{byte(i & 0xff)}
		all = append(all, b...)
		if _, err := tb.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	want := all[len(all)-limit:]
	got := []byte(tb.String())
	if !bytes.Equal(got, want) {
		t.Errorf("tail invariant violated: len(got)=%d len(want)=%d first-disagree-at=%d", len(got), len(want), firstDiff(got, want))
	}
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

// Final write that crosses the limit: tail invariant must hold across the
// write boundary regardless of where the write split falls.
func TestTailBuffer_WriteBoundaryAtLimit(t *testing.T) {
	const limit = 100
	cases := []struct {
		name        string
		firstWrite  int
		secondWrite int
	}{
		{"first under, second over", 60, 80},
		{"first exactly at limit, second past", 100, 20},
		{"first over limit, second small", 200, 10},
		{"both tiny", 5, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tb := newTailBuffer(limit)
			all := make([]byte, 0, tc.firstWrite+tc.secondWrite)
			a := bytes.Repeat([]byte("A"), tc.firstWrite)
			b := bytes.Repeat([]byte("B"), tc.secondWrite)
			all = append(all, a...)
			all = append(all, b...)
			if _, err := tb.Write(a); err != nil {
				t.Fatalf("write a: %v", err)
			}
			if _, err := tb.Write(b); err != nil {
				t.Fatalf("write b: %v", err)
			}
			want := all
			if len(all) > limit {
				want = all[len(all)-limit:]
			}
			got := []byte(tb.String())
			if !bytes.Equal(got, want) {
				t.Errorf("tail mismatch: got len=%d, want len=%d", len(got), len(want))
			}
		})
	}
}

func TestTailBuffer_Empty(t *testing.T) {
	tb := newTailBuffer(64)
	if got := tb.String(); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if tb.Truncated() {
		t.Error("empty buffer should not be truncated")
	}
}

func TestTailBuffer_ConcurrentWrites_Safe(t *testing.T) {
	tb := newTailBuffer(4096)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if _, err := tb.Write([]byte("xyz")); err != nil {
					t.Errorf("concurrent write: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	// 16 goroutines × 100 writes × 3 bytes = 4800 bytes total.
	// Tail is 4096 bytes of "xyz" (interleaved by goroutine, but all "xyz" triples).
	got := tb.String()
	if len(got) != 4096 {
		t.Errorf("captured len = %d, want 4096", len(got))
	}
	// Every byte must be 'x', 'y', or 'z' — no torn writes producing other bytes.
	for i, b := range []byte(got) {
		if b != 'x' && b != 'y' && b != 'z' {
			t.Errorf("byte %d = %q, not in xyz", i, b)
			break
		}
	}
}
