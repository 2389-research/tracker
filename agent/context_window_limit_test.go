// ABOUTME: Tests that compaction derives its limit from the model's real context
// ABOUTME: window (dippin-sourced) rather than the generic default (#572).
package agent

import "testing"

func TestEffectiveContextWindowLimit(t *testing.T) {
	// A known 1M-window model overrides the generic default.
	c := DefaultConfig()
	c.Model = "claude-opus-5"
	if got := c.EffectiveContextWindowLimit(); got != 1000000 {
		t.Errorf("opus-5: got %d, want 1000000 (dippin window, not the %d default)", got, c.ContextWindowLimit)
	}
	// A model with a smaller real window compacts before overrun.
	c.Model = "claude-haiku-4-5"
	if got := c.EffectiveContextWindowLimit(); got != 200000 {
		t.Errorf("haiku-4-5: got %d, want 200000", got)
	}
	// Unknown model falls back to the configured default.
	c.Model = "some-unknown-model-xyz"
	c.ContextWindowLimit = 200000
	if got := c.EffectiveContextWindowLimit(); got != 200000 {
		t.Errorf("unknown: got %d, want the 200000 fallback", got)
	}
}
