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

// TestRetry_UsageCountedOncePerRetry pins the cost-correctness half of #550: even
// though the token tracker's middleware fires on every retry attempt, it records
// usage ONLY on the successful response — so a fail-then-succeed retry counts the
// usage once, never double. (The residual in #550 is trace CONTENT duplication in
// the activity log, not usage/cost.)
func TestRetry_UsageCountedOncePerRetry(t *testing.T) {
	tt := NewTokenTracker()
	calls := 0
	handler := CompleteHandler(func(ctx context.Context, req *Request) (*Response, error) {
		calls++
		if calls == 1 {
			return nil, &StreamError{SDKError: SDKError{Msg: "mid-stream cut"}}
		}
		return &Response{Provider: "anthropic", Model: "claude-opus-5",
			Usage: Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150}}, nil
	})
	// Retry OUTSIDE the token tracker: the tracker fires on both attempts but only
	// records the one that returned err == nil.
	chain := NewRetryMiddleware(WithMaxRetries(2), WithBaseDelay(time.Millisecond)).
		WrapComplete(tt.WrapComplete(handler))
	if _, err := chain(context.Background(), &Request{Model: "claude-opus-5", Provider: "anthropic"}); err != nil {
		t.Fatalf("retry should have succeeded: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected one retry (2 attempts), got %d", calls)
	}
	total := tt.TotalUsage()
	if total.InputTokens != 100 || total.OutputTokens != 50 {
		t.Fatalf("usage double-counted across a retry: got in=%d out=%d, want in=100 out=50 (counted once)",
			total.InputTokens, total.OutputTokens)
	}
}

// fakeAbortAdapter streams one text delta, then a mid-stream error.
type fakeAbortAdapter struct{}

func (fakeAbortAdapter) Name() string { return "anthropic" }
func (fakeAbortAdapter) Complete(ctx context.Context, req *Request) (*Response, error) {
	return &Response{}, nil
}
func (fakeAbortAdapter) Close() error { return nil }
func (fakeAbortAdapter) Stream(ctx context.Context, req *Request) <-chan StreamEvent {
	ch := make(chan StreamEvent, 4)
	ch <- StreamEvent{Type: EventTextDelta, Delta: "partial"}
	ch <- StreamEvent{Type: EventError, Err: &StreamError{SDKError: SDKError{Msg: "mid-stream cut"}}}
	close(ch)
	return ch
}

// TestAbortedAttemptEmitsTerminalBoundary: a stream that errors mid-flight must
// emit an explicit aborted TraceFinish for its CallID, so a trace reader can
// collapse the failed attempt's partial deltas rather than leaving them dangling
// alongside the successful retry (#550).
func TestAbortedAttemptEmitsTerminalBoundary(t *testing.T) {
	var events []TraceEvent
	obs := TraceObserverFunc(func(e TraceEvent) { events = append(events, e) })
	client, err := NewClient(WithProvider(fakeAbortAdapter{}))
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	// No retry middleware here — we just want to observe the single aborted attempt.
	_, _ = client.Complete(context.Background(), &Request{
		Provider: "anthropic", Model: "claude-opus-5",
		TraceObservers: []TraceObserver{obs},
	})
	var aborted bool
	for _, e := range events {
		if e.Kind == TraceFinish && e.FinishReason == "aborted" {
			aborted = true
		}
	}
	if !aborted {
		t.Fatalf("aborted attempt did not emit a terminal 'aborted' TraceFinish; events=%+v", events)
	}
}
