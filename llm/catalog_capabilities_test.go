// ABOUTME: Guards the #571 burn-down: capability metadata for dippin-populated
// ABOUTME: models comes from dippin, never re-hand-maintained in defaultCatalog.
package llm

import (
	"testing"

	"github.com/2389-research/dippin-lang/pricing"
)

// TestCatalogCapabilitiesComeFromDippin enforces the burn-down invariant: for any
// catalog entry dippin has populated capability metadata for, the hand-maintained
// catalog must NOT carry its own capability values (they'd be dead — the overlay
// prefers dippin — and, as the big-three population revealed, historically wrong).
// A capability value is allowed ONLY for a model dippin hasn't populated yet.
func TestCatalogCapabilitiesComeFromDippin(t *testing.T) {
	for i := range defaultCatalog {
		c := &defaultCatalog[i]
		mp, ok := pricing.Lookup(c.ID)
		dippinPopulated := ok && (mp.ContextWindow > 0 || mp.MaxOutput > 0 || len(mp.Capabilities) > 0)
		if !dippinPopulated {
			continue // dippin hasn't populated this one — a catalog fallback is allowed
		}
		if c.ContextWindow != 0 || c.MaxOutput != 0 || c.SupportsTools || c.SupportsVision || c.SupportsReasoning {
			t.Errorf("%s: dippin populates its capabilities, so the catalog must not hand-maintain any "+
				"(ctx=%d out=%d tools=%v vision=%v reasoning=%v) — delete them and let the dippin overlay fill them",
				c.ID, c.ContextWindow, c.MaxOutput, c.SupportsTools, c.SupportsVision, c.SupportsReasoning)
		}
	}
}

// TestCatalogCapabilitiesResolveFromDippin is the positive side: a stripped
// catalog entry still yields correct capabilities via the overlay.
func TestCatalogCapabilitiesResolveFromDippin(t *testing.T) {
	for _, id := range []string{"claude-fable-5", "claude-sonnet-5", "gpt-5.2", "gemini-2.5-pro"} {
		m := GetModelInfo(id)
		if m == nil {
			t.Errorf("%s: GetModelInfo nil", id)
			continue
		}
		if m.ContextWindow == 0 {
			t.Errorf("%s: ContextWindow 0 after burn-down — dippin overlay did not fill it", id)
		}
	}
}
