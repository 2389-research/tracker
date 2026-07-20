// ABOUTME: Tests the library webhook-gate default callback port (#476).
// ABOUTME: Empty CallbackAddr resolves to an ephemeral :0 so concurrent runs don't collide.
package tracker

import (
	"testing"

	"github.com/2389-research/tracker/pipeline/handlers"
)

func TestResolveWebhookInterviewer_DefaultsToEphemeralPort(t *testing.T) {
	iv, err := resolveWebhookInterviewer(&WebhookGateConfig{WebhookURL: "http://example.invalid"})
	if err != nil {
		t.Fatalf("resolveWebhookInterviewer: %v", err)
	}
	wi, ok := iv.(*handlers.WebhookInterviewer)
	if !ok {
		t.Fatalf("expected *handlers.WebhookInterviewer, got %T", iv)
	}
	if wi.CallbackAddr != ":0" {
		t.Fatalf("default CallbackAddr = %q, want %q (ephemeral)", wi.CallbackAddr, ":0")
	}
}

func TestResolveWebhookInterviewer_RespectsExplicitAddr(t *testing.T) {
	iv, err := resolveWebhookInterviewer(&WebhookGateConfig{
		WebhookURL:   "http://example.invalid",
		CallbackAddr: ":9999",
	})
	if err != nil {
		t.Fatalf("resolveWebhookInterviewer: %v", err)
	}
	if got := iv.(*handlers.WebhookInterviewer).CallbackAddr; got != ":9999" {
		t.Fatalf("explicit CallbackAddr = %q, want :9999", got)
	}
}
