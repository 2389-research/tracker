// ABOUTME: Tests the subscription usage-limit classifier and reset-time parser.
package llm

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestIsUsageLimit(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{
			"claude subscription usage limit",
			errors.New("Claude AI usage limit reached. Your limit will reset at 2026-08-24T15:00:00Z"),
			true,
		},
		{
			"usage limit reached phrasing",
			errors.New("you've reached your usage limit, resets at 3pm"),
			true,
		},
		// insufficient_quota / credit-balance are IsBillingError, NOT usage-limit.
		{"insufficient_quota is billing not usage-limit", &QuotaExceededError{
			ProviderError: ProviderError{SDKError: SDKError{Msg: "insufficient_quota: you exceeded your current quota"}},
		}, false},
		{"credit balance is billing not usage-limit", errors.New("Your credit balance is too low to access the API"), false},
		// A plain retryable 429 rate limit must NOT be classified as a usage-limit,
		// or it would be diverted out of the retry path.
		{"plain retryable 429 rate limit", ErrorFromStatusCode(429, "rate limit exceeded", "anthropic"), false},
		{"too many requests", errors.New("429 Too Many Requests"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsUsageLimit(tt.err); got != tt.want {
				t.Errorf("IsUsageLimit(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestUsageLimitResetAt(t *testing.T) {
	t.Run("rfc3339", func(t *testing.T) {
		want := time.Date(2026, 8, 24, 15, 0, 0, 0, time.UTC)
		got, ok := UsageLimitResetAt(errors.New("usage limit reached, resets at 2026-08-24T15:00:00Z"))
		if !ok {
			t.Fatal("expected a reset time to be parsed")
		}
		if !got.Equal(want) {
			t.Errorf("reset = %v, want %v", got, want)
		}
	})

	t.Run("unix epoch after pipe", func(t *testing.T) {
		want := time.Unix(1755990000, 0).UTC()
		got, ok := UsageLimitResetAt(fmt.Errorf("Claude AI usage limit reached|%d", 1755990000))
		if !ok {
			t.Fatal("expected a reset time to be parsed")
		}
		if !got.Equal(want) {
			t.Errorf("reset = %v, want %v", got, want)
		}
	})

	t.Run("no timestamp is unknown", func(t *testing.T) {
		if got, ok := UsageLimitResetAt(errors.New("usage limit reached")); ok {
			t.Errorf("expected no reset time, got %v", got)
		}
	})

	t.Run("nil", func(t *testing.T) {
		if _, ok := UsageLimitResetAt(nil); ok {
			t.Error("expected no reset time for nil error")
		}
	})
}
