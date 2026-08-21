// ABOUTME: A transient stream error retries the completion (not the node), so a
// ABOUTME: deep multi-turn episode is preserved via the accumulated request (#574).
package llm

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestRetry_StreamErrorRetriesCompletion(t *testing.T) {
	rm := NewRetryMiddleware(WithMaxRetries(2), WithBaseDelay(time.Millisecond))
	calls := 0
	handler := CompleteHandler(func(ctx context.Context, req *Request) (*Response, error) {
		calls++
		if calls == 1 {
			// The exact class the adapter now emits on a mid-stream cut.
			return nil, &StreamError{SDKError: SDKError{Msg: "anthropic: SSE read error", Cause: io.ErrUnexpectedEOF}}
		}
		return &Response{}, nil
	})
	resp, err := rm.WrapComplete(handler)(context.Background(), &Request{})
	if err != nil {
		t.Fatalf("StreamError should be retried at the completion level, got err: %v", err)
	}
	if resp == nil {
		t.Fatal("expected a response after retry")
	}
	if calls != 2 {
		t.Fatalf("expected exactly one retry (2 calls), got %d — the completion was not retried", calls)
	}
}

func TestStreamError_IsRetryable(t *testing.T) {
	if !isRetryable(&StreamError{SDKError: SDKError{Msg: "x"}}) {
		t.Fatal("StreamError must be retryable so a transient stream cut retries the completion, not the node")
	}
}
