// ABOUTME: Shared model-resolution helper for cost accumulation pricing.
package agent

import "github.com/2389-research/tracker/llm"

// pricingModel returns the model to price a response against. The response's
// resolved model wins — on a provider/model failover the response carries a
// different model than config, and pricing from the config's rate table would
// be silently wrong (#524). config.Model is the fallback for adapters that
// leave resp.Model unset. Mirrors the event-path resolution in emitTurnMetrics
// (#508) so the run total and --max-cost BudgetGuard agree with turn_metrics.
func (s *Session) pricingModel(resp *llm.Response) string {
	if resp.Model != "" {
		return resp.Model
	}
	return s.config.Model
}
