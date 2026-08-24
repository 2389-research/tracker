// ABOUTME: Guards the #570 retirement: the catalog (models, display names,
// ABOUTME: capabilities) derives entirely from dippin-lang/pricing.
package llm

import (
	"testing"

	"github.com/2389-research/dippin-lang/pricing"
)

// TestCatalogDerivesFromDippin enforces the #570 retirement invariant: the model
// set ListModels reports IS dippin's priced set — no hand-maintained catalog to
// drift from it. Every listed model resolves in dippin, and every model dippin
// prices is listed.
func TestCatalogDerivesFromDippin(t *testing.T) {
	listed := map[string]bool{}
	for _, m := range ListModels("") {
		if _, ok := pricing.Lookup(m.ID); !ok {
			t.Errorf("%s: ListModels reports a model dippin does not price", m.ID)
		}
		listed[m.ID] = true
	}
	for _, models := range pricing.Providers() {
		for id := range models {
			if !listed[id] {
				t.Errorf("%s: dippin prices it but ListModels omits it", id)
			}
		}
	}
}

// TestCatalogCapabilitiesResolveFromDippin is the positive check: a listed entry
// carries dippin's capability metadata (context window) via the derivation.
func TestCatalogCapabilitiesResolveFromDippin(t *testing.T) {
	for _, id := range []string{"claude-fable-5", "claude-sonnet-5", "gpt-5.2", "gemini-2.5-pro"} {
		m := GetModelInfo(id)
		if m == nil {
			t.Errorf("%s: GetModelInfo nil", id)
			continue
		}
		mp, ok := pricing.Lookup(id)
		if !ok {
			t.Errorf("%s: dippin does not price it", id)
			continue
		}
		if m.ContextWindow != mp.ContextWindow {
			t.Errorf("%s: ContextWindow = %d, want dippin's %d", id, m.ContextWindow, mp.ContextWindow)
		}
	}
}

// TestDisplayNameDerivesFromDippin checks the #570 display-name rule: a model
// with a dippin DisplayName uses it verbatim; a model dippin leaves blank
// (unknown) falls back to the id itself, never an empty name.
func TestDisplayNameDerivesFromDippin(t *testing.T) {
	for _, m := range ListModels("") {
		if m.DisplayName == "" {
			t.Errorf("%s: DisplayName is empty — the id fallback did not apply", m.ID)
		}
		mp, ok := pricing.Lookup(m.ID)
		if !ok {
			continue
		}
		if mp.DisplayName != "" {
			if m.DisplayName != mp.DisplayName {
				t.Errorf("%s: DisplayName = %q, want dippin's %q", m.ID, m.DisplayName, mp.DisplayName)
			}
		} else if m.DisplayName != m.ID {
			t.Errorf("%s: dippin has no display name, so it must fall back to the id, got %q", m.ID, m.DisplayName)
		}
	}
}
