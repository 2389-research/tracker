// ABOUTME: Tests for the LLM error type hierarchy.
// ABOUTME: Validates retryability, error wrapping, and HTTP status code mapping.
package llm

import (
	"errors"
	"testing"
)

func TestSDKError_ErrorMessage(t *testing.T) {
	err := &SDKError{Msg: "something broke"}
	if err.Error() != "something broke" {
		t.Errorf("expected 'something broke', got %q", err.Error())
	}
}

func TestSDKError_Unwrap(t *testing.T) {
	cause := errors.New("root cause")
	err := &SDKError{Msg: "wrapper", Cause: cause}
	if !errors.Is(err, cause) {
		t.Error("expected Unwrap to return the cause")
	}
}

func TestRateLimitError_IsRetryable(t *testing.T) {
	err := &RateLimitError{}
	if !err.Retryable() {
		t.Error("RateLimitError should be retryable")
	}
}

func TestAuthenticationError_IsNotRetryable(t *testing.T) {
	err := &AuthenticationError{}
	if err.Retryable() {
		t.Error("AuthenticationError should not be retryable")
	}
}

func TestProviderError_ImplementsInterface(t *testing.T) {
	err := &RateLimitError{
		ProviderError: ProviderError{
			SDKError:   SDKError{Msg: "rate limited"},
			Provider:   "openai",
			StatusCode: 429,
		},
	}
	var pe ProviderErrorInterface = err
	if pe.GetProvider() != "openai" {
		t.Errorf("expected provider 'openai', got %q", pe.GetProvider())
	}
	if pe.GetStatusCode() != 429 {
		t.Errorf("expected status 429, got %d", pe.GetStatusCode())
	}
}

func TestNonRetryableErrors(t *testing.T) {
	tests := []struct {
		name string
		err  ProviderErrorInterface
	}{
		{"AuthenticationError", &AuthenticationError{}},
		{"AccessDeniedError", &AccessDeniedError{}},
		{"NotFoundError", &NotFoundError{}},
		{"InvalidRequestError", &InvalidRequestError{}},
		{"ContextLengthError", &ContextLengthError{}},
		{"QuotaExceededError", &QuotaExceededError{}},
		{"ContentFilterError", &ContentFilterError{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Retryable() {
				t.Errorf("%s should not be retryable", tt.name)
			}
		})
	}
}

func TestRetryableErrors(t *testing.T) {
	tests := []struct {
		name string
		err  ProviderErrorInterface
	}{
		{"RateLimitError", &RateLimitError{}},
		{"ServerError", &ServerError{}},
		{"RequestTimeoutError", &RequestTimeoutError{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.err.Retryable() {
				t.Errorf("%s should be retryable", tt.name)
			}
		})
	}
}

func TestNetworkAndStreamErrors_AreRetryable(t *testing.T) {
	netErr := &NetworkError{SDKError: SDKError{Msg: "connection reset"}}
	if !netErr.Retryable() {
		t.Error("NetworkError should be retryable")
	}

	streamErr := &StreamError{SDKError: SDKError{Msg: "stream interrupted"}}
	if !streamErr.Retryable() {
		t.Error("StreamError should be retryable")
	}
}

func TestErrorFromStatusCode(t *testing.T) {
	tests := []struct {
		status     int
		expectType string
		retryable  bool
	}{
		{400, "*llm.InvalidRequestError", false},
		{401, "*llm.AuthenticationError", false},
		{403, "*llm.AccessDeniedError", false},
		{404, "*llm.NotFoundError", false},
		{408, "*llm.RequestTimeoutError", true},
		{413, "*llm.ContextLengthError", false},
		{429, "*llm.RateLimitError", true},
		{500, "*llm.ServerError", true},
		{502, "*llm.ServerError", true},
		{503, "*llm.ServerError", true},
		{504, "*llm.ServerError", true},
		{418, "*llm.InvalidRequestError", false}, // unknown 4xx defaults to non-retryable
		{522, "*llm.ServerError", true},           // unknown 5xx defaults to retryable
	}
	for _, tt := range tests {
		t.Run(string(rune('0'+tt.status/100))+"xx_"+string(rune('0'+tt.status%10)), func(t *testing.T) {
			err := ErrorFromStatusCode(tt.status, "test message", "test-provider")

			// Check it implements ProviderErrorInterface.
			pe, ok := err.(ProviderErrorInterface)
			if !ok {
				t.Fatalf("expected ProviderErrorInterface, got %T", err)
			}

			if pe.Retryable() != tt.retryable {
				t.Errorf("status %d: expected retryable=%v, got %v", tt.status, tt.retryable, pe.Retryable())
			}

			if pe.GetStatusCode() != tt.status {
				t.Errorf("status %d: expected status code %d, got %d", tt.status, tt.status, pe.GetStatusCode())
			}

			if pe.GetProvider() != "test-provider" {
				t.Errorf("expected provider 'test-provider', got %q", pe.GetProvider())
			}
		})
	}
}

func TestConfigurationError_IsSDKError(t *testing.T) {
	err := &ConfigurationError{SDKError: SDKError{Msg: "missing API key"}}
	if err.Error() != "missing API key" {
		t.Errorf("expected 'missing API key', got %q", err.Error())
	}
}

func TestAbortError_IsSDKError(t *testing.T) {
	err := &AbortError{SDKError: SDKError{Msg: "aborted"}}
	if err.Error() != "aborted" {
		t.Errorf("expected 'aborted', got %q", err.Error())
	}
}

func TestInvalidToolCallError_IsSDKError(t *testing.T) {
	err := &InvalidToolCallError{SDKError: SDKError{Msg: "bad tool call"}}
	if err.Error() != "bad tool call" {
		t.Errorf("expected 'bad tool call', got %q", err.Error())
	}
}

func TestNoObjectGeneratedError_IsSDKError(t *testing.T) {
	err := &NoObjectGeneratedError{SDKError: SDKError{Msg: "no object"}}
	if err.Error() != "no object" {
		t.Errorf("expected 'no object', got %q", err.Error())
	}
}
