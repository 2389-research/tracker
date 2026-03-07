// ABOUTME: Tests for the core LLM Client covering provider routing, error cases, and streaming.
// ABOUTME: Verifies explicit routing, default fallback, unknown provider errors, and stream behavior.
package llm

import (
	"context"
	"errors"
	"testing"
)

func TestClientRouting(t *testing.T) {
	providerA := &mockAdapter{
		name:     "alpha",
		response: &Response{ID: "a-1", Message: AssistantMessage("from alpha")},
	}
	providerB := &mockAdapter{
		name:     "beta",
		response: &Response{ID: "b-1", Message: AssistantMessage("from beta")},
	}

	client, err := NewClient(
		WithProvider(providerA),
		WithProvider(providerB),
		WithDefaultProvider("alpha"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer client.Close()

	// Explicit provider routing to beta.
	resp, err := client.Complete(context.Background(), &Request{
		Model:    "model-b",
		Messages: []Message{UserMessage("hi")},
		Provider: "beta",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Provider != "beta" {
		t.Errorf("expected provider 'beta', got %q", resp.Provider)
	}
	if resp.Text() != "from beta" {
		t.Errorf("expected 'from beta', got %q", resp.Text())
	}

	// Default provider routing to alpha.
	resp, err = client.Complete(context.Background(), &Request{
		Model:    "model-a",
		Messages: []Message{UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Provider != "alpha" {
		t.Errorf("expected provider 'alpha', got %q", resp.Provider)
	}
	if resp.Text() != "from alpha" {
		t.Errorf("expected 'from alpha', got %q", resp.Text())
	}
}

func TestClientNoProvider(t *testing.T) {
	_, err := NewClient()
	if err == nil {
		t.Fatal("expected error for no providers, got nil")
	}

	var cfgErr *ConfigurationError
	if !errors.As(err, &cfgErr) {
		t.Errorf("expected ConfigurationError, got %T: %v", err, err)
	}
}

func TestClientUnknownProvider(t *testing.T) {
	provider := &mockAdapter{
		name:     "known",
		response: &Response{ID: "k-1", Message: AssistantMessage("ok")},
	}

	client, err := NewClient(
		WithProvider(provider),
		WithDefaultProvider("known"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer client.Close()

	_, err = client.Complete(context.Background(), &Request{
		Model:    "m",
		Messages: []Message{UserMessage("hi")},
		Provider: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}

	var cfgErr *ConfigurationError
	if !errors.As(err, &cfgErr) {
		t.Errorf("expected ConfigurationError, got %T: %v", err, err)
	}
}

func TestClientStream(t *testing.T) {
	events := []StreamEvent{
		{Type: EventStreamStart},
		{Type: EventTextDelta, Delta: "streamed"},
		{Type: EventFinish, FinishReason: &FinishReason{Reason: "stop"}},
	}

	provider := &mockAdapter{
		name:   "streamer",
		events: events,
	}

	client, err := NewClient(
		WithProvider(provider),
		WithDefaultProvider("streamer"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer client.Close()

	ch := client.Stream(context.Background(), &Request{
		Model:    "m",
		Messages: []Message{UserMessage("hi")},
	})

	var received []StreamEvent
	for evt := range ch {
		received = append(received, evt)
	}

	if len(received) != 3 {
		t.Fatalf("expected 3 events, got %d", len(received))
	}
	if received[0].Type != EventStreamStart {
		t.Errorf("expected StreamStart, got %s", received[0].Type)
	}
	if received[1].Delta != "streamed" {
		t.Errorf("expected delta 'streamed', got %q", received[1].Delta)
	}
}

func TestClientStream_UnknownProvider(t *testing.T) {
	provider := &mockAdapter{
		name:   "valid",
		events: []StreamEvent{{Type: EventStreamStart}},
	}

	client, err := NewClient(
		WithProvider(provider),
		WithDefaultProvider("valid"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer client.Close()

	ch := client.Stream(context.Background(), &Request{
		Model:    "m",
		Messages: []Message{UserMessage("hi")},
		Provider: "ghost",
	})

	evt := <-ch
	if evt.Type != EventError {
		t.Errorf("expected EventError, got %s", evt.Type)
	}
	if evt.Err == nil {
		t.Error("expected non-nil error in error event")
	}
}

func TestClientSingleProviderDefaultsToIt(t *testing.T) {
	provider := &mockAdapter{
		name:     "solo",
		response: &Response{ID: "s-1", Message: AssistantMessage("solo response")},
	}

	client, err := NewClient(WithProvider(provider))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer client.Close()

	resp, err := client.Complete(context.Background(), &Request{
		Model:    "m",
		Messages: []Message{UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Provider != "solo" {
		t.Errorf("expected provider 'solo', got %q", resp.Provider)
	}
}

func TestClientCompletePublishesTraceEvents(t *testing.T) {
	provider := &mockAdapter{
		name: "streamer",
		events: []StreamEvent{
			{Type: EventStreamStart},
			{Type: EventTextStart, TextID: "t1"},
			{Type: EventTextDelta, TextID: "t1", Delta: "hello"},
			{Type: EventFinish, FinishReason: &FinishReason{Reason: "stop"}},
		},
	}

	var traces []TraceEvent
	client, err := NewClient(
		WithProvider(provider),
		WithDefaultProvider("streamer"),
		WithTraceObserver(TraceObserverFunc(func(evt TraceEvent) {
			traces = append(traces, evt)
		})),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer client.Close()

	resp, err := client.Complete(context.Background(), &Request{
		Model:    "m",
		Messages: []Message{UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text() != "hello" {
		t.Fatalf("resp.Text() = %q, want %q", resp.Text(), "hello")
	}
	if len(traces) == 0 {
		t.Fatal("expected trace events")
	}
	if traces[0].Kind != TraceRequestStart {
		t.Fatalf("traces[0].Kind = %q, want %q", traces[0].Kind, TraceRequestStart)
	}
}
