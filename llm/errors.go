// ABOUTME: Error type hierarchy for the unified LLM client library.
// ABOUTME: Defines provider errors, retryability, and HTTP status and error code mapping.
package llm

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SDKError is the base error type for all errors in the LLM package.
// ErrNoProviders is the cause of the ConfigurationError NewClient returns when
// no provider is configured. A run whose nodes all use claude-code, acp or
// backend: auto may tolerate it; any other client error stays fatal.
var ErrNoProviders = errors.New("no providers configured")

type SDKError struct {
	Msg   string
	Cause error
}

func (e *SDKError) Error() string {
	return e.Msg
}

func (e *SDKError) Unwrap() error {
	return e.Cause
}

// ProviderErrorInterface describes errors originating from an LLM provider.
type ProviderErrorInterface interface {
	error
	Retryable() bool
	GetProvider() string
	GetStatusCode() int
}

// ProviderError is the base type for errors returned by LLM providers.
type ProviderError struct {
	SDKError
	Provider   string
	StatusCode int
	ErrorCode  string
	RetryAfter *float64
	RawBody    json.RawMessage
}

func (e *ProviderError) GetProvider() string {
	return e.Provider
}

func (e *ProviderError) GetStatusCode() int {
	return e.StatusCode
}

// --- Non-retryable provider errors ---

// AuthenticationError indicates invalid or missing credentials.
type AuthenticationError struct {
	ProviderError
}

func (e *AuthenticationError) Retryable() bool { return false }

// AccessDeniedError indicates insufficient permissions.
type AccessDeniedError struct {
	ProviderError
}

func (e *AccessDeniedError) Retryable() bool { return false }

// NotFoundError indicates the requested resource does not exist.
type NotFoundError struct {
	ProviderError
}

func (e *NotFoundError) Retryable() bool { return false }

// InvalidRequestError indicates a malformed or invalid request.
type InvalidRequestError struct {
	ProviderError
}

func (e *InvalidRequestError) Retryable() bool { return false }

// ContextLengthError indicates the request exceeded the model's context window.
type ContextLengthError struct {
	ProviderError
}

func (e *ContextLengthError) Retryable() bool { return false }

// QuotaExceededError indicates the account's quota has been exhausted.
type QuotaExceededError struct {
	ProviderError
}

func (e *QuotaExceededError) Retryable() bool { return false }

// ContentFilterError indicates the request or response was blocked by a content filter.
type ContentFilterError struct {
	ProviderError
}

func (e *ContentFilterError) Retryable() bool { return false }

// UsageLimitError indicates a subscription usage-limit cap was reached (e.g.
// a ChatGPT-subscription Codex backend 429 with error.type
// "usage_limit_reached", #590). It is never retried — the retry middleware
// would just re-hit the same cap — and is deliberately NOT an IsBillingError:
// a usage-limit is a plan cap that clears on a schedule, not credit/quota
// exhaustion. Its message must satisfy llm.IsUsageLimit and, when ResetsAt is
// known, llm.UsageLimitResetAt, so the existing message-sniffing in those
// helpers (and the recoverablePause routing built on them) keeps working
// without a type switch.
type UsageLimitError struct {
	ProviderError
	ResetsAt time.Time // zero when unknown
	PlanType string
}

func (e *UsageLimitError) Retryable() bool { return false }

// --- Retryable provider errors ---

// RateLimitError indicates the request was rate limited by the provider.
type RateLimitError struct {
	ProviderError
}

func (e *RateLimitError) Retryable() bool { return true }

// ServerError indicates a server-side failure from the provider.
type ServerError struct {
	ProviderError
}

func (e *ServerError) Retryable() bool { return true }

// RequestTimeoutError indicates the provider timed out processing the request.
type RequestTimeoutError struct {
	ProviderError
}

func (e *RequestTimeoutError) Retryable() bool { return true }

// --- Retryable non-provider errors ---

// NetworkError indicates a network-level failure (DNS, TCP, TLS).
type NetworkError struct {
	SDKError
}

func (e *NetworkError) Retryable() bool { return true }

// StreamError indicates a failure during response streaming.
type StreamError struct {
	SDKError
}

func (e *StreamError) Retryable() bool { return true }

// --- Configuration error ---

// ConfigurationError indicates a problem with the client configuration.
type ConfigurationError struct {
	SDKError
}

// --- Non-provider SDK errors ---

// AbortError indicates the operation was explicitly aborted.
type AbortError struct {
	SDKError
}

// InvalidToolCallError indicates the model produced an invalid tool call.
type InvalidToolCallError struct {
	SDKError
}

// NoObjectGeneratedError indicates structured output generation failed.
type NoObjectGeneratedError struct {
	SDKError
}

// ErrorFromStatusCode maps an HTTP status code to the appropriate error type.
func ErrorFromStatusCode(statusCode int, message, provider string) error {
	return ErrorFromStatusCodeRetryAfter(statusCode, message, provider, nil)
}

// ErrorFromStatusCodeRetryAfter is ErrorFromStatusCode with a server-requested
// retry delay (seconds) parsed from the `Retry-After` header. It populates
// ProviderError.RetryAfter so the retry middleware honors the server's backoff
// on a 429/503 instead of only its local exponential schedule (#549). Adapters
// that hold the http.Response pass ParseRetryAfter(resp.Header).
func ErrorFromStatusCodeRetryAfter(statusCode int, message, provider string, retryAfter *float64) error {
	base := ProviderError{
		SDKError:   SDKError{Msg: message},
		Provider:   provider,
		StatusCode: statusCode,
		RetryAfter: retryAfter,
	}
	return errorForBase(statusCode, base)
}

// ParseRetryAfter extracts a non-negative retry delay in seconds from a
// `Retry-After` header, supporting both forms the HTTP spec allows: an integer
// (or float) number of seconds, and an HTTP-date. Returns nil when the header
// is absent or unparseable, so the caller falls back to local backoff.
func ParseRetryAfter(h http.Header) *float64 {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return nil
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		if secs < 0 {
			secs = 0
		}
		return &secs
	}
	if t, err := http.ParseTime(v); err == nil {
		secs := time.Until(t).Seconds()
		if secs < 0 {
			secs = 0
		}
		return &secs
	}
	return nil
}

// errorForBase selects the concrete error type for the given status code and base.
func errorForBase(statusCode int, base ProviderError) error {
	if err := errorForClientStatus(statusCode, base); err != nil {
		return err
	}
	return errorForServerStatus(statusCode, base)
}

// errorForClientStatus maps 4xx status codes to typed errors. Returns nil for non-4xx codes.
func errorForClientStatus(statusCode int, base ProviderError) error {
	switch statusCode {
	case 400:
		return &InvalidRequestError{ProviderError: base}
	case 401:
		return &AuthenticationError{ProviderError: base}
	case 402:
		// Payment Required: credit exhaustion, a resumable pause (#487).
		return &QuotaExceededError{ProviderError: base}
	case 403:
		return &AccessDeniedError{ProviderError: base}
	case 404:
		return &NotFoundError{ProviderError: base}
	}
	return errorForLimitStatus(statusCode, base)
}

// errorForLimitStatus maps the timeout, size and rate-limit 4xx status codes
// to typed errors. Returns nil for other codes.
func errorForLimitStatus(statusCode int, base ProviderError) error {
	switch statusCode {
	case 408:
		return &RequestTimeoutError{ProviderError: base}
	case 413:
		return &ContextLengthError{ProviderError: base}
	case 429:
		return errorFor429(base)
	}
	return nil
}

// errorFor429 distinguishes a rate limit (retryable) from account credit/quota
// EXHAUSTION (OpenAI/compat return insufficient_quota as HTTP 429), which is
// non-retryable and resumable-pausable (#487/#486) — not a rate limit to retry
// against an empty balance. Matches only exhaustion text (isExhaustionText) so
// a rate-limit 429 that merely mentions "quota" stays retryable; the stream
// path uses the same test, so both paths agree.
func errorFor429(base ProviderError) error {
	if isExhaustionText(base.Msg) {
		return &QuotaExceededError{ProviderError: base}
	}
	return &RateLimitError{ProviderError: base}
}

// exhaustionPhrases mark credit or quota exhaustion wherever they appear.
// "too low to access" is Anthropic's credit-balance wording.
var exhaustionPhrases = []string{
	"insufficient_quota",
	"insufficient quota",
	"credit balance",
	"too low to access",
	"billing hard limit",
	"credits are depleted", // Gemini: "Your prepayment credits are depleted" (429 RESOURCE_EXHAUSTED) — #722
}

// rateLimitMarkers mark a per-minute throttle. Gemini's 429 text is OpenAI's
// exhausted-quota wording plus a link to its rate-limits page and, in its
// body, the RESOURCE_EXHAUSTED status, so the wording counts as exhaustion only
// without one of these.
var rateLimitMarkers = []string{"rate-limit", "rate limit", "per minute", "retry in", "resource_exhausted"}

// isExhaustionText reports whether an error's text signals account credit or
// quota exhaustion (a non-retryable, pausable condition) rather than a rate
// limit: one of the exhaustionPhrases, or OpenAI's "exceeded your current
// quota" without a rateLimitMarker. A bare "quota exceeded" never counts:
// Gemini uses it for per-minute limits.
func isExhaustionText(msg string) bool {
	m := strings.ToLower(msg)
	if containsAny(m, exhaustionPhrases) {
		return true
	}
	return strings.Contains(m, "exceeded your current quota") && !containsAny(m, rateLimitMarkers)
}

// containsAny reports whether s contains any of subs.
func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// errorForServerStatus maps 5xx status codes to typed errors, falling back for unknowns.
func errorForServerStatus(statusCode int, base ProviderError) error {
	switch statusCode {
	case 500, 502, 503, 504:
		return &ServerError{ProviderError: base}
	}
	return errorForUnknownStatus(statusCode, base)
}

// errorForUnknownStatus returns the appropriate error for unrecognized status codes.
func errorForUnknownStatus(statusCode int, base ProviderError) error {
	if statusCode >= 400 && statusCode < 500 {
		return &InvalidRequestError{ProviderError: base}
	}
	return &ServerError{ProviderError: base}
}
