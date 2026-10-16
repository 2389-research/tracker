// ABOUTME: Tests for the LLM error type hierarchy.
// ABOUTME: Validates retryability, error wrapping, and HTTP status code mapping.
package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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
		{522, "*llm.ServerError", true},          // unknown 5xx defaults to retryable
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

// #A: an HTTP 429 whose body signals account credit/quota EXHAUSTION
// (OpenAI insufficient_quota) must map to a non-retryable QuotaExceededError
// (so it pauses resumably per #487) — not a retryable RateLimitError that burns
// retries against an empty balance. A plain rate-limit 429 stays retryable.
func TestErrorFromStatusCode_QuotaExhaustion429(t *testing.T) {
	exhausted := ErrorFromStatusCode(429, `{"error":{"code":"insufficient_quota","message":"You exceeded your current quota"}}`, "openai")
	if _, ok := exhausted.(*QuotaExceededError); !ok {
		t.Fatalf("insufficient_quota 429 = %T, want *QuotaExceededError", exhausted)
	}
	if pe, ok := exhausted.(ProviderErrorInterface); !ok || pe.Retryable() {
		t.Error("quota exhaustion must be non-retryable")
	}
	if !IsBillingError(exhausted) {
		t.Error("quota exhaustion must be recognized as a billing error (→ resumable pause)")
	}

	// A plain rate-limit 429 stays a retryable RateLimitError.
	rl := ErrorFromStatusCode(429, `{"error":{"code":"rate_limit_exceeded","message":"Rate limit reached"}}`, "openai")
	if _, ok := rl.(*RateLimitError); !ok {
		t.Errorf("rate-limit 429 = %T, want *RateLimitError", rl)
	}
	if IsBillingError(rl) {
		t.Error("a plain rate limit must NOT be treated as billing exhaustion")
	}
}

func TestErrorFromOpenAICode(t *testing.T) {
	tests := []struct {
		code       string
		expectType string
		retryable  bool
	}{
		{"insufficient_quota", "*llm.QuotaExceededError", false},
		{"invalid_api_key", "*llm.AuthenticationError", false},
		{"authentication_error", "*llm.AuthenticationError", false},
		{"model_not_found", "*llm.NotFoundError", false},
		{"invalid_request_error", "*llm.InvalidRequestError", false},
		{"invalid_request", "*llm.InvalidRequestError", false},
		{"context_length_exceeded", "*llm.ContextLengthError", false},
		{"content_filter", "*llm.ContentFilterError", false},
		{"content_policy_violation", "*llm.ContentFilterError", false},
		{"rate_limit_exceeded", "*llm.RateLimitError", true},
		{"server_error", "*llm.ServerError", true},
		{"internal_error", "*llm.ServerError", true},
		// Request-side failures OpenAI's Responses API documents for a failed
		// response fail at once: a retry would send the same request.
		{"invalid_prompt", "*llm.InvalidRequestError", false},
		{"data_residency_mismatch", "*llm.InvalidRequestError", false},
		{"invalid_image", "*llm.InvalidRequestError", false},
		{"invalid_image_format", "*llm.InvalidRequestError", false},
		{"invalid_base64_image", "*llm.InvalidRequestError", false},
		{"invalid_image_url", "*llm.InvalidRequestError", false},
		{"image_too_large", "*llm.InvalidRequestError", false},
		{"image_too_small", "*llm.InvalidRequestError", false},
		{"image_parse_error", "*llm.InvalidRequestError", false},
		{"invalid_image_mode", "*llm.InvalidRequestError", false},
		{"image_file_too_large", "*llm.InvalidRequestError", false},
		{"unsupported_image_media_type", "*llm.InvalidRequestError", false},
		{"empty_image_file", "*llm.InvalidRequestError", false},
		{"image_file_not_found", "*llm.InvalidRequestError", false},
		{"image_content_policy_violation", "*llm.ContentFilterError", false},
		{"bio_policy", "*llm.ContentFilterError", false},
		{"misalignment_policy_violation", "*llm.ContentFilterError", false},
		// The server took the request before it failed, so every other code
		// is retried: OpenAI's transient ones, and the codes gateways invent (#684).
		{"vector_store_timeout", "*llm.ServerError", true},
		{"failed_to_download_image", "*llm.ServerError", true},
		{"brand_new_code", "*llm.ServerError", true},
		{"", "*llm.ServerError", true},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			err := ErrorFromOpenAICode(tt.code, "openai-compat: boom", "openai-compat")
			if got := fmt.Sprintf("%T", err); got != tt.expectType {
				t.Fatalf("code %q: expected %s, got %s", tt.code, tt.expectType, got)
			}
			pe := err.(ProviderErrorInterface)
			if pe.Retryable() != tt.retryable {
				t.Errorf("code %q: expected retryable=%v, got %v", tt.code, tt.retryable, pe.Retryable())
			}
			if pe.GetProvider() != "openai-compat" {
				t.Errorf("expected provider 'openai-compat', got %q", pe.GetProvider())
			}
			if err.Error() != "openai-compat: boom" {
				t.Errorf("expected message 'openai-compat: boom', got %q", err.Error())
			}
			if got := reflect.ValueOf(err).Elem().FieldByName("ErrorCode").String(); got != tt.code {
				t.Errorf("expected ErrorCode %q, got %q", tt.code, got)
			}
		})
	}
}

// TestStreamErrorCodeDecode: a stream error's code decodes from a JSON string
// or number, and any other value decodes as no code instead of failing the
// whole event, which would drop the server's message.
func TestStreamErrorCodeDecode(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`"server_error"`, "server_error"},
		{`500`, "500"},
		{`null`, ""},
		{`true`, ""},
		{`{"nested":1}`, ""},
	}
	for _, tt := range tests {
		var c StreamErrorCode
		if err := json.Unmarshal([]byte(tt.in), &c); err != nil {
			t.Errorf("Unmarshal(%s): %v", tt.in, err)
			continue
		}
		if string(c) != tt.want {
			t.Errorf("Unmarshal(%s) = %q, want %q", tt.in, c, tt.want)
		}
	}
}

// TestErrorFromOpenAICodeUnknownCodeBilling: an unrecognized code whose message
// reports credit or quota exhaustion stays a non-retryable billing error, so the
// run pauses (#487) instead of retrying against an empty balance. IsBillingError
// never counts a retryable error as billing, so a retryable type would lose it.
func TestErrorFromOpenAICodeUnknownCodeBilling(t *testing.T) {
	tests := []struct {
		name          string
		code          string
		msg           string
		wantBilling   bool
		wantRetryable bool
	}{
		{"credit balance without a code", "", "openai-compat: Your credit balance is too low to access the API", true, false},
		{"exhausted quota with an unknown code", "brand_new_code", "openai: You exceeded your current quota, please check your plan", true, false},
		{"gateway timeout without a code", "", "openai-compat: scheduler admission timeout", false, true},
		// Gemini reuses "Quota exceeded" for per-minute limits: without an
		// exhaustion signal it is a transient throttle, retried, not a pause.
		{"per-minute quota without a code", "", "openai-compat: Quota exceeded for metric per minute", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ErrorFromOpenAICode(tt.code, tt.msg, "openai-compat")
			if got := IsBillingError(err); got != tt.wantBilling {
				t.Errorf("IsBillingError = %v, want %v (%T)", got, tt.wantBilling, err)
			}
			if got := err.(ProviderErrorInterface).Retryable(); got != tt.wantRetryable {
				t.Errorf("Retryable = %v, want %v (%T)", got, tt.wantRetryable, err)
			}
		})
	}
}

// TestErrorFromOpenAIStreamError: a stream error whose code is empty or
// unknown is classified by its type, and a code that is an HTTP status
// (string "401" or number 400) is classified like that status over HTTP, so an
// auth, bad-request or not-found error still fails at once, a 402 pauses as
// billing, and a 429 stays a rate limit unless its text reports exhaustion.
func TestErrorFromOpenAIStreamError(t *testing.T) {
	const gemini429 = "You exceeded your current quota, please check your plan and billing details. /gemini-api/docs/rate-limits"
	tests := []struct {
		name, code, typ, msg string
		wantType             string
		wantRetryable        bool
		wantBilling          bool
		wantStatus           int
	}{
		{"null code, invalid_request_error type", "", "invalid_request_error", "Incorrect API key provided", "*llm.InvalidRequestError", false, false, 0},
		{"null code, authentication_error type", "", "authentication_error", "invalid x-api-key", "*llm.AuthenticationError", false, false, 0},
		{"null code, auth_error type", "", "auth_error", "bad key", "*llm.AuthenticationError", false, false, 0},
		{"null code, permission_error type", "", "permission_error", "no", "*llm.AccessDeniedError", false, false, 0},
		{"null code, not_found_error type", "", "not_found_error", "no model", "*llm.NotFoundError", false, false, 0},
		{"null code, insufficient_quota type", "", "insufficient_quota", "out of quota", "*llm.QuotaExceededError", false, true, 0},
		{"null code, rate_limit_exceeded type", "", "rate_limit_exceeded", "slow down", "*llm.RateLimitError", true, false, 0},
		{"null code, rate_limit_error type", "", "rate_limit_error", "slow down", "*llm.RateLimitError", true, false, 0},
		// Anthropic's 402 and 413 types classify as HTTP 402 and 413 do.
		{"null code, billing_error type", "", "billing_error", "Your organization's payment method was declined", "*llm.QuotaExceededError", false, true, 0},
		{"null code, request_too_large type", "", "request_too_large", "Request exceeds the maximum size", "*llm.ContextLengthError", false, false, 0},
		{"billing_hard_limit_reached code", "billing_hard_limit_reached", "", "Billing hard limit has been reached", "*llm.QuotaExceededError", false, true, 0},
		{"unknown code, invalid_request_error type", "brand_new_code", "invalid_request_error", "bad", "*llm.InvalidRequestError", false, false, 0},
		{"unknown code, unknown type", "brand_new_code", "gateway_error", "scheduler admission timeout", "*llm.ServerError", true, false, 0},
		{"known code beats type", "server_error", "invalid_request_error", "boom", "*llm.ServerError", true, false, 0},
		{"string 401", "401", "auth_error", "Invalid API key", "*llm.AuthenticationError", false, false, 401},
		{"numeric 400 (vLLM)", "400", "BadRequestError", "This model's maximum context length is 4096 tokens", "*llm.InvalidRequestError", false, false, 400},
		{"numeric 402 (OpenRouter)", "402", "", "Insufficient credits. Add more using https://openrouter.ai/credits", "*llm.QuotaExceededError", false, true, 402},
		{"numeric 403", "403", "", "forbidden", "*llm.AccessDeniedError", false, false, 403},
		{"numeric 404", "404", "", "model not found", "*llm.NotFoundError", false, false, 404},
		{"numeric 413", "413", "", "too large", "*llm.ContextLengthError", false, false, 413},
		{"numeric 429 with Gemini per-minute text", "429", "", gemini429, "*llm.RateLimitError", true, false, 429},
		{"numeric 429 with insufficient_quota", "429", "", "insufficient_quota: add credit", "*llm.QuotaExceededError", false, true, 429},
		{"numeric 500", "500", "", "upstream overloaded", "*llm.ServerError", true, false, 500},
		{"numeric 503", "503", "", "unavailable", "*llm.ServerError", true, false, 503},
		{"out-of-range number", "999", "", "odd", "*llm.ServerError", true, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ErrorFromOpenAIStreamError(tt.code, tt.typ, tt.msg, "openai-compat")
			if got := fmt.Sprintf("%T", err); got != tt.wantType {
				t.Fatalf("type = %s, want %s (%v)", got, tt.wantType, err)
			}
			pe := err.(ProviderErrorInterface)
			if pe.Retryable() != tt.wantRetryable {
				t.Errorf("Retryable = %v, want %v", pe.Retryable(), tt.wantRetryable)
			}
			if got := IsBillingError(err); got != tt.wantBilling {
				t.Errorf("IsBillingError = %v, want %v", got, tt.wantBilling)
			}
			if pe.GetStatusCode() != tt.wantStatus {
				t.Errorf("StatusCode = %d, want %d", pe.GetStatusCode(), tt.wantStatus)
			}
			if err.Error() != tt.msg {
				t.Errorf("message = %q, want %q", err.Error(), tt.msg)
			}
		})
	}
}

// TestErrorFromOpenAICodeUnknownCodeUsageLimit: an unknown code whose text is a
// subscription usage limit fails at once, so codergen pauses on it (IsUsageLimit)
// instead of the retry middleware re-hitting the same cap.
func TestErrorFromOpenAICodeUnknownCodeUsageLimit(t *testing.T) {
	err := ErrorFromOpenAICode("", "openai-compat: usage limit reached, resets at 2026-08-24T15:00:00Z", "openai-compat")
	if err.(ProviderErrorInterface).Retryable() {
		t.Errorf("usage-limit error must not be retryable, got %T", err)
	}
	if !IsUsageLimit(err) {
		t.Error("IsUsageLimit = false, want true")
	}
	if IsBillingError(err) {
		t.Error("a usage limit must not be classified as billing")
	}
}

// TestErrorFromStatusCode402: HTTP 402 Payment Required is credit exhaustion,
// so it pauses as billing (#487) rather than failing as a bad request.
func TestErrorFromStatusCode402(t *testing.T) {
	err := ErrorFromStatusCode(402, "Insufficient credits", "openrouter")
	if _, ok := err.(*QuotaExceededError); !ok {
		t.Fatalf("402 = %T, want *QuotaExceededError", err)
	}
	if !IsBillingError(err) {
		t.Error("IsBillingError = false, want true")
	}
}
