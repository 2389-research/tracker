// ABOUTME: Tests ModelContextWindow — the per-model context window sourced straight
// ABOUTME: from dippin (provider-scoped, alias-resolving), 0 when unknown (#572).
package llm

import "testing"

func TestModelContextWindow(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		want     int
	}{
		{"catalogued 1M window", "anthropic", "claude-opus-5", 1000000},
		{"uncatalogued dippin-priced", "cohere", "command-a-03-2025", 256000},
		{"smaller window", "cohere", "command-r-08-2024", 128000},
		{"empty provider falls back to bare lookup", "", "claude-opus-5", 1000000},
		{"family alias resolves to concrete model", "anthropic", "opus@latest", 1000000},
		{"unknown model → 0", "anthropic", "some-unknown-model-xyz", 0},
		{"unresolvable alias → 0", "anthropic", "nosuchfamily@latest", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ModelContextWindow(tt.provider, tt.model); got != tt.want {
				t.Errorf("ModelContextWindow(%q, %q) = %d, want %d", tt.provider, tt.model, got, tt.want)
			}
		})
	}
}
