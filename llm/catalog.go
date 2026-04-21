// ABOUTME: Model catalog providing a registry of known LLM models and their capabilities.
// ABOUTME: Supports lookup by ID/alias, listing by provider, and filtering by capability.
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
	InputCostPerM     float64  `json:"input_cost_per_m"`
	OutputCostPerM    float64  `json:"output_cost_per_m"`
	Aliases           []string `json:"aliases,omitempty"`
}

// defaultCatalog is the built-in registry of known models.
// Each provider section is ordered newest-first so GetLatestModel returns the
// most recent model for a given capability.
var defaultCatalog = []ModelInfo{
	// ── Anthropic ────────────────────────────────────────────
	{
		ID:                "claude-opus-4-7",
		Provider:          "anthropic",
		DisplayName:       "Claude Opus 4.7",
		ContextWindow:     1000000,
		MaxOutput:         128000,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		InputCostPerM:     5.0,
		OutputCostPerM:    25.0,
		Aliases:           []string{"opus-4-7", "claude-opus"},
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
		InputCostPerM:     3.0,
		OutputCostPerM:    15.0,
		Aliases:           []string{"sonnet-4-6", "claude-sonnet"},
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
		InputCostPerM:     5.0,
		OutputCostPerM:    25.0,
		Aliases:           []string{"opus-4-6"},
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
		InputCostPerM:     3.0,
		OutputCostPerM:    15.0,
		Aliases:           []string{"sonnet-4-5"},
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
		InputCostPerM:     1.0,
		OutputCostPerM:    5.0,
		Aliases:           []string{"haiku-4-5", "claude-haiku"},
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
		InputCostPerM:     2.50,
		OutputCostPerM:    15.0,
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
		InputCostPerM:     0.75,
		OutputCostPerM:    4.50,
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
		InputCostPerM:     0.20,
		OutputCostPerM:    1.25,
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
		InputCostPerM:     5.0,
		OutputCostPerM:    15.0,
		Aliases:           []string{"gpt5.2"},
	},
	{
		ID:                "gpt-5.2-mini",
		Provider:          "openai",
		DisplayName:       "GPT-5.2 Mini",
		ContextWindow:     128000,
		MaxOutput:         16384,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: false,
		InputCostPerM:     0.30,
		OutputCostPerM:    1.20,
		Aliases:           []string{"gpt5.2-mini"},
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
		InputCostPerM:     2.50,
		OutputCostPerM:    10.0,
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
		InputCostPerM:     2.00,
		OutputCostPerM:    8.00,
		Aliases:           []string{"gpt4.1"},
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
		InputCostPerM:     0.40,
		OutputCostPerM:    1.60,
		Aliases:           []string{"gpt4.1-mini"},
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
		InputCostPerM:     0.10,
		OutputCostPerM:    0.40,
		Aliases:           []string{"gpt4.1-nano"},
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
		InputCostPerM:     2.00,
		OutputCostPerM:    8.00,
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
		InputCostPerM:     1.10,
		OutputCostPerM:    4.40,
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
		InputCostPerM:     2.50,
		OutputCostPerM:    10.00,
		Aliases:           []string{"4o"},
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
		InputCostPerM:     0.15,
		OutputCostPerM:    0.60,
		Aliases:           []string{"4o-mini"},
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
		InputCostPerM:     1.25,
		OutputCostPerM:    10.0,
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
		InputCostPerM:     0.30,
		OutputCostPerM:    2.50,
		Aliases:           []string{"gemini-flash"},
	},
	{
		ID:                "gemini-2.5-flash-lite",
		Provider:          "gemini",
		DisplayName:       "Gemini 2.5 Flash Lite",
		ContextWindow:     1000000,
		MaxOutput:         65536,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		InputCostPerM:     0.10,
		OutputCostPerM:    0.40,
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
		InputCostPerM:     2.00,
		OutputCostPerM:    12.0,
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
		InputCostPerM:     0.50,
		OutputCostPerM:    3.0,
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

// GetLatestModel returns the first model matching the given provider and
// optional capability filter. Supported capability values are "reasoning",
// "vision", and "tools". Pass an empty string for no capability filter.
// Returns nil if no model matches.
func GetLatestModel(provider string, capability string) *ModelInfo {
	for i := range defaultCatalog {
		m := &defaultCatalog[i]
		if m.Provider != provider {
			continue
		}
		if !matchesCapability(m, capability) {
			continue
		}
		return m
	}
	return nil
}

// matchesCapability checks whether a model supports the requested capability.
func matchesCapability(m *ModelInfo, capability string) bool {
	switch capability {
	case "reasoning":
		return m.SupportsReasoning
	case "vision":
		return m.SupportsVision
	case "tools":
		return m.SupportsTools
	case "":
		return true
	default:
		return false
	}
}
