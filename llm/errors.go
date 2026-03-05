// ABOUTME: Error type hierarchy for the unified LLM client library.
// ABOUTME: Defines provider errors, retryability, and HTTP status code mapping.
package llm

import "encoding/json"

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
	base := ProviderError{
		SDKError:   SDKError{Msg: message},
		Provider:   provider,
		StatusCode: statusCode,
	}

	switch statusCode {
	case 400:
		return &InvalidRequestError{ProviderError: base}
	case 401:
		return &AuthenticationError{ProviderError: base}
	case 403:
		return &AccessDeniedError{ProviderError: base}
	case 404:
		return &NotFoundError{ProviderError: base}
	case 408:
		return &RequestTimeoutError{ProviderError: base}
	case 413:
		return &ContextLengthError{ProviderError: base}
	case 429:
		return &RateLimitError{ProviderError: base}
	case 500, 502, 503, 504:
		return &ServerError{ProviderError: base}
	default:
		return &ServerError{ProviderError: base}
	}
}
