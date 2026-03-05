// ABOUTME: Tests for retry middleware with exponential backoff.
// ABOUTME: Covers retryable/non-retryable errors, context cancellation, RetryAfter, and defaults.
package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

// callCountHandler returns a handler that fails with the given error for the
// first failCount calls, then succeeds with a fixed response.
func callCountHandler(failCount int, err error) (CompleteHandler, *int) {
	calls := 0
	handler := func(ctx context.Context, req *Request) (*Response, error) {
		calls++
		if calls <= failCount {
			return nil, err
		}
		return &Response{ID: "ok", Message: AssistantMessage("done")}, nil
	}
	return handler, &calls
}

func TestRetryMiddleware_RateLimitSucceedsOnSecondAttempt(t *testing.T) {
	rle := &RateLimitError{ProviderError: ProviderError{
		SDKError:   SDKError{Msg: "rate limited"},
		StatusCode: 429,
	}}
	inner, calls := callCountHandler(1, rle)

	mw := NewRetryMiddleware(WithMaxRetries(3), WithBaseDelay(time.Millisecond))
	handler := mw.WrapComplete(inner)

	resp, err := handler(context.Background(), &Request{Model: "m", Messages: []Message{UserMessage("hi")}})
	if err != nil {
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	if resp.ID != "ok" {
		t.Fatalf("expected response ID 'ok', got %q", resp.ID)
	}
	if *calls != 2 {
		t.Fatalf("expected 2 calls, got %d", *calls)
	}
}

func TestRetryMiddleware_ServerErrorSucceedsOnThirdAttempt(t *testing.T) {
	se := &ServerError{ProviderError: ProviderError{
		SDKError:   SDKError{Msg: "server error"},
		StatusCode: 500,
	}}
	inner, calls := callCountHandler(2, se)

	mw := NewRetryMiddleware(WithMaxRetries(3), WithBaseDelay(time.Millisecond))
	handler := mw.WrapComplete(inner)

	resp, err := handler(context.Background(), &Request{Model: "m", Messages: []Message{UserMessage("hi")}})
	if err != nil {
		t.Fatalf("expected success after retries, got error: %v", err)
	}
	if resp.ID != "ok" {
		t.Fatalf("expected response ID 'ok', got %q", resp.ID)
	}
	if *calls != 3 {
		t.Fatalf("expected 3 calls, got %d", *calls)
	}
}

func TestRetryMiddleware_NoRetryOnAuthenticationError(t *testing.T) {
	ae := &AuthenticationError{ProviderError: ProviderError{
		SDKError:   SDKError{Msg: "bad creds"},
		StatusCode: 401,
	}}
	inner, calls := callCountHandler(5, ae)

	mw := NewRetryMiddleware(WithMaxRetries(3), WithBaseDelay(time.Millisecond))
	handler := mw.WrapComplete(inner)

	_, err := handler(context.Background(), &Request{Model: "m", Messages: []Message{UserMessage("hi")}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var authErr *AuthenticationError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected AuthenticationError, got %T: %v", err, err)
	}
	if *calls != 1 {
		t.Fatalf("expected exactly 1 call (no retries), got %d", *calls)
	}
}

func TestRetryMiddleware_NoRetryOnContextLengthError(t *testing.T) {
	cle := &ContextLengthError{ProviderError: ProviderError{
		SDKError:   SDKError{Msg: "too long"},
		StatusCode: 413,
	}}
	inner, calls := callCountHandler(5, cle)

	mw := NewRetryMiddleware(WithMaxRetries(3), WithBaseDelay(time.Millisecond))
	handler := mw.WrapComplete(inner)

	_, err := handler(context.Background(), &Request{Model: "m", Messages: []Message{UserMessage("hi")}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var clErr *ContextLengthError
	if !errors.As(err, &clErr) {
		t.Fatalf("expected ContextLengthError, got %T: %v", err, err)
	}
	if *calls != 1 {
		t.Fatalf("expected exactly 1 call (no retries), got %d", *calls)
	}
}

func TestRetryMiddleware_MaxRetriesExhausted(t *testing.T) {
	se := &ServerError{ProviderError: ProviderError{
		SDKError:   SDKError{Msg: "always fails"},
		StatusCode: 500,
	}}
	inner, calls := callCountHandler(100, se)

	mw := NewRetryMiddleware(WithMaxRetries(3), WithBaseDelay(time.Millisecond))
	handler := mw.WrapComplete(inner)

	_, err := handler(context.Background(), &Request{Model: "m", Messages: []Message{UserMessage("hi")}})
	if err == nil {
		t.Fatal("expected error after max retries, got nil")
	}
	var srvErr *ServerError
	if !errors.As(err, &srvErr) {
		t.Fatalf("expected ServerError, got %T: %v", err, err)
	}
	// 1 initial + 3 retries = 4 total calls
	if *calls != 4 {
		t.Fatalf("expected 4 calls (1 initial + 3 retries), got %d", *calls)
	}
}

func TestRetryMiddleware_ContextCancelledDuringBackoff(t *testing.T) {
	se := &ServerError{ProviderError: ProviderError{
		SDKError:   SDKError{Msg: "server error"},
		StatusCode: 500,
	}}
	inner, calls := callCountHandler(100, se)

	// Use a long base delay so the backoff timer is definitely still waiting.
	mw := NewRetryMiddleware(WithMaxRetries(3), WithBaseDelay(5*time.Second))
	handler := mw.WrapComplete(inner)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short delay to interrupt the backoff wait.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := handler(ctx, &Request{Model: "m", Messages: []Message{UserMessage("hi")}})
	if err == nil {
		t.Fatal("expected error from context cancellation, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %T: %v", err, err)
	}
	// Should have made exactly 1 call, then been cancelled during backoff.
	if *calls != 1 {
		t.Fatalf("expected 1 call before cancellation, got %d", *calls)
	}
}

func TestRetryMiddleware_SuccessfulFirstCall(t *testing.T) {
	inner, calls := callCountHandler(0, nil)

	mw := NewRetryMiddleware(WithMaxRetries(3), WithBaseDelay(time.Millisecond))
	handler := mw.WrapComplete(inner)

	resp, err := handler(context.Background(), &Request{Model: "m", Messages: []Message{UserMessage("hi")}})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if resp.ID != "ok" {
		t.Fatalf("expected response ID 'ok', got %q", resp.ID)
	}
	if *calls != 1 {
		t.Fatalf("expected exactly 1 call, got %d", *calls)
	}
}

func TestRetryMiddleware_RetryAfterRespected(t *testing.T) {
	retryAfterSec := 0.01 // 10ms
	rle := &RateLimitError{ProviderError: ProviderError{
		SDKError:   SDKError{Msg: "rate limited"},
		StatusCode: 429,
		RetryAfter: &retryAfterSec,
	}}
	inner, calls := callCountHandler(1, rle)

	// Use a very long base delay. If RetryAfter is respected, the retry
	// should use the short RetryAfter duration instead of the long base delay.
	mw := NewRetryMiddleware(WithMaxRetries(3), WithBaseDelay(10*time.Second))
	handler := mw.WrapComplete(inner)

	start := time.Now()
	resp, err := handler(context.Background(), &Request{Model: "m", Messages: []Message{UserMessage("hi")}})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if resp.ID != "ok" {
		t.Fatalf("expected response ID 'ok', got %q", resp.ID)
	}
	if *calls != 2 {
		t.Fatalf("expected 2 calls, got %d", *calls)
	}
	// Should have completed much faster than 10s base delay.
	if elapsed > 2*time.Second {
		t.Fatalf("expected fast retry using RetryAfter, but took %v", elapsed)
	}
}

func TestRetryMiddleware_DefaultOptions(t *testing.T) {
	mw := NewRetryMiddleware()
	rm, ok := mw.(*RetryMiddleware)
	if !ok {
		t.Fatalf("expected *RetryMiddleware, got %T", mw)
	}
	if rm.maxRetries != 3 {
		t.Fatalf("expected default maxRetries=3, got %d", rm.maxRetries)
	}
	if rm.baseDelay != time.Second {
		t.Fatalf("expected default baseDelay=1s, got %v", rm.baseDelay)
	}
}

func TestRetryMiddleware_NoRetryOnUnknownError(t *testing.T) {
	unknownErr := errors.New("something unexpected")
	inner, calls := callCountHandler(5, unknownErr)

	mw := NewRetryMiddleware(WithMaxRetries(3), WithBaseDelay(time.Millisecond))
	handler := mw.WrapComplete(inner)

	_, err := handler(context.Background(), &Request{Model: "m", Messages: []Message{UserMessage("hi")}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "something unexpected" {
		t.Fatalf("expected original error, got: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("expected exactly 1 call (no retries for unknown errors), got %d", *calls)
	}
}

func TestRetryMiddleware_ImplementsMiddlewareInterface(t *testing.T) {
	var _ Middleware = NewRetryMiddleware()
}
