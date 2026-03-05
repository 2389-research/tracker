// ABOUTME: Tests for the middleware system verifying onion-pattern execution order.
// ABOUTME: Ensures middleware wraps handlers so outer middleware runs first/last.
package llm

import (
	"context"
	"testing"
)

// orderTracker records the sequence of middleware invocations for testing.
type orderTracker struct {
	order []string
}

// trackingMiddleware returns a MiddlewareFunc that appends before/after labels to the tracker.
func trackingMiddleware(tracker *orderTracker, name string) MiddlewareFunc {
	return func(next CompleteHandler) CompleteHandler {
		return func(ctx context.Context, req *Request) (*Response, error) {
			tracker.order = append(tracker.order, name+"_before")
			resp, err := next(ctx, req)
			tracker.order = append(tracker.order, name+"_after")
			return resp, err
		}
	}
}

func TestMiddlewareOrder(t *testing.T) {
	tracker := &orderTracker{}

	mw1 := trackingMiddleware(tracker, "mw1")
	mw2 := trackingMiddleware(tracker, "mw2")

	mockProvider := &mockAdapter{
		name: "test",
		response: &Response{
			ID:      "resp-1",
			Message: AssistantMessage("ok"),
		},
	}

	client, err := NewClient(
		WithProvider(mockProvider),
		WithDefaultProvider("test"),
		WithMiddleware(mw1),
		WithMiddleware(mw2),
	)
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}
	defer client.Close()

	_, err = client.Complete(context.Background(), &Request{
		Model:    "test-model",
		Messages: []Message{UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"mw1_before", "mw2_before", "mw2_after", "mw1_after"}
	if len(tracker.order) != len(expected) {
		t.Fatalf("expected %d entries, got %d: %v", len(expected), len(tracker.order), tracker.order)
	}
	for i, v := range expected {
		if tracker.order[i] != v {
			t.Errorf("order[%d]: expected %q, got %q", i, v, tracker.order[i])
		}
	}
}

func TestMiddlewareFunc_SatisfiesMiddleware(t *testing.T) {
	var _ Middleware = MiddlewareFunc(func(next CompleteHandler) CompleteHandler {
		return next
	})
}
