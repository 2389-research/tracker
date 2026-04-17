// ABOUTME: Provider adapter constructors for the agent-runner binary.
// ABOUTME: Builds Anthropic and OpenAI adapters with optional base URL overrides and gateway auth.
package main

import (
	"os"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/llm/anthropic"
	"github.com/2389-research/tracker/llm/openai"
)

// gatewayHeaders returns extra headers for CF AI Gateway authentication.
// When CF_AIG_TOKEN is set, it is sent as the cf-aig-token header so the
// gateway can authenticate the request and inject provider API keys.
func gatewayHeaders() map[string]string {
	if token := os.Getenv("CF_AIG_TOKEN"); token != "" {
		return map[string]string{"cf-aig-token": token}
	}
	return nil
}

func newAnthropicAdapter(key, baseURL string) (llm.ProviderAdapter, error) {
	var opts []anthropic.Option
	if baseURL != "" {
		opts = append(opts, anthropic.WithBaseURL(baseURL))
	}
	if h := gatewayHeaders(); h != nil {
		opts = append(opts, anthropic.WithExtraHeaders(h))
	}
	return anthropic.New(key, opts...), nil
}

func newOpenAIAdapter(key, baseURL string) (llm.ProviderAdapter, error) {
	var opts []openai.Option
	if baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	if h := gatewayHeaders(); h != nil {
		opts = append(opts, openai.WithExtraHeaders(h))
	}
	return openai.New(key, opts...), nil
}
