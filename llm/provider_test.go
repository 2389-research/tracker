// ABOUTME: Tests for the ProviderAdapter interface.
// ABOUTME: Uses a mock adapter to verify interface satisfaction, Complete(), and Stream().
package llm

import (
	"context"
	"testing"
)

// mockAdapter is a test double that implements ProviderAdapter.
type mockAdapter struct {
	name     string
	response *Response
	events   []StreamEvent
}

func (m *mockAdapter) Name() string {
	return m.name
}

func (m *mockAdapter) Complete(_ context.Context, _ *Request) (*Response, error) {
	return m.response, nil
}

func (m *mockAdapter) Stream(_ context.Context, _ *Request) <-chan StreamEvent {
	ch := make(chan StreamEvent, len(m.events))
	for _, e := range m.events {
		ch <- e
	}
	close(ch)
	return ch
}

func (m *mockAdapter) Close() error {
	return nil
}

func TestMockAdapter_SatisfiesProviderAdapter(t *testing.T) {
	var _ ProviderAdapter = &mockAdapter{}
}

func TestMockAdapter_Name(t *testing.T) {
	adapter := &mockAdapter{name: "test-provider"}
	if adapter.Name() != "test-provider" {
		t.Errorf("expected 'test-provider', got %q", adapter.Name())
	}
}

func TestMockAdapter_Complete(t *testing.T) {
	expected := &Response{
		ID:       "resp-1",
		Model:    "gpt-4",
		Provider: "test-provider",
		Message:  AssistantMessage("hello world"),
		Usage:    Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}

	adapter := &mockAdapter{
		name:     "test-provider",
		response: expected,
	}

	resp, err := adapter.Complete(context.Background(), &Request{
		Model:    "gpt-4",
		Messages: []Message{UserMessage("hi")},
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "resp-1" {
		t.Errorf("expected ID 'resp-1', got %q", resp.ID)
	}
	if resp.Text() != "hello world" {
		t.Errorf("expected text 'hello world', got %q", resp.Text())
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("expected 15 total tokens, got %d", resp.Usage.TotalTokens)
	}
}

func TestMockAdapter_Stream(t *testing.T) {
	events := []StreamEvent{
		{Type: EventStreamStart},
		{Type: EventTextDelta, Delta: "hello"},
		{Type: EventFinish, FinishReason: &FinishReason{Reason: "stop"}},
	}

	adapter := &mockAdapter{
		name:   "test-provider",
		events: events,
	}

	ch := adapter.Stream(context.Background(), &Request{
		Model:    "gpt-4",
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
		t.Errorf("expected first event StreamStart, got %s", received[0].Type)
	}
	if received[1].Delta != "hello" {
		t.Errorf("expected delta 'hello', got %q", received[1].Delta)
	}
	if received[2].FinishReason.Reason != "stop" {
		t.Errorf("expected finish reason 'stop', got %q", received[2].FinishReason.Reason)
	}
}
