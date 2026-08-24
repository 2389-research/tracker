// ABOUTME: Model catalog derived entirely from dippin-lang/pricing (DRY, #570).
// ABOUTME: Supports lookup by ID/alias and listing by provider.
package llm

import (
	"slices"
	"sort"

	"github.com/2389-research/dippin-lang/pricing"
)

// ModelInfo describes a known LLM model and its capabilities. Every field is
// derived from dippin-lang/pricing (the single source of truth, #558/#570):
// identity (ID/provider/aliases), display name (#570, dippin v0.68), and
// capabilities (context window / max output / tool·vision·reasoning, #571). The
// hand-maintained catalog that used to live here is retired — the model set is
// now dippin's by construction and can never drift from it.
type ModelInfo struct {
	ID                string   `json:"id"`
	Provider          string   `json:"provider"`
	DisplayName       string   `json:"display_name"`
	ContextWindow     int      `json:"context_window"`
	MaxOutput         int      `json:"max_output"`
	SupportsTools     bool     `json:"supports_tools"`
	SupportsVision    bool     `json:"supports_vision"`
	SupportsReasoning bool     `json:"supports_reasoning"`
	Aliases           []string `json:"aliases,omitempty"`
	// CacheReadMultiplier and CacheWriteMultiplier are a per-model override hook
	// for the cache-rate overlay (pricing.go). dippin-lang/pricing now carries
	// verified cache read AND write rates for essentially every model, so the
	// overlay self-disables and these are never set for a dippin-derived entry
	// (0 everywhere). They remain on the struct as the override seam the overlay
	// reads through for a model in pricing.CacheGaps().
	CacheReadMultiplier  float64 `json:"cache_read_multiplier,omitempty"`
	CacheWriteMultiplier float64 `json:"cache_write_multiplier,omitempty"`
}

// modelInfoFrom projects one dippin pricing entry into a ModelInfo. The display
// name comes from dippin's DisplayName; an empty DisplayName means UNKNOWN (no
// confirmable official name — about 14 models), so we fall back to the id itself
// rather than treating "" as a real name (#570).
func modelInfoFrom(provider, id string, mp pricing.ModelPrice) ModelInfo {
	name := id
	if mp.DisplayName != "" {
		name = mp.DisplayName
	}
	return ModelInfo{
		ID:                id,
		Provider:          provider,
		DisplayName:       name,
		ContextWindow:     mp.ContextWindow,
		MaxOutput:         mp.MaxOutput,
		SupportsTools:     slices.Contains(mp.Capabilities, "tools"),
		SupportsVision:    slices.Contains(mp.Capabilities, "vision"),
		SupportsReasoning: slices.Contains(mp.Capabilities, "reasoning"),
		Aliases:           mp.Aliases,
	}
}

// derivedCatalog enumerates every priced model from dippin-lang/pricing, sorted
// by provider then id for a stable, deterministic order.
func derivedCatalog() []ModelInfo {
	provs := pricing.Providers()
	keys := make([]string, 0, len(provs))
	for p := range provs {
		keys = append(keys, p)
	}
	sort.Strings(keys)

	var out []ModelInfo
	for _, p := range keys {
		models := provs[p]
		ids := make([]string, 0, len(models))
		for id := range models {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			out = append(out, modelInfoFrom(p, id, models[id]))
		}
	}
	return out
}

// GetModelInfo looks up a model by ID or alias, resolving through dippin's
// version-separator fold (claude-haiku-4.5 == claude-haiku-4-5). Returns nil if
// dippin does not price the model. An exact id wins anywhere in the catalog;
// otherwise the first alias or version-folded id match resolves to its entry.
func GetModelInfo(modelID string) *ModelInfo {
	if modelID == "" {
		return nil
	}
	canon := pricing.CanonicalModelID(modelID)
	cat := derivedCatalog()
	var aliasMatch *ModelInfo
	for i := range cat {
		m := &cat[i]
		if m.ID == modelID {
			return m
		}
		if aliasMatch == nil && modelMatches(m, modelID, canon) {
			aliasMatch = m
		}
	}
	return aliasMatch
}

// modelMatches reports whether modelID resolves to m by version-folded id or by
// one of m's aliases (exact or version-folded). canon is CanonicalModelID(modelID).
func modelMatches(m *ModelInfo, modelID, canon string) bool {
	if pricing.CanonicalModelID(m.ID) == canon {
		return true
	}
	for _, a := range m.Aliases {
		if a == modelID || pricing.CanonicalModelID(a) == canon {
			return true
		}
	}
	return false
}

// ListModels returns all models dippin prices, optionally filtered by provider.
// Pass an empty string to return all models.
func ListModels(provider string) []ModelInfo {
	cat := derivedCatalog()
	if provider == "" {
		return cat
	}
	var result []ModelInfo
	for _, m := range cat {
		if m.Provider == provider {
			result = append(result, m)
		}
	}
	return result
}
