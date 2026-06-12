// ABOUTME: Tests for client-level trace observer behavior, including SessionOwned
// ABOUTME: stamping that lets log writers dedup session-re-emitted llm events (#354).
package llm

import (
	"context"
	"testing"
)

// streamingMockEvents is a minimal stream that produces request_start, text,
// finish, and (verbose) provider_raw trace events.
func streamingMockEvents() []StreamEvent {
	return []StreamEvent{
		{Type: EventStreamStart},
		{Type: EventTextDelta, Delta: "hello"},
		{Type: EventFinish, FinishReason: &FinishReason{Reason: "stop"}},
	}
}

func TestClientTraceEvents_NotSessionOwnedWithoutRequestObservers(t *testing.T) {
	adapter := &mockAdapter{name: "alpha", events: streamingMockEvents()}
	client, err := NewClient(WithProvider(adapter), WithDefaultProvider("alpha"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer client.Close()

	var seen []TraceEvent
	client.AddTraceObserver(TraceObserverFunc(func(evt TraceEvent) {
		seen = append(seen, evt)
	}))

	if _, err := client.Complete(context.Background(), &Request{
		Model:    "m",
		Messages: []Message{UserMessage("hi")},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(seen) == 0 {
		t.Fatal("expected client-level observer to receive trace events")
	}
	for _, evt := range seen {
		if evt.SessionOwned {
			t.Errorf("event %s: SessionOwned = true for request without request-level observers", evt.Kind)
		}
	}
}

func TestClientTraceEvents_SessionOwnedWithRequestObservers(t *testing.T) {
	adapter := &mockAdapter{name: "alpha", events: streamingMockEvents()}
	client, err := NewClient(WithProvider(adapter), WithDefaultProvider("alpha"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer client.Close()

	var clientSeen []TraceEvent
	client.AddTraceObserver(TraceObserverFunc(func(evt TraceEvent) {
		clientSeen = append(clientSeen, evt)
	}))

	var requestSeen []TraceEvent
	if _, err := client.Complete(context.Background(), &Request{
		Model:    "m",
		Messages: []Message{UserMessage("hi")},
		TraceObservers: []TraceObserver{TraceObserverFunc(func(evt TraceEvent) {
			requestSeen = append(requestSeen, evt)
		})},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(clientSeen) == 0 {
		t.Fatal("expected client-level observer to receive trace events")
	}
	if len(requestSeen) != len(clientSeen) {
		t.Fatalf("request-level observer saw %d events, client-level saw %d", len(requestSeen), len(clientSeen))
	}
	for _, evt := range clientSeen {
		if !evt.SessionOwned {
			t.Errorf("event %s: SessionOwned = false for request with request-level observers", evt.Kind)
		}
	}
}
