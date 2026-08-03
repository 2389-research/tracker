// ABOUTME: Per-provider cost rollup for the TokenTracker middleware.
// ABOUTME: Maps accumulated Usage to dollar cost via a caller-supplied model resolver.
package llm

// ProviderCost is the per-provider cost rollup returned by TokenTracker.CostByProvider.
// Usage and USD are aggregated across every model the provider ran; Model names
// the model used for display (the provider's last-observed model — see the note
// on ModelForProvider). USD is the sum of each (provider, model) bucket priced
// at its own rate, so a multi-model provider is no longer mispriced by a single
// last-write-wins model (#527).
type ProviderCost struct {
	Usage Usage
	Model string
	USD   float64
}

// ModelResolver returns the model name that should be used for cost estimation
// for a given provider. Return "" when unknown — the entry is still included
// in the result with USD=0.
type ModelResolver func(provider string) string

// CostByProvider returns a per-provider cost rollup. Each (provider, model)
// bucket is priced at its own model's rate and the results are summed per
// provider, so a provider that ran multiple models is priced correctly and the
// total no longer depends on call ordering (#527). The caller-supplied resolver
// only prices buckets whose model was never observed ("" — e.g. subscription
// backends); a nil resolver treats those as $0.
func (t *TokenTracker) CostByProvider(resolve ModelResolver) map[string]ProviderCost {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]ProviderCost)
	for key, usage := range t.usage {
		model := key.model
		if model == "" && resolve != nil {
			model = resolve(key.provider)
		}
		pc := out[key.provider]
		pc.Usage = pc.Usage.Add(usage)
		pc.USD += EstimateCost(model, usage)
		out[key.provider] = pc
	}
	// Attach a representative model per provider for display, independent of the
	// map-iteration order above.
	for provider, pc := range out {
		pc.Model = t.lastModel[provider]
		if pc.Model == "" && resolve != nil {
			pc.Model = resolve(provider)
		}
		out[provider] = pc
	}
	return out
}

// TotalCostUSD sums CostByProvider to a single dollar figure using the same resolver.
func (t *TokenTracker) TotalCostUSD(resolve ModelResolver) float64 {
	var total float64
	for _, pc := range t.CostByProvider(resolve) {
		total += pc.USD
	}
	return total
}
