// ABOUTME: Tests for review fixes — pending-freeform cleanup and clear-on-exit.
package main

import (
	"sync"
	"testing"
)

// clearingUI is a fakeUI that also implements pendingClearer, recording clears.
type clearingUI struct {
	*fakeUI
	mu      sync.Mutex
	cleared []string
}

func (c *clearingUI) clearPending(gateID string) {
	c.mu.Lock()
	c.cleared = append(c.cleared, gateID)
	c.mu.Unlock()
}

func (c *clearingUI) clears() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.cleared...)
}

// TestSlackInterviewer_ClearsPendingOnCancel: a cancelled freeform gate tells the
// transport to clear its pending entry (so a later reply isn't misconsumed).
func TestSlackInterviewer_ClearsPendingOnCancel(t *testing.T) {
	ui := &clearingUI{fakeUI: newFakeUI()}
	iv := NewSlackInterviewer(ui, seqIDs())

	done := make(chan struct{})
	go func() { _, _ = iv.AskFreeform("hold"); close(done) }()
	g := awaitGate(t, ui.fakeUI)
	iv.Cancel()
	<-done

	if got := ui.clears(); len(got) != 1 || got[0] != g.ID {
		t.Fatalf("expected clearPending(%s), got %v", g.ID, got)
	}
}

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
