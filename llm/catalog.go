// ABOUTME: Model catalog providing a registry of known LLM models and their capabilities.
// ABOUTME: Supports lookup by ID/alias and listing by provider.
package llm

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
	// ── Anthropic ────────────────────────────────────────────
	// Claude 5 family. These models default to *omitted* thinking; using them on
	// multi-turn / tool-use runs was blocked until #567 (empty thinking field) and
	// #568 (empty no-arg tool input) were fixed (v0.63.4 / v0.63.5). dippin prices
	// all three (guarded by TestCatalogModelsArePricedByDippin). Capability
	// metadata below MIRRORS the prior flagship generation (opus-4-7 / sonnet-4-6)
	// pending confirmed published Claude 5 specs; fable-5 uses conservative values
	// (200K context) as its tier is unconfirmed — under-estimating the window fails
	// safe toward earlier compaction rather than a context overflow.
	{
		ID:                "claude-opus-5",
		Provider:          "anthropic",
		DisplayName:       "Claude Opus 5",
		ContextWindow:     1000000,
		MaxOutput:         128000,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		Aliases:           []string{"opus-5", "claude-opus"},
		// Anthropic charges a 5-minute cache-write premium of 1.25x base input.
		CacheWriteMultiplier: 1.25,
	},
	{
		ID:                "claude-sonnet-5",
		Provider:          "anthropic",
		DisplayName:       "Claude Sonnet 5",
		ContextWindow:     1000000,
		MaxOutput:         64000,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		Aliases:           []string{"sonnet-5", "claude-sonnet"},
		// Anthropic charges a 5-minute cache-write premium of 1.25x base input.
		CacheWriteMultiplier: 1.25,
	},
	{
		ID:                "claude-fable-5",
		Provider:          "anthropic",
		DisplayName:       "Claude Fable 5",
		ContextWindow:     200000,
		MaxOutput:         64000,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		Aliases:           []string{"fable-5", "claude-fable"},
		// Anthropic charges a 5-minute cache-write premium of 1.25x base input.
		CacheWriteMultiplier: 1.25,
	},
	{
		ID:                "claude-opus-4-8",
		Provider:          "anthropic",
		DisplayName:       "Claude Opus 4.8",
		ContextWindow:     1000000,
		MaxOutput:         128000,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		Aliases:           []string{"opus-4-8"},
		// Anthropic charges a 5-minute cache-write premium of 1.25x base input.
		CacheWriteMultiplier: 1.25,
	},
	{
		ID:                "claude-opus-4-7",
		Provider:          "anthropic",
		DisplayName:       "Claude Opus 4.7",
		ContextWindow:     1000000,
		MaxOutput:         128000,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		Aliases:           []string{"opus-4-7"},
		// Anthropic charges a 5-minute cache-write premium of 1.25x base input.
		CacheWriteMultiplier: 1.25,
	},
	{
		ID:                "claude-sonnet-4-6",
		Provider:          "anthropic",
		DisplayName:       "Claude Sonnet 4.6",
		ContextWindow:     1000000,
		MaxOutput:         64000,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		Aliases:           []string{"sonnet-4-6"},
		// Anthropic charges a 5-minute cache-write premium of 1.25x base input.
		CacheWriteMultiplier: 1.25,
	},
	{
		ID:                "claude-opus-4-6",
		Provider:          "anthropic",
		DisplayName:       "Claude Opus 4.6",
		ContextWindow:     1000000,
		MaxOutput:         128000,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		Aliases:           []string{"opus-4-6"},
		// Anthropic charges a 5-minute cache-write premium of 1.25x base input.
		CacheWriteMultiplier: 1.25,
	},
	{
		ID:                "claude-sonnet-4-5",
		Provider:          "anthropic",
		DisplayName:       "Claude Sonnet 4.5",
		ContextWindow:     200000,
		MaxOutput:         64000,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		Aliases:           []string{"sonnet-4-5"},
		// Anthropic charges a 5-minute cache-write premium of 1.25x base input.
		CacheWriteMultiplier: 1.25,
	},
	{
		ID:                "claude-haiku-4-5",
		Provider:          "anthropic",
		DisplayName:       "Claude Haiku 4.5",
		ContextWindow:     200000,
		MaxOutput:         64000,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		Aliases:           []string{"haiku-4-5", "claude-haiku"},
		// Anthropic charges a 5-minute cache-write premium of 1.25x base input.
		CacheWriteMultiplier: 1.25,
	},
	// ── OpenAI ───────────────────────────────────────────────
	{
		ID:                "gpt-5.4",
		Provider:          "openai",
		DisplayName:       "GPT-5.4",
		ContextWindow:     1050000,
		MaxOutput:         128000,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: false,
		Aliases:           []string{"gpt5.4"},
	},
	{
		ID:                "gpt-5.4-mini",
		Provider:          "openai",
		DisplayName:       "GPT-5.4 Mini",
		ContextWindow:     400000,
		MaxOutput:         16384,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: false,
		Aliases:           []string{"gpt5.4-mini"},
	},
	{
		ID:                "gpt-5.4-nano",
		Provider:          "openai",
		DisplayName:       "GPT-5.4 Nano",
		ContextWindow:     128000,
		MaxOutput:         16384,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: false,
		Aliases:           []string{"gpt5.4-nano"},
	},
	{
		ID:                "gpt-5.2",
		Provider:          "openai",
		DisplayName:       "GPT-5.2",
		ContextWindow:     128000,
		MaxOutput:         16384,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		Aliases:           []string{"gpt5.2"},
	},
	{
		ID:                "gpt-5.2-codex",
		Provider:          "openai",
		DisplayName:       "GPT-5.2 Codex",
		ContextWindow:     128000,
		MaxOutput:         32768,
		SupportsTools:     true,
		SupportsVision:    false,
		SupportsReasoning: true,
		Aliases:           []string{"codex", "gpt5.2-codex"},
	},
	{
		ID:                "gpt-4.1",
		Provider:          "openai",
		DisplayName:       "GPT-4.1",
		ContextWindow:     1000000,
		MaxOutput:         32768,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: false,
		Aliases:           []string{"gpt4.1"},
		// GPT-4.1 family: cached input is $0.50 vs $2.00 base.
		CacheReadMultiplier: 0.25,
	},
	{
		ID:                "gpt-4.1-mini",
		Provider:          "openai",
		DisplayName:       "GPT-4.1 Mini",
		ContextWindow:     1000000,
		MaxOutput:         32768,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: false,
		Aliases:           []string{"gpt4.1-mini"},
		// GPT-4.1 family: cached input is $0.10 vs $0.40 base.
		CacheReadMultiplier: 0.25,
	},
	{
		ID:                "gpt-4.1-nano",
		Provider:          "openai",
		DisplayName:       "GPT-4.1 Nano",
		ContextWindow:     1000000,
		MaxOutput:         32768,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: false,
		Aliases:           []string{"gpt4.1-nano"},
		// GPT-4.1 family: cached input is $0.025 vs $0.10 base.
		CacheReadMultiplier: 0.25,
	},
	{
		ID:                "o3",
		Provider:          "openai",
		DisplayName:       "o3",
		ContextWindow:     200000,
		MaxOutput:         100000,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		Aliases:           nil,
	},
	{
		ID:                "o4-mini",
		Provider:          "openai",
		DisplayName:       "o4-mini",
		ContextWindow:     200000,
		MaxOutput:         100000,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		Aliases:           nil,
	},
	// Older OpenAI models (still active on API)
	{
		ID:                "gpt-4o",
		Provider:          "openai",
		DisplayName:       "GPT-4o",
		ContextWindow:     128000,
		MaxOutput:         16384,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: false,
		Aliases:           []string{"4o"},
		// GPT-4o family: cached input is $1.25 vs $2.50 base.
		CacheReadMultiplier: 0.5,
	},
	{
		ID:                "gpt-4o-mini",
		Provider:          "openai",
		DisplayName:       "GPT-4o Mini",
		ContextWindow:     128000,
		MaxOutput:         16384,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: false,
		Aliases:           []string{"4o-mini"},
		// GPT-4o family: cached input is $0.075 vs $0.15 base.
		CacheReadMultiplier: 0.5,
	},
	// ── Gemini ───────────────────────────────────────────────
	// GA models first, then previews.
	{
		ID:                "gemini-2.5-pro",
		Provider:          "gemini",
		DisplayName:       "Gemini 2.5 Pro",
		ContextWindow:     1000000,
		MaxOutput:         65536,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		Aliases:           []string{"gemini-pro"},
	},
	{
		ID:                "gemini-2.5-flash",
		Provider:          "gemini",
		DisplayName:       "Gemini 2.5 Flash",
		ContextWindow:     1000000,
		MaxOutput:         65536,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		Aliases:           []string{"gemini-flash"},
	},
	{
		// Closed to new API keys as of mid-2026: a request 404s with "no
		// longer available to new users", though the models list still
		// advertises it and existing keys may still work. Kept because runs
		// that used it still need a price when their manifests are rebuilt —
		// removing the entry would silently zero their cost.
		ID:                "gemini-2.5-flash-lite",
		Provider:          "gemini",
		DisplayName:       "Gemini 2.5 Flash Lite",
		ContextWindow:     1000000,
		MaxOutput:         65536,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		Aliases:           []string{"gemini-flash-lite"},
	},
	{
		ID:                "gemini-3.1-pro-preview",
		Provider:          "gemini",
		DisplayName:       "Gemini 3.1 Pro Preview",
		ContextWindow:     1000000,
		MaxOutput:         65536,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		Aliases:           []string{"gemini-3.1-pro", "gemini-3-pro"},
	},
	{
		ID:                "gemini-3-flash-preview",
		Provider:          "gemini",
		DisplayName:       "Gemini 3 Flash Preview",
		ContextWindow:     1000000,
		MaxOutput:         65536,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		Aliases:           []string{"gemini-3-flash"},
	},
}

// GetModelInfo looks up a model by ID or alias. Returns nil if not found.
func GetModelInfo(modelID string) *ModelInfo {
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
// Pass an empty string to return all models.
func ListModels(provider string) []ModelInfo {
	var result []ModelInfo
	for _, m := range defaultCatalog {
		if provider == "" || m.Provider == provider {
			result = append(result, m)
		}
	}
	return result
}
