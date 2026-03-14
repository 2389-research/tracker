// ABOUTME: Model pricing table for LLM cost estimation.
// ABOUTME: Maps model names to per-million-token prices and computes estimated costs.
package llm

// ModelPricing holds the per-million-token cost for a model.
type ModelPricing struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

// pricing maps model identifiers to their pricing information.
var pricing = map[string]ModelPricing{
	"claude-sonnet-4-5": {InputPerMTok: 3.00, OutputPerMTok: 15.00},
	"claude-sonnet-4-6": {InputPerMTok: 3.00, OutputPerMTok: 15.00},
	"claude-opus-4-6":   {InputPerMTok: 15.00, OutputPerMTok: 75.00},
	"claude-haiku-4-5":  {InputPerMTok: 0.80, OutputPerMTok: 4.00},
	"gpt-4o":            {InputPerMTok: 2.50, OutputPerMTok: 10.00},
	"gpt-4o-mini":       {InputPerMTok: 0.15, OutputPerMTok: 0.60},
}

// EstimateCost returns the estimated dollar cost for the given model and token
// usage. Returns 0 for unknown models.
func EstimateCost(model string, usage Usage) float64 {
	p, ok := pricing[model]
	if !ok {
		return 0
	}
	input := float64(usage.InputTokens) / 1_000_000 * p.InputPerMTok
	output := float64(usage.OutputTokens) / 1_000_000 * p.OutputPerMTok
	return input + output
}
