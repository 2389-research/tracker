// ABOUTME: Pins that anthropicRateLimit reads Anthropic's real anthropic-ratelimit-* headers (#617).
package anthropic

import (
	"net/http"
	"testing"
)

func TestAnthropicRateLimit_HeaderNames(t *testing.T) {
	h := http.Header{}
	h.Set("anthropic-ratelimit-requests-remaining", "5")
	h.Set("anthropic-ratelimit-requests-limit", "50")
	h.Set("anthropic-ratelimit-tokens-remaining", "38000")
	h.Set("anthropic-ratelimit-tokens-limit", "40000")

	rl := anthropicRateLimit(h)
	if rl == nil {
		t.Fatal("expected non-nil RateLimitInfo from anthropic-ratelimit-* headers")
	}
	if rl.RequestsRemaining == nil || *rl.RequestsRemaining != 5 {
		t.Errorf("RequestsRemaining = %v, want 5", rl.RequestsRemaining)
	}
	if rl.TokensLimit == nil || *rl.TokensLimit != 40000 {
		t.Errorf("TokensLimit = %v, want 40000", rl.TokensLimit)
	}
	if anthropicRateLimit(http.Header{}) != nil {
		t.Error("expected nil when no rate-limit headers present")
	}
}
