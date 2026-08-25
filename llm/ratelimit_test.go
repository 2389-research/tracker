// ABOUTME: Tests RateLimitFromHeaders — the shared provider rate-limit header parser (#617).
package llm

import (
	"net/http"
	"testing"
	"time"
)

func TestRateLimitFromHeaders_Anthropic(t *testing.T) {
	h := http.Header{}
	h.Set("anthropic-ratelimit-requests-remaining", "49")
	h.Set("anthropic-ratelimit-requests-limit", "50")
	h.Set("anthropic-ratelimit-tokens-remaining", "9000")
	h.Set("anthropic-ratelimit-tokens-limit", "10000")
	h.Set("anthropic-ratelimit-requests-reset", "2026-08-25T15:00:00Z")

	rl := RateLimitFromHeaders(h,
		"anthropic-ratelimit-requests-remaining", "anthropic-ratelimit-requests-limit",
		"anthropic-ratelimit-tokens-remaining", "anthropic-ratelimit-tokens-limit",
		"anthropic-ratelimit-requests-reset", ResetRFC3339)
	if rl == nil {
		t.Fatal("expected non-nil RateLimitInfo")
	}
	if rl.RequestsRemaining == nil || *rl.RequestsRemaining != 49 {
		t.Errorf("RequestsRemaining = %v, want 49", rl.RequestsRemaining)
	}
	if rl.TokensLimit == nil || *rl.TokensLimit != 10000 {
		t.Errorf("TokensLimit = %v, want 10000", rl.TokensLimit)
	}
	want, _ := time.Parse(time.RFC3339, "2026-08-25T15:00:00Z")
	if rl.ResetAt == nil || !rl.ResetAt.Equal(want) {
		t.Errorf("ResetAt = %v, want %v", rl.ResetAt, want)
	}
}

func TestRateLimitFromHeaders_OpenAIDuration(t *testing.T) {
	h := http.Header{}
	h.Set("x-ratelimit-remaining-requests", "199")
	h.Set("x-ratelimit-limit-requests", "200")
	h.Set("x-ratelimit-reset-requests", "6m0s")

	before := time.Now()
	rl := RateLimitFromHeaders(h,
		"x-ratelimit-remaining-requests", "x-ratelimit-limit-requests",
		"x-ratelimit-remaining-tokens", "x-ratelimit-limit-tokens",
		"x-ratelimit-reset-requests", ResetDuration)
	if rl == nil || rl.RequestsRemaining == nil || *rl.RequestsRemaining != 199 {
		t.Fatalf("RequestsRemaining parse failed: %+v", rl)
	}
	if rl.TokensRemaining != nil {
		t.Errorf("TokensRemaining should be nil (header absent), got %v", rl.TokensRemaining)
	}
	if rl.ResetAt == nil || rl.ResetAt.Before(before.Add(5*time.Minute)) {
		t.Errorf("ResetAt = %v, want ~now+6m", rl.ResetAt)
	}
}

func TestRateLimitFromHeaders_AbsentReturnsNil(t *testing.T) {
	if rl := RateLimitFromHeaders(http.Header{}, "a", "b", "c", "d", "e", ResetRFC3339); rl != nil {
		t.Errorf("expected nil when no headers present, got %+v", rl)
	}
}
