// ABOUTME: Per-provider cost rollup for the TokenTracker middleware.
// ABOUTME: Maps accumulated Usage to dollar cost via a caller-supplied model resolver.
package llm

// ProviderCost is the per-provider cost rollup returned by TokenTracker.CostByProvider.
type ProviderCost struct {
	Usage Usage
	Model string
	USD   float64
}

// ModelResolver returns the model name that should be used for cost estimation
// for a given provider. Return "" when unknown — the entry is still included
// in the result with USD=0.
type ModelResolver func(provider string) string

// CostByProvider returns a per-provider cost rollup, resolving each provider's
// model via the caller-supplied resolver and pricing it via EstimateCost.
// A nil resolver is treated as one that always returns "".
func (t *TokenTracker) CostByProvider(resolve ModelResolver) map[string]ProviderCost {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]ProviderCost, len(t.usage))
	for provider, usage := range t.usage {
		var model string
		if resolve != nil {
			model = resolve(provider)
		}
		out[provider] = ProviderCost{
			Usage: usage,
			Model: model,
			USD:   EstimateCost(model, usage),
		}
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
