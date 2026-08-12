// ABOUTME: Model catalog providing a registry of known LLM models and their capabilities.
// ABOUTME: Supports lookup by ID/alias and listing by provider.
package llm

import (
	"slices"

	"github.com/2389-research/dippin-lang/pricing"
)

// ModelInfo describes a known LLM model and its capabilities.
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
	// CacheReadMultiplier and CacheWriteMultiplier price cached prompt tokens as
	// a fraction of the base input rate (which comes from dippin-lang/pricing,
	// #558). They live in tracker for now because dippin's prices.json does not
	// yet carry cache rates; the overlay in pricing.go applies them. They live
	// per model because the discount is not a provider-wide convention: cached
	// reads are 0.1x on Anthropic, Gemini, and the GPT-5 family, 0.25x on
	// GPT-4.1, and 0.5x on gpt-4o-mini. Zero means "use the default" (see
	// defaultCacheReadMultiplier).
	CacheReadMultiplier  float64 `json:"cache_read_multiplier,omitempty"`
	CacheWriteMultiplier float64 `json:"cache_write_multiplier,omitempty"`
}

// defaultCatalog is the built-in registry of known models.
// Each provider section is ordered newest-first so ListModels returns each
// provider's most recent models first.
var defaultCatalog = []ModelInfo{
	// Capability metadata (context window, max output, tool/vision/reasoning) is
	// NO LONGER hand-maintained here — it comes from dippin-lang/pricing, overlaid
	// by GetModelInfo / ListModels (#571). These entries carry only identity
	// (ID / provider / display name / aliases) and tracker's per-model cache-write
	// premium. TestCatalogCapabilitiesComeFromDippin guards that no capability
	// value is re-added for a model dippin populates. (When dippin populated the
	// big three it revealed 15 of tracker's old hand-maintained values were wrong —
	// e.g. fable-5 was guessed at 200K, actually 1M — which the overlay now fixes.)
	//
	// ── Anthropic ────────────────────────────────────────────
	{
		ID:          "claude-opus-5",
		Provider:    "anthropic",
		DisplayName: "Claude Opus 5",
		Aliases:     []string{"opus-5", "claude-opus"},
		// Anthropic charges a 5-minute cache-write premium of 1.25x base input.
		CacheWriteMultiplier: 1.25,
	},
	{
		ID:          "claude-sonnet-5",
		Provider:    "anthropic",
		DisplayName: "Claude Sonnet 5",
		Aliases:     []string{"sonnet-5", "claude-sonnet"},
		// Anthropic charges a 5-minute cache-write premium of 1.25x base input.
		CacheWriteMultiplier: 1.25,
	},
	{
		ID:          "claude-fable-5",
		Provider:    "anthropic",
		DisplayName: "Claude Fable 5",
		Aliases:     []string{"fable-5", "claude-fable"},
		// Anthropic charges a 5-minute cache-write premium of 1.25x base input.
		CacheWriteMultiplier: 1.25,
	},
	{
		ID:          "claude-opus-4-8",
		Provider:    "anthropic",
		DisplayName: "Claude Opus 4.8",
		Aliases:     []string{"opus-4-8"},
		// Anthropic charges a 5-minute cache-write premium of 1.25x base input.
		CacheWriteMultiplier: 1.25,
	},
	{
		ID:          "claude-opus-4-7",
		Provider:    "anthropic",
		DisplayName: "Claude Opus 4.7",
		Aliases:     []string{"opus-4-7"},
		// Anthropic charges a 5-minute cache-write premium of 1.25x base input.
		CacheWriteMultiplier: 1.25,
	},
	{
		ID:          "claude-sonnet-4-6",
		Provider:    "anthropic",
		DisplayName: "Claude Sonnet 4.6",
		Aliases:     []string{"sonnet-4-6"},
		// Anthropic charges a 5-minute cache-write premium of 1.25x base input.
		CacheWriteMultiplier: 1.25,
	},
	{
		ID:          "claude-opus-4-6",
		Provider:    "anthropic",
		DisplayName: "Claude Opus 4.6",
		Aliases:     []string{"opus-4-6"},
		// Anthropic charges a 5-minute cache-write premium of 1.25x base input.
		CacheWriteMultiplier: 1.25,
	},
	{
		ID:          "claude-sonnet-4-5",
		Provider:    "anthropic",
		DisplayName: "Claude Sonnet 4.5",
		Aliases:     []string{"sonnet-4-5"},
		// Anthropic charges a 5-minute cache-write premium of 1.25x base input.
		CacheWriteMultiplier: 1.25,
	},
	{
		ID:          "claude-haiku-4-5",
		Provider:    "anthropic",
		DisplayName: "Claude Haiku 4.5",
		Aliases:     []string{"haiku-4-5", "claude-haiku"},
		// Anthropic charges a 5-minute cache-write premium of 1.25x base input.
		CacheWriteMultiplier: 1.25,
	},
	// ── OpenAI ───────────────────────────────────────────────
	{
		ID:          "gpt-5.4",
		Provider:    "openai",
		DisplayName: "GPT-5.4",
		Aliases:     []string{"gpt5.4"},
	},
	{
		ID:          "gpt-5.4-mini",
		Provider:    "openai",
		DisplayName: "GPT-5.4 Mini",
		Aliases:     []string{"gpt5.4-mini"},
	},
	{
		ID:          "gpt-5.4-nano",
		Provider:    "openai",
		DisplayName: "GPT-5.4 Nano",
		Aliases:     []string{"gpt5.4-nano"},
	},
	{
		ID:          "gpt-5.2",
		Provider:    "openai",
		DisplayName: "GPT-5.2",
		Aliases:     []string{"gpt5.2"},
	},
	{
		ID:          "gpt-5.2-codex",
		Provider:    "openai",
		DisplayName: "GPT-5.2 Codex",
		Aliases:     []string{"codex", "gpt5.2-codex"},
	},
	{
		ID:          "gpt-4.1",
		Provider:    "openai",
		DisplayName: "GPT-4.1",
		Aliases:     []string{"gpt4.1"},
		// GPT-4.1 family: cached input is $0.50 vs $2.00 base.
		CacheReadMultiplier: 0.25,
	},
	{
		ID:          "gpt-4.1-mini",
		Provider:    "openai",
		DisplayName: "GPT-4.1 Mini",
		Aliases:     []string{"gpt4.1-mini"},
		// GPT-4.1 family: cached input is $0.10 vs $0.40 base.
		CacheReadMultiplier: 0.25,
	},
	{
		ID:          "gpt-4.1-nano",
		Provider:    "openai",
		DisplayName: "GPT-4.1 Nano",
		Aliases:     []string{"gpt4.1-nano"},
		// GPT-4.1 family: cached input is $0.025 vs $0.10 base.
		CacheReadMultiplier: 0.25,
	},
	{
		ID:          "o3",
		Provider:    "openai",
		DisplayName: "o3",
		Aliases:     nil,
	},
	{
		ID:          "o4-mini",
		Provider:    "openai",
		DisplayName: "o4-mini",
		Aliases:     nil,
	},
	// Older OpenAI models (still active on API)
	{
		ID:          "gpt-4o",
		Provider:    "openai",
		DisplayName: "GPT-4o",
		Aliases:     []string{"4o"},
		// GPT-4o family: cached input is $1.25 vs $2.50 base.
		CacheReadMultiplier: 0.5,
	},
	{
		ID:          "gpt-4o-mini",
		Provider:    "openai",
		DisplayName: "GPT-4o Mini",
		Aliases:     []string{"4o-mini"},
		// GPT-4o family: cached input is $0.075 vs $0.15 base.
		CacheReadMultiplier: 0.5,
	},
	// ── Gemini ───────────────────────────────────────────────
	// GA models first, then previews.
	{
		ID:          "gemini-2.5-pro",
		Provider:    "gemini",
		DisplayName: "Gemini 2.5 Pro",
		Aliases:     []string{"gemini-pro"},
	},
	{
		ID:          "gemini-2.5-flash",
		Provider:    "gemini",
		DisplayName: "Gemini 2.5 Flash",
		Aliases:     []string{"gemini-flash"},
	},
	{
		// Closed to new API keys as of mid-2026: a request 404s with "no
		// longer available to new users", though the models list still
		// advertises it and existing keys may still work. Kept because runs
		// that used it still need a price when their manifests are rebuilt —
		// removing the entry would silently zero their cost.
		ID:          "gemini-2.5-flash-lite",
		Provider:    "gemini",
		DisplayName: "Gemini 2.5 Flash Lite",
		Aliases:     []string{"gemini-flash-lite"},
	},
	{
		ID:          "gemini-3.1-pro-preview",
		Provider:    "gemini",
		DisplayName: "Gemini 3.1 Pro Preview",
		Aliases:     []string{"gemini-3.1-pro", "gemini-3-pro"},
	},
	{
		ID:          "gemini-3-flash-preview",
		Provider:    "gemini",
		DisplayName: "Gemini 3 Flash Preview",
		Aliases:     []string{"gemini-3-flash"},
	},
}

// GetModelInfo looks up a model by ID or alias. Returns nil if not found.
func GetModelInfo(modelID string) *ModelInfo {
	base := lookupCatalog(modelID)
	if base == nil {
		return nil
	}
	if mp, ok := pricing.Lookup(modelID); ok {
		return overlayDippinCapabilities(base, mp)
	}
	return base
}

// overlayDippinCapabilities prefers dippin's capability metadata (#267/#571) over
// the hand-maintained catalog wherever dippin has populated it — the incremental
// path to retiring the parallel catalog (#570). dippin populates in verified
// per-provider batches, so an unpopulated field (0 / empty) leaves the catalog's
// value untouched. Returns base unchanged when dippin has nothing, else a COPY
// with dippin's populated fields overlaid (never mutates the shared catalog entry).
func overlayDippinCapabilities(base *ModelInfo, mp pricing.ModelPrice) *ModelInfo {
	if mp.ContextWindow == 0 && mp.MaxOutput == 0 && len(mp.Capabilities) == 0 {
		return base
	}
	m := *base
	if mp.ContextWindow > 0 {
		m.ContextWindow = mp.ContextWindow
	}
	if mp.MaxOutput > 0 {
		m.MaxOutput = mp.MaxOutput
	}
	if len(mp.Capabilities) > 0 {
		m.SupportsTools = slices.Contains(mp.Capabilities, "tools")
		m.SupportsVision = slices.Contains(mp.Capabilities, "vision")
		m.SupportsReasoning = slices.Contains(mp.Capabilities, "reasoning")
	}
	return &m
}

// lookupCatalog returns the hand-maintained catalog entry for modelID (by ID or
// alias), or nil. It returns a pointer into the shared defaultCatalog slice —
// callers must not mutate it (GetModelInfo copies before overlaying).
func lookupCatalog(modelID string) *ModelInfo {
	for i := range defaultCatalog {
		m := &defaultCatalog[i]
		if m.ID == modelID {
			return m
		}
		for _, alias := range m.Aliases {
			if alias == modelID {
				return m
			}
		}
	}
	return nil
}

// ListModels returns all known models, optionally filtered by provider.
// Pass an empty string to return all models. Capability metadata is overlaid
// from dippin (#571), same as GetModelInfo, so the hand-maintained catalog no
// longer needs to carry it for models dippin has populated.
func ListModels(provider string) []ModelInfo {
	var result []ModelInfo
	for i := range defaultCatalog {
		m := &defaultCatalog[i]
		if provider != "" && m.Provider != provider {
			continue
		}
		if mp, ok := pricing.Lookup(m.ID); ok {
			result = append(result, *overlayDippinCapabilities(m, mp))
			continue
		}
		result = append(result, *m)
	}
	return result
}
