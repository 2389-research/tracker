// ABOUTME: Retry middleware with exponential backoff for the LLM client.
// ABOUTME: Retries only errors that implement Retryable() bool returning true; respects context and RetryAfter.
package llm

import (
	"context"
	"time"
)

// retryable is the interface checked to decide whether an error should be retried.
type retryable interface {
	Retryable() bool
}

// retryAfterHint is the interface checked to extract a provider-suggested retry delay.
type retryAfterHint interface {
	retryable
	GetRetryAfter() *float64
}

// RetryMiddleware retries failed requests with exponential backoff when the
// error is retryable. Non-retryable and unknown errors are returned immediately.
type RetryMiddleware struct {
	maxRetries int
	baseDelay  time.Duration
}

// RetryOption configures a RetryMiddleware.
type RetryOption func(*RetryMiddleware)

// WithMaxRetries sets the maximum number of retry attempts (default 3).
func WithMaxRetries(n int) RetryOption {
	return func(rm *RetryMiddleware) {
		rm.maxRetries = n
	}
}

// WithBaseDelay sets the base delay for exponential backoff (default 1s).
// Actual delay is baseDelay * 2^attempt.
func WithBaseDelay(d time.Duration) RetryOption {
	return func(rm *RetryMiddleware) {
		rm.baseDelay = d
	}
}

// NewRetryMiddleware creates a retry Middleware with the given options.
func NewRetryMiddleware(opts ...RetryOption) Middleware {
	rm := &RetryMiddleware{
		maxRetries: 3,
		baseDelay:  time.Second,
	}
	for _, opt := range opts {
		opt(rm)
	}
	return rm
}

// WrapComplete implements the Middleware interface.
func (rm *RetryMiddleware) WrapComplete(next CompleteHandler) CompleteHandler {
	return func(ctx context.Context, req *Request) (*Response, error) {
		resp, err := next(ctx, req)
		if err == nil {
			return resp, nil
		}
		return rm.retryLoop(ctx, req, next, err)
	}
}

// retryLoop executes the retry attempts after an initial failure.
func (rm *RetryMiddleware) retryLoop(ctx context.Context, req *Request, next CompleteHandler, firstErr error) (*Response, error) {
	err := firstErr
	for attempt := 0; attempt < rm.maxRetries; attempt++ {
		if !isRetryable(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(rm.backoffDelay(attempt, err)):
		}
		resp, retryErr := next(ctx, req)
		if retryErr == nil {
			return resp, nil
		}
		err = retryErr
	}
	return nil, err
}

// isRetryable checks whether an error should be retried.
func isRetryable(err error) bool {
	var r retryable
	if ok := asRetryable(err, &r); ok {
		return r.Retryable()
	}
	return false
}

// asRetryable extracts the retryable interface from an error chain.
func asRetryable(err error, target *retryable) bool {
	for err != nil {
		if r, ok := err.(retryable); ok {
			*target = r
			return true
		}
		if u, ok := err.(interface{ Unwrap() error }); ok {
			err = u.Unwrap()
		} else {
			return false
		}
	}
	return false
}

// backoffDelay computes the delay for the given retry attempt. If the error
// carries a RetryAfter hint (e.g. from a RateLimitError), that value is used
// instead of the computed exponential backoff.
func (rm *RetryMiddleware) backoffDelay(attempt int, err error) time.Duration {
	// Check for RetryAfter on the error (RateLimitError embeds ProviderError
	// which has a RetryAfter field).
	if rle, ok := err.(*RateLimitError); ok && rle.RetryAfter != nil {
		return time.Duration(*rle.RetryAfter * float64(time.Second))
	}

	// Exponential backoff: baseDelay * 2^attempt
	delay := rm.baseDelay
	for i := 0; i < attempt; i++ {
		delay *= 2
	}
	return delay
}
