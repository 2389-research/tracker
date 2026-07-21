// ABOUTME: Tests SlackBot's pending-freeform bookkeeping (clobber guard).
package main

import "testing"

// TestSlackBot_ClearPendingFreeform: clear only removes the entry when the gate
// id still matches (never clobbers a newer gate).
func TestSlackBot_ClearPendingFreeform(t *testing.T) {
	b := &SlackBot{pendingFree: map[string]string{}}

	b.setPendingFreeform("T", "G1")
	b.clearPendingFreeform("T", "G2") // stale id — must NOT clear
	if got := b.takePendingFreeform("T"); got != "G1" {
		t.Fatalf("clear with wrong id removed the entry; got %q", got)
	}

	b.setPendingFreeform("T", "G1")
	b.clearPendingFreeform("T", "G1") // matching — clears
	if got := b.takePendingFreeform("T"); got != "" {
		t.Fatalf("matching clear left %q", got)
	}
}
