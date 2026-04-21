// ABOUTME: Cost estimation for LLM usage based on the model catalog.
// ABOUTME: Prices input, output, and cache tokens using per-model rates from the catalog.
package llm

// EstimateCost returns the estimated dollar cost for the given model and token
// usage. Looks up pricing from the model catalog (supports ID and aliases).
// Returns 0 for unknown models. Cache read tokens are priced at 10% of input
// rate; cache write tokens at 25% of input rate (Anthropic pricing convention,
// applied uniformly since other providers use similar discounts).
func EstimateCost(model string, usage Usage) float64 {
	info := GetModelInfo(model)
	if info == nil {
		return 0
	}
	input := float64(usage.InputTokens) / 1_000_000 * info.InputCostPerM
	output := float64(usage.OutputTokens) / 1_000_000 * info.OutputCostPerM

	var cacheRead, cacheWrite float64
	if usage.CacheReadTokens != nil {
		cacheRead = float64(*usage.CacheReadTokens) / 1_000_000 * info.InputCostPerM * 0.1
	}
	if usage.CacheWriteTokens != nil {
		cacheWrite = float64(*usage.CacheWriteTokens) / 1_000_000 * info.InputCostPerM * 0.25
	}

	return input + output + cacheRead + cacheWrite
}
