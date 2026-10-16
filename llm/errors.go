// ABOUTME: Error type hierarchy for the unified LLM client library.
// ABOUTME: Defines provider errors, retryability, and HTTP status and error code mapping.
package llm

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SDKError is the base error type for all errors in the LLM package.
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

// StreamErrorCode is the code of a stream error event. OpenAI sends a string;
// a number decodes as written (500 gives "500"), and any other JSON value as
// no code, so an odd code never stops the event, and the server's message,
// from being read.
type StreamErrorCode string

// UnmarshalJSON decodes a JSON string or number; other values give "".
func (c *StreamErrorCode) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*c = StreamErrorCode(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		*c = StreamErrorCode(n)
		return nil
	}
	*c = ""
	return nil
}

// ErrorFromOpenAICode maps an OpenAI error code, as sent in a stream error
// event by OpenAI and OpenAI-compatible servers, to the appropriate error type.
// It is ErrorFromOpenAIStreamError without an error type.
func ErrorFromOpenAICode(code, message, provider string) error {
	return ErrorFromOpenAIStreamError(code, "", message, provider)
}

// ErrorFromOpenAIStreamError maps a stream error event's code and type to the
// appropriate error type, in this order:
//
//  1. A code this function knows (insufficient_quota, invalid_api_key, ...).
//  2. A code that is an HTTP status ("401", or the number 400 sent by vLLM and
//     OpenRouter), classified exactly as that status is over HTTP, so a 429
//     stays a rate limit unless its text reports exhaustion.
//  3. The error type, for servers that send `code: null` and put the class in
//     `type` (invalid_request_error, authentication_error, ...).
//  4. Anything else: the server accepted the request before the stream failed,
//     so it is a retryable ServerError (#684).
//
// A step 2-4 result that would be retried yields to its text: credit or quota
// exhaustion (isExhaustionText) is a QuotaExceededError, so the run pauses
// (#487) instead of retrying against an empty balance, and a subscription
// usage limit is non-retryable, so codergen pauses on it at once.
func ErrorFromOpenAIStreamError(code, errType, message, provider string) error {
	base := ProviderError{
		SDKError:  SDKError{Msg: message},
		Provider:  provider,
		ErrorCode: code,
	}
	if err := errorForKnownCode(code, base); err != nil {
		return err
	}
	status, isStatus := httpStatusCode(code)
	if isStatus {
		base.StatusCode = status
	}
	err := errorForStatusOrType(status, isStatus, errType, base)
	if r, ok := err.(interface{ Retryable() bool }); ok && r.Retryable() {
		return errorForRetryableText(err, base)
	}
	return err
}

// errorForStatusOrType classifies a stream error by its HTTP-status code, else
// its type, else as a retryable ServerError.
func errorForStatusOrType(status int, isStatus bool, errType string, base ProviderError) error {
	if isStatus {
		return errorForBase(status, base)
	}
	if err := errorForErrorType(errType, base); err != nil {
		return err
	}
	return &ServerError{ProviderError: base}
}

// errorForKnownCode maps the error codes this package knows to typed errors.
// Returns nil for other codes.
func errorForKnownCode(code string, base ProviderError) error {
	if err := errorForAuthQuotaCode(code, base); err != nil {
		return err
	}
	return errorForRequestServerCode(code, base)
}

// httpStatusCode reports whether code is an HTTP status (100-599) written in
// decimal, as gateways send when they reuse the status as the error code.
func httpStatusCode(code string) (int, bool) {
	if len(code) != 3 || strings.Trim(code, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(code)
	return n, err == nil && n >= 100 && n <= 599
}

// errorForErrorType maps a stream error's type to a typed error. It covers the
// types OpenAI-style and Anthropic-style servers send alongside a null code,
// then the known codes, which some servers send as the type. Returns nil for
// other types.
func errorForErrorType(errType string, base ProviderError) error {
	switch errType {
	case "auth_error":
		return &AuthenticationError{ProviderError: base}
	case "permission_error":
		return &AccessDeniedError{ProviderError: base}
	case "not_found_error":
		return &NotFoundError{ProviderError: base}
	case "rate_limit_error", "rate_limit_exceeded":
		return errorFor429(base)
	case "billing_error":
		// Anthropic's HTTP 402 type: credit exhaustion, a resumable pause (#487).
		return &QuotaExceededError{ProviderError: base}
	case "request_too_large":
		// Anthropic's HTTP 413 type: a retry would re-send the same request.
		return &ContextLengthError{ProviderError: base}
	}
	return errorForKnownCode(errType, base)
}

// errorForAuthQuotaCode maps auth, quota, and not-found error codes to typed
// errors. Returns nil for other codes.
func errorForAuthQuotaCode(code string, base ProviderError) error {
	switch code {
	case "insufficient_quota", "billing_hard_limit_reached":
		return &QuotaExceededError{ProviderError: base}
	case "invalid_api_key", "authentication_error":
		return &AuthenticationError{ProviderError: base}
	case "model_not_found":
		return &NotFoundError{ProviderError: base}
	}
	return nil
}

// errorForRequestServerCode maps request, content, rate-limit, and server error
// codes to typed errors. Returns nil for other codes.
func errorForRequestServerCode(code string, base ProviderError) error {
	switch code {
	case "invalid_request_error", "invalid_request",
		// Request-side codes OpenAI's Responses API documents for a failed
		// response. A retry would send the same request, so they fail at once.
		"invalid_prompt", "data_residency_mismatch", "invalid_image",
		"invalid_image_format", "invalid_base64_image", "invalid_image_url",
		"image_too_large", "image_too_small", "image_parse_error",
		"invalid_image_mode", "image_file_too_large", "unsupported_image_media_type",
		"empty_image_file", "image_file_not_found":
		return &InvalidRequestError{ProviderError: base}
	case "context_length_exceeded":
		return &ContextLengthError{ProviderError: base}
	case "content_filter", "content_policy_violation",
		// The Responses API's policy codes.
		"image_content_policy_violation", "bio_policy", "misalignment_policy_violation":
		return &ContentFilterError{ProviderError: base}
	case "rate_limit_exceeded":
		return &RateLimitError{ProviderError: base}
	case "server_error", "internal_error":
		return &ServerError{ProviderError: base}
	}
	return nil
}

// errorForRetryableText returns err, a stream error that would be retried,
// unless its text says otherwise. Exhaustion text maps to QuotaExceededError,
// so the run pauses (#487): IsBillingError never counts a retryable error as
// billing. A subscription usage limit maps to a non-retryable error, so
// codergen pauses on it (IsUsageLimit) at once instead of the retry middleware
// re-hitting the same cap.
func errorForRetryableText(err error, base ProviderError) error {
	if isExhaustionText(base.Msg) {
		return &QuotaExceededError{ProviderError: base}
	}
	if hasUsageLimitSignal(base.Msg) {
		return &InvalidRequestError{ProviderError: base}
	}
	return err
}
