// ABOUTME: Tests billing-error detection and the account-attributed help message,
// ABOUTME: including the invariant that the raw API key is never revealed.
package llm

import (
	"fmt"
	"strings"
	"testing"
)

func anthropicCreditErr() error {
	return &InvalidRequestError{ProviderError: ProviderError{
		SDKError:   SDKError{Msg: "anthropic: invalid_request_error: Your credit balance is too low to access the Anthropic API."},
		Provider:   "anthropic",
		StatusCode: 400,
	}}
}

func TestIsBillingError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"anthropic credit balance", anthropicCreditErr(), true},
		{"openai quota type", &QuotaExceededError{ProviderError: ProviderError{Provider: "openai"}}, true},
		{"insufficient_quota text", fmt.Errorf("openai: insufficient_quota: You exceeded your current quota"), true},
		{"wrapped credit error", fmt.Errorf("node \"Implement\": %w", anthropicCreditErr()), true},
		{"auth error is not billing", &AuthenticationError{ProviderError: ProviderError{SDKError: SDKError{Msg: "invalid api key"}}}, false},
		{"unrelated error", fmt.Errorf("connection reset"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsBillingError(tc.err); got != tc.want {
				t.Errorf("IsBillingError = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBillingHelp_AttributesAccount(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-secret1234TAIL")

	msg, ok := BillingHelp(anthropicCreditErr())
	if !ok {
		t.Fatal("expected a billing help message")
	}
	for _, want := range []string{"anthropic", "ANTHROPIC_API_KEY", "console.anthropic.com"} {
		if !strings.Contains(msg, want) {
			t.Errorf("help missing %q:\n%s", want, msg)
		}
	}
	// The masked fingerprint must never reveal the full key.
	if strings.Contains(msg, "sk-ant-secret1234TAIL") {
		t.Fatalf("help leaked the raw API key:\n%s", msg)
	}
	if !strings.Contains(msg, "TAIL") { // safe 4-char suffix is fine
		t.Errorf("help should show a masked fingerprint tail:\n%s", msg)
	}
}

func TestBillingHelp_KeyNotSet(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	msg, ok := BillingHelp(anthropicCreditErr())
	if !ok {
		t.Fatal("expected a billing help message")
	}
	if !strings.Contains(msg, "ANTHROPIC_API_KEY") || !strings.Contains(msg, "not set") {
		t.Errorf("with no key set, still name the env var as 'not set':\n%s", msg)
	}
}

func TestBillingHelp_NotABillingError(t *testing.T) {
	if _, ok := BillingHelp(fmt.Errorf("connection reset")); ok {
		t.Error("non-billing error should not produce help")
	}
}
