// ABOUTME: Verifies concurrent webhook callback servers on :0 don't collide (#476).
package handlers

import "testing"

// TestWebhookInterviewer_EphemeralPortsDoNotCollide proves two callback servers
// bound to the ephemeral :0 address start successfully and get distinct ports —
// the property that lets a service run many webhook-gated pipelines at once
// (the old fixed :8789 default would fail the second net.Listen).
func TestWebhookInterviewer_EphemeralPortsDoNotCollide(t *testing.T) {
	a := NewWebhookInterviewer("http://example.invalid", ":0")
	b := NewWebhookInterviewer("http://example.invalid", ":0")
	defer a.Cancel()
	defer b.Cancel()

	if err := a.startServerOnce(); err != nil {
		t.Fatalf("server a failed to start on :0: %v", err)
	}
	if err := b.startServerOnce(); err != nil {
		t.Fatalf("server b failed to start on :0 while a is running: %v", err)
	}

	addrA := a.listener.Addr().String()
	addrB := b.listener.Addr().String()
	if addrA == addrB {
		t.Fatalf("expected distinct ephemeral ports, both bound %s", addrA)
	}
}
