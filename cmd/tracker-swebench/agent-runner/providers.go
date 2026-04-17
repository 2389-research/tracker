// ABOUTME: Provider adapter constructors for the agent-runner binary.
// ABOUTME: Builds Anthropic and OpenAI adapters with optional base URL overrides.
package main

import (
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/llm/anthropic"
	"github.com/2389-research/tracker/llm/openai"
)

func newAnthropicAdapter(key, baseURL string) (llm.ProviderAdapter, error) {
	var opts []anthropic.Option
	if baseURL != "" {
		opts = append(opts, anthropic.WithBaseURL(baseURL))
	}
	return anthropic.New(key, opts...), nil
}

func newOpenAIAdapter(key, baseURL string) (llm.ProviderAdapter, error) {
	var opts []openai.Option
	if baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	return openai.New(key, opts...), nil
}
