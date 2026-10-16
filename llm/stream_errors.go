// ABOUTME: Classification of in-stream error events (OpenAI and OpenAI-compatible SSE).
// ABOUTME: Maps a stream error's code, type, and text to the typed errors in errors.go.
package llm

import (
	"encoding/json"
	"strconv"
	"strings"
)

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
// Any result that would be retried yields to its text: credit or quota
// exhaustion (isExhaustionText) is a QuotaExceededError, so the run pauses
// (#487) instead of retrying against an empty balance, and a subscription
// usage limit is a non-retryable UsageLimitError, so codergen pauses on it at
// once. A step-4 error, which has no code, status or type to say it is a rate
// limit, also pauses on any billing signal ("quota exceeded") without a
// rate-limit marker (errorForUnclassified).
func ErrorFromOpenAIStreamError(code, errType, message, provider string) error {
	base := ProviderError{
		SDKError:  SDKError{Msg: message},
		Provider:  provider,
		ErrorCode: code,
	}
	if status, ok := httpStatusCode(code); ok {
		base.StatusCode = status
	}
	err := classifyStreamError(code, errType, base)
	if err == nil {
		return errorForUnclassified(base)
	}
	if r, ok := err.(interface{ Retryable() bool }); ok && r.Retryable() {
		return errorForRetryableText(err, base)
	}
	return err
}

// classifyStreamError runs steps 1-3 of ErrorFromOpenAIStreamError: the known
// code, else the HTTP-status code, else the type. Returns nil when none of them
// classifies the error.
func classifyStreamError(code, errType string, base ProviderError) error {
	if err := errorForKnownCode(code, base); err != nil {
		return err
	}
	if base.StatusCode != 0 {
		return errorForBase(base.StatusCode, base)
	}
	return errorForErrorType(errType, base)
}

// errorForUnclassified is step 4: a stream error with no code, status or type
// this package knows is a retryable ServerError (#684), unless its text says
// otherwise. With nothing marking it a rate limit, any billing signal without
// a rateLimitMarker is exhaustion, so a bare "Quota exceeded" pauses (#487) as
// it did before #684; Gemini's per-minute wording carries a marker or arrives
// with a 429 / RESOURCE_EXHAUSTED code, which steps 1-3 classify.
func errorForUnclassified(base ProviderError) error {
	if hasBillingSignal(base.Msg) && !containsAny(strings.ToLower(base.Msg), rateLimitMarkers) {
		return &QuotaExceededError{ProviderError: base}
	}
	return errorForRetryableText(&ServerError{ProviderError: base}, base)
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
	case "insufficient_quota", "billing_hard_limit_reached", "billing_not_active":
		return &QuotaExceededError{ProviderError: base}
	case "invalid_api_key", "authentication_error":
		return &AuthenticationError{ProviderError: base}
	case "usage_not_included":
		// The Codex backend's plan code (ChatGPT mode): never retried, as
		// over HTTP (openai.chatgptPlanError).
		return &AccessDeniedError{ProviderError: base}
	case "usage_limit_reached":
		return usageLimitStreamError(base)
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
// billing. A subscription usage limit maps to a non-retryable UsageLimitError,
// so codergen pauses on it (IsUsageLimit) at once instead of the retry
// middleware re-hitting the same cap.
func errorForRetryableText(err error, base ProviderError) error {
	if isExhaustionText(base.Msg) {
		return &QuotaExceededError{ProviderError: base}
	}
	if hasUsageLimitSignal(base.Msg) {
		return &UsageLimitError{ProviderError: base}
	}
	return err
}

// usageLimitStreamError builds the error for the Codex backend's
// usage_limit_reached code (ChatGPT mode): a non-retryable UsageLimitError, as
// over HTTP (openai.chatgptPlanError). Its text must satisfy IsUsageLimit, the
// test codergen's pause routing uses, so a message that does not say "usage
// limit" gets the words appended; the server's own message is kept.
func usageLimitStreamError(base ProviderError) error {
	if !hasUsageLimitSignal(base.Msg) {
		base.Msg += ": usage limit reached"
	}
	return &UsageLimitError{ProviderError: base}
}
