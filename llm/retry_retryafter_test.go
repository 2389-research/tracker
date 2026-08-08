// ABOUTME: Tests that the retry middleware honors a server Retry-After header (#549).
package llm

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	h := http.Header{}
	if ParseRetryAfter(h) != nil {
		t.Error("absent header should parse to nil")
	}
	h.Set("Retry-After", "5")
	if got := ParseRetryAfter(h); got == nil || *got != 5 {
		t.Errorf("seconds form = %v, want 5", got)
	}
	h.Set("Retry-After", "-3")
	if got := ParseRetryAfter(h); got == nil || *got != 0 {
		t.Errorf("negative clamps to 0, got %v", got)
	}
	h.Set("Retry-After", "not-a-number")
	if ParseRetryAfter(h) != nil {
		t.Error("garbage should parse to nil (fall back to local backoff)")
	}
	h.Set("Retry-After", time.Now().Add(10*time.Second).UTC().Format(http.TimeFormat))
	if got := ParseRetryAfter(h); got == nil || *got < 8 || *got > 11 {
		t.Errorf("HTTP-date form = %v, want ~10s", got)
	}
}

func TestErrorFromStatusCodeRetryAfter_PopulatesRateLimit(t *testing.T) {
	ra := 7.0
	err := ErrorFromStatusCodeRetryAfter(429, "slow down", "openai", &ra)
	rle, ok := err.(*RateLimitError)
	if !ok {
		t.Fatalf("want *RateLimitError, got %T", err)
	}
	if rle.RetryAfter == nil || *rle.RetryAfter != 7 {
		t.Errorf("RetryAfter = %v, want 7", rle.RetryAfter)
	}
}

func TestBackoffDelay_HonorsAndCapsRetryAfter(t *testing.T) {
	rm := &RetryMiddleware{baseDelay: time.Second}

	ra := 5.0
	if d := rm.backoffDelay(0, ErrorFromStatusCodeRetryAfter(429, "x", "openai", &ra)); d != 5*time.Second {
		t.Errorf("honored delay = %v, want 5s", d)
	}
	big := 100000.0
	if d := rm.backoffDelay(0, ErrorFromStatusCodeRetryAfter(429, "x", "openai", &big)); d != maxRetryAfter {
		t.Errorf("huge Retry-After = %v, want cap %v", d, maxRetryAfter)
	}
	// A WRAPPED RateLimitError must still be honored (errors.As, not a bare assert).
	wrapped := fmt.Errorf("provider call failed: %w", ErrorFromStatusCodeRetryAfter(429, "x", "openai", &ra))
	if d := rm.backoffDelay(3, wrapped); d != 5*time.Second {
		t.Errorf("wrapped RateLimitError delay = %v, want 5s (errors.As)", d)
	}
	// No Retry-After → local exponential backoff (base 1s × 2^2).
	if d := rm.backoffDelay(2, ErrorFromStatusCode(429, "x", "openai")); d != 4*time.Second {
		t.Errorf("no-hint delay = %v, want 4s exponential", d)
	}
}
