// ABOUTME: Tests for review fixes — pending-freeform cleanup on interviewer cancel.
package chatops

import (
	"sync"
	"testing"
)

// clearingUI is a fakeUI that also implements PendingClearer, recording clears.
type clearingUI struct {
	*fakeUI
	mu      sync.Mutex
	cleared []string
}

func (c *clearingUI) ClearPending(gateID string) {
	c.mu.Lock()
	c.cleared = append(c.cleared, gateID)
	c.mu.Unlock()
}

func (c *clearingUI) clears() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.cleared...)
}

// TestThreadInterviewer_ClearsPendingOnCancel: a cancelled freeform gate tells the
// transport to clear its pending entry (so a later reply isn't misconsumed).
func TestThreadInterviewer_ClearsPendingOnCancel(t *testing.T) {
	ui := &clearingUI{fakeUI: newFakeUI()}
	iv := NewThreadInterviewer(ui, seqIDs())

	done := make(chan struct{})
	go func() { _, _ = iv.AskFreeform("hold"); close(done) }()
	g := awaitGate(t, ui.fakeUI)
	iv.Cancel()
	<-done

	if got := ui.clears(); len(got) != 1 || got[0] != g.ID {
		t.Fatalf("expected ClearPending(%s), got %v", g.ID, got)
	}
}
