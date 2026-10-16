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
