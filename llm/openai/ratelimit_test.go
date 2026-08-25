// ABOUTME: Pins that openaiRateLimit reads OpenAI's real x-ratelimit-* headers (#617).
package openai

import (
	"net/http"
	"testing"
)

func TestOpenAIRateLimit_HeaderNames(t *testing.T) {
	h := http.Header{}
	h.Set("x-ratelimit-remaining-requests", "199")
	h.Set("x-ratelimit-limit-requests", "200")
	h.Set("x-ratelimit-remaining-tokens", "149000")
	h.Set("x-ratelimit-limit-tokens", "150000")

	rl := openaiRateLimit(h)
	if rl == nil {
		t.Fatal("expected non-nil RateLimitInfo from x-ratelimit-* headers")
	}
	if rl.RequestsRemaining == nil || *rl.RequestsRemaining != 199 {
		t.Errorf("RequestsRemaining = %v, want 199", rl.RequestsRemaining)
	}
	if rl.TokensLimit == nil || *rl.TokensLimit != 150000 {
		t.Errorf("TokensLimit = %v, want 150000", rl.TokensLimit)
	}
	if openaiRateLimit(http.Header{}) != nil {
		t.Error("expected nil when no rate-limit headers present")
	}
}
