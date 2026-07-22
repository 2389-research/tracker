// ABOUTME: Tests failure classification — every terminal error leads with a
// ABOUTME: human-first cause + remediation, not the node/handler wrapper (#492).
package tracker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// wrapLikeEngine mirrors the real chain: the handler wraps the provider error
// with `node "X": ` and the engine wraps that with `handler error at node "X": `.
func wrapLikeEngine(provErr error) error {
	handlerErr := fmt.Errorf("node %q: %w", "Implement", provErr)
	return fmt.Errorf("handler error at node %q: %w", "Implement", handlerErr)
}

func TestClassifyFailure_Kinds(t *testing.T) {
	billing := &llm.InvalidRequestError{ProviderError: llm.ProviderError{
		SDKError: llm.SDKError{Msg: "anthropic: invalid_request_error: Your credit balance is too low"},
		Provider: "anthropic", StatusCode: 400,
	}}
	cases := []struct {
		name     string
		err      error
		wantKind string
		wantIn   []string // substrings expected across Title/Detail/NextSteps
	}{
		{"billing", wrapLikeEngine(billing), "billing", []string{"Billing", "anthropic", "add credit"}},
		{"auth", wrapLikeEngine(&llm.AuthenticationError{ProviderError: llm.ProviderError{SDKError: llm.SDKError{Msg: "openai: invalid api key"}}}), "auth", []string{"Authentication", "doctor"}},
		{"context length", wrapLikeEngine(&llm.ContextLengthError{ProviderError: llm.ProviderError{SDKError: llm.SDKError{Msg: "maximum context length exceeded"}}}), "context_length", []string{"Context too long", "context window"}},
		{"rate limit", wrapLikeEngine(&llm.RateLimitError{ProviderError: llm.ProviderError{SDKError: llm.SDKError{Msg: "429 too many requests"}}}), "rate_limit", []string{"Rate limited", "retr"}},
		{"network", wrapLikeEngine(&llm.NetworkError{SDKError: llm.SDKError{Msg: "connection refused"}}), "network", []string{"Network", "connectivity"}},
		{"config", wrapLikeEngine(&llm.ConfigurationError{SDKError: llm.SDKError{Msg: "no providers configured"}}), "config", []string{"Configuration", "doctor"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := ClassifyFailure(tc.err)
			if fc.Kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", fc.Kind, tc.wantKind)
			}
			blob := fc.Title + "\n" + fc.Detail + "\n" + fc.NextSteps
			for _, w := range tc.wantIn {
				if !strings.Contains(blob, w) {
					t.Errorf("classification missing %q:\n%s", w, blob)
				}
			}
			// The node/handler wrapper must never be the headline.
			if strings.HasPrefix(fc.Title, "handler error") || strings.Contains(fc.Title, "node \"Implement\"") {
				t.Errorf("Title leaked the wrapper: %q", fc.Title)
			}
		})
	}
}

func TestClassifyFailure_GenericStripsWrappers(t *testing.T) {
	err := wrapLikeEngine(fmt.Errorf("compile failed: undefined symbol Foo"))
	fc := ClassifyFailure(err)
	if fc.Kind != "generic" {
		t.Fatalf("kind = %q, want generic", fc.Kind)
	}
	if fc.Detail != "compile failed: undefined symbol Foo" {
		t.Errorf("generic Detail should be the unwrapped cause, got %q", fc.Detail)
	}
}

func TestClassifyFailure_StripsCLIWrapper(t *testing.T) {
	// The CLI wraps with "pipeline execution: " on top of the engine/handler
	// wrappers; a generic cause must still lead cleanly.
	err := fmt.Errorf("pipeline execution: %w", wrapLikeEngine(fmt.Errorf("tests failed: 2 assertions")))
	if fc := ClassifyFailure(err); fc.Detail != "tests failed: 2 assertions" {
		t.Errorf("Detail should strip all wrappers, got %q", fc.Detail)
	}
}

func TestClassifyFailure_Nil(t *testing.T) {
	if fc := ClassifyFailure(nil); fc != (FailureCause{}) {
		t.Errorf("nil err should yield zero value, got %+v", fc)
	}
}

func TestClassifyFailure_RetryableRateLimitNotBilling(t *testing.T) {
	// A retryable 429 whose message says "Quota exceeded" must classify as
	// rate_limit (retry), NOT billing (add credit) — guards the #487 fix.
	err := wrapLikeEngine(&llm.RateLimitError{ProviderError: llm.ProviderError{
		SDKError: llm.SDKError{Msg: "gemini: Quota exceeded for ... per minute"}, Provider: "gemini", StatusCode: 429,
	}})
	if fc := ClassifyFailure(err); fc.Kind != "rate_limit" {
		t.Errorf("retryable quota-text 429 should be rate_limit, got %q", fc.Kind)
	}
}

func TestClassifyFailure_Breaches(t *testing.T) {
	tl := ClassifyFailure(wrapLikeEngine(fmt.Errorf("agent exhausted turn limit (50 turns) without completing")))
	if tl.Kind != "turn_limit" || !strings.Contains(tl.NextSteps, "max_turns") {
		t.Errorf("turn-limit classification: %+v", tl)
	}
	np := ClassifyFailure(wrapLikeEngine(fmt.Errorf("no-progress detected (3 consecutive turns with no tool calls)")))
	if np.Kind != "no_progress" || !strings.Contains(np.NextSteps, "no_progress_turns") {
		t.Errorf("no-progress classification: %+v", np)
	}
}
