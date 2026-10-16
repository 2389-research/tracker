// ABOUTME: Tests that a session result names the usage label its client reports for the configured provider
// ABOUTME: (openai-subscription for openai on a ChatGPT subscription), and the provider when there is none.
package agent

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// labelingCompleter is a mockCompleter that also reports usage labels, as
// *llm.Client does: labels maps a provider name to its label.
type labelingCompleter struct {
	mockCompleter
	labels map[string]string
}

func (c *labelingCompleter) UsageLabel(provider string) string { return c.labels[provider] }

func TestSessionResultCarriesUsageLabel(t *testing.T) {
	cases := []struct {
		name   string
		client Completer
		want   string
	}{
		{"client labels openai", &labelingCompleter{labels: map[string]string{"openai": llm.OpenAISubscription}}, llm.OpenAISubscription},
		{"client without UsageLabel", &mockCompleter{}, "openai"},
		{"client with an empty label", &labelingCompleter{labels: map[string]string{}}, "openai"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Provider = "openai"
			cfg.MaxTurns = 1
			result, err := mustNewSession(t, tc.client, cfg).Run(context.Background(), "hi")
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.Provider != tc.want {
				t.Errorf("result.Provider = %q, want %q", result.Provider, tc.want)
			}
		})
	}
}
