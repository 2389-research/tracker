// ABOUTME: Tests that compaction derives its limit from the model's real context
// ABOUTME: window (sourced straight from dippin per model) rather than the default (#572).
package agent

import "testing"

func TestEffectiveContextWindowLimit(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		limit    int // 0 means "leave DefaultConfig's default"
		want     int
	}{
		// A catalogued 1M-window model overrides the generic default.
		{"catalogued 1M window", "anthropic", "claude-opus-5", 0, 1000000},
		// A model dippin prices but tracker does NOT catalogue now resolves to
		// dippin's real window — the case GetModelInfo (catalog-only) missed.
		{"uncatalogued dippin-priced", "cohere", "command-a-03-2025", 0, 256000},
		// A smaller-window model compacts before it overruns.
		{"smaller window", "cohere", "command-r-08-2024", 0, 128000},
		// An unknown model keeps the generic default.
		{"unknown model", "anthropic", "some-unknown-model-xyz", 0, 200000},
		// An explicitly configured limit is always respected, even for a model
		// with a known (different) dippin window.
		{"explicit limit respected", "anthropic", "claude-opus-5", 500000, 500000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := DefaultConfig()
			c.Provider = tt.provider
			c.Model = tt.model
			if tt.limit != 0 {
				c.ContextWindowLimit = tt.limit
			}
			if got := c.EffectiveContextWindowLimit(); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}
