// ABOUTME: Verifies session cost accumulation prices against the response's
// resolved model (failover), not the configured model (#524).
package agent

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// TestSessionCostPricesWithResponseModel guards #524: on a provider/model
// failover the response carries a different model than config. The accumulated
// result.Usage.EstimatedCost (which flows to SessionStats.CostUSD → run.json
// and the --max-cost BudgetGuard) must be priced at the RESPONSE model's rate,
// not the config model's rate — matching the event-path fix in
// agent/session_events.go (#508).
func TestSessionCostPricesWithResponseModel(t *testing.T) {
	usage := llm.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000, TotalTokens: 2_000_000}
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
				Model:        "claude-opus-4-6", // failover target, differs from config
				Usage:        usage,             // no EstimatedCost -> forces local pricing
			},
		},
	}

	cfg := DefaultConfig()
	cfg.Model = "claude-haiku-4-5" // configured (cheap) model

	sess := mustNewSession(t, client, cfg)
	result, err := sess.Run(context.Background(), "do it")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantOpus := llm.EstimateCost("claude-opus-4-6", usage)
	haiku := llm.EstimateCost("claude-haiku-4-5", usage)
	if wantOpus == haiku {
		t.Fatalf("test precondition broken: opus and haiku price identically (%f)", wantOpus)
	}
	if result.Usage.EstimatedCost != wantOpus {
		t.Errorf("cost priced at wrong rate: got %f, want opus rate %f (config-haiku rate would be %f)",
			result.Usage.EstimatedCost, wantOpus, haiku)
	}
}
