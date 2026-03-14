// ABOUTME: Tests for agent event types and the EventHandler interface.
// ABOUTME: Validates event construction and multi-handler fan-out.
package agent

import (
	"testing"
)

func TestEventTypes(t *testing.T) {
	types := []EventType{
		EventSessionStart,
		EventSessionEnd,
		EventTurnStart,
		EventTurnEnd,
		EventToolCallStart,
		EventToolCallEnd,
		EventTextDelta,
		EventError,
		EventContextWindowWarning,
		EventSteeringInjected,
		EventLLMRequestStart,
		EventLLMReasoning,
		EventLLMText,
		EventLLMToolPrepare,
		EventLLMFinish,
		EventLLMProviderRaw,
		EventToolCacheHit,
		EventContextCompaction,
		EventTurnMetrics,
	}
	seen := make(map[EventType]bool)
	for _, et := range types {
		if seen[et] {
			t.Errorf("duplicate event type: %s", et)
		}
		seen[et] = true
	}
}

func TestMultiHandler(t *testing.T) {
	var received []Event
	handler := EventHandlerFunc(func(evt Event) {
		received = append(received, evt)
	})

	multi := MultiHandler(handler, handler)
	multi.HandleEvent(Event{Type: EventTurnStart})

	if len(received) != 2 {
		t.Fatalf("expected 2 events, got %d", len(received))
	}
}

func TestNilHandlerNoPanic(t *testing.T) {
	NoopHandler.HandleEvent(Event{Type: EventTurnStart})
}

func TestMultiHandlerWithNilNoPanic(t *testing.T) {
	var received []Event
	handler := EventHandlerFunc(func(evt Event) {
		received = append(received, evt)
	})

	multi := MultiHandler(handler, nil, handler)
	multi.HandleEvent(Event{Type: EventTurnStart})

	if len(received) != 2 {
		t.Fatalf("expected 2 events (nil skipped), got %d", len(received))
	}
}
