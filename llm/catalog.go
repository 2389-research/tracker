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
var defaultCatalog = []ModelInfo{
	// Anthropic models
	{
		ID:                "claude-opus-4-6",
		Provider:          "anthropic",
		DisplayName:       "Claude Opus 4.6",
		ContextWindow:     200000,
		MaxOutput:         32000,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		InputCostPerM:     15.0,
		OutputCostPerM:    75.0,
		Aliases:           []string{"opus-4-6", "claude-opus"},
	},
	{
		ID:                "claude-sonnet-4-5",
		Provider:          "anthropic",
		DisplayName:       "Claude Sonnet 4.5",
		ContextWindow:     200000,
		MaxOutput:         16000,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		InputCostPerM:     3.0,
		OutputCostPerM:    15.0,
		Aliases:           []string{"sonnet-4-5", "claude-sonnet"},
	},
	// OpenAI models
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
		ID:                "gpt-5.4",
		Provider:          "openai",
		DisplayName:       "GPT-5.4",
		ContextWindow:     128000,
		MaxOutput:         16384,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		InputCostPerM:     5.0,
		OutputCostPerM:    15.0,
		Aliases:           []string{"gpt5.4"},
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
	// Gemini models
	{
		ID:                "gemini-3-pro-preview",
		Provider:          "gemini",
		DisplayName:       "Gemini 3 Pro Preview",
		ContextWindow:     1000000,
		MaxOutput:         8192,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: true,
		InputCostPerM:     3.50,
		OutputCostPerM:    10.50,
		Aliases:           []string{"gemini-3-pro"},
	},
	{
		ID:                "gemini-3-flash-preview",
		Provider:          "gemini",
		DisplayName:       "Gemini 3 Flash Preview",
		ContextWindow:     1000000,
		MaxOutput:         8192,
		SupportsTools:     true,
		SupportsVision:    true,
		SupportsReasoning: false,
		InputCostPerM:     0.075,
		OutputCostPerM:    0.30,
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
