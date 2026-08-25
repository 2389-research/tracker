// ABOUTME: Builds RateLimitInfo from provider rate-limit response headers so both
// ABOUTME: the Complete and streamed (traced) paths can surface the same metadata (#617).
package llm

import (
	"net/http"
	"strconv"
	"time"
)

// atoiPtr parses a header value into an *int, returning nil when absent or invalid.
func atoiPtr(s string) *int {
	if s == "" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &n
}

// ResetKind selects how a rate-limit reset header value is interpreted.
type ResetKind int

const (
	// ResetRFC3339 parses the reset header as an absolute RFC3339 timestamp
	// (Anthropic: anthropic-ratelimit-*-reset).
	ResetRFC3339 ResetKind = iota
	// ResetDuration parses the reset header as a Go duration relative to now
	// (OpenAI: x-ratelimit-reset-*, e.g. "1s", "6m0s").
	ResetDuration
)

// RateLimitFromHeaders builds a *RateLimitInfo from a provider's rate-limit
// headers using the given header names, or nil when none are present. It lets the
// Anthropic and OpenAI adapters share one parser across their Complete and stream
// paths so a traced (production) call surfaces the same rate-limit metadata a
// direct Complete call does (#605 / #617). Providers without standard rate-limit
// headers (Gemini, openai-compat) simply pass empty names and get nil.
func RateLimitFromHeaders(h http.Header, reqRemaining, reqLimit, tokRemaining, tokLimit, reset string, resetKind ResetKind) *RateLimitInfo {
	rl := &RateLimitInfo{
		RequestsRemaining: atoiPtr(h.Get(reqRemaining)),
		RequestsLimit:     atoiPtr(h.Get(reqLimit)),
		TokensRemaining:   atoiPtr(h.Get(tokRemaining)),
		TokensLimit:       atoiPtr(h.Get(tokLimit)),
		ResetAt:           parseResetHeader(h.Get(reset), resetKind),
	}
	if rl.empty() {
		return nil
	}
	return rl
}

// parseResetHeader interprets a rate-limit reset header value, nil when absent/invalid.
func parseResetHeader(v string, kind ResetKind) *time.Time {
	if v == "" {
		return nil
	}
	switch kind {
	case ResetRFC3339:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return &t
		}
	case ResetDuration:
		if d, err := time.ParseDuration(v); err == nil {
			t := time.Now().Add(d)
			return &t
		}
	}
	return nil
}

// empty reports whether no rate-limit field was populated.
func (r *RateLimitInfo) empty() bool {
	return r.RequestsRemaining == nil && r.RequestsLimit == nil &&
		r.TokensRemaining == nil && r.TokensLimit == nil && r.ResetAt == nil
}
