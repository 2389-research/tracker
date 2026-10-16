// ABOUTME: Cost-ceiling visibility for openai calls billed to a ChatGPT subscription
// ABOUTME: (design §3 "Cost (D6)") — a run-start warning plus the EstimateRun pricing switch.
package tracker

import (
	"strconv"

	"github.com/2389-research/tracker/internal/chatgptauth"
	"github.com/2389-research/tracker/internal/diag"
	"github.com/2389-research/tracker/pipeline"
)

// chatGPTSubscriptionActive reports whether TRACKER_OPENAI_AUTH=chatgpt, so
// openai calls bill to the subscription (the OpenAISubscription label, priced
// at $0 regardless of model — design §3 "Cost (D6)") instead of the API key.
// An invalid switch value is treated as "no": buildClient already hard-fails
// Run on an invalid value before buildEngineOpts runs, and EstimateRun has no
// client to fail, so this is the same conservative default either way.
func chatGPTSubscriptionActive() bool {
	mode, err := chatgptauth.ModeFromEnv()
	return err == nil && mode == chatgptauth.ModeChatGPT
}

// warnSubscriptionCeilings warns once, at run start, when a dollar ceiling is
// configured but can't see ChatGPT-subscription openai calls: those are
// priced at $0, so --max-cost, Config.Budget, a .dip `defaults:
// max_cost_cents` (all folded into budget.MaxCostCents by ResolveBudgetLimits,
// called just before this), and any node's `params: max_cost_usd` (#304, e.g.
// build_product's ReviewCodex cap) never count them — a runaway node keeps
// spending plan usage until a usage-limit pause, not a budget halt. Silent
// whenever the subscription isn't active, no codergen node routes to openai,
// or no dollar ceiling is set at all; --max-tokens is unaffected and needs no
// warning.
func warnSubscriptionCeilings(budget pipeline.BudgetLimits, graph *pipeline.Graph) {
	if graph == nil || !chatGPTSubscriptionActive() {
		return
	}
	if !graphRoutesOpenAI(graph) || !hasDollarCeiling(budget, graph) {
		return
	}
	diag.Warnf("[tracker] openai runs on your ChatGPT subscription and costs $0 here: --max-cost, max_cost_cents and max_cost_usd ceilings don't count it. --max-tokens still applies.")
}

// graphRoutesOpenAI reports whether any codergen node's provider — node
// llm_provider, else graph llm_provider — is openai.
func graphRoutesOpenAI(graph *pipeline.Graph) bool {
	for _, n := range graph.Nodes {
		if n.Handler != "codergen" {
			continue
		}
		provider := n.Attrs["llm_provider"]
		if provider == "" {
			provider = graph.Attrs["llm_provider"]
		}
		if provider == "openai" {
			return true
		}
	}
	return false
}

// hasDollarCeiling reports whether any dollar-denominated cost ceiling is
// configured: the resolved budget.MaxCostCents, or a positive max_cost_usd on
// the graph or on any node.
func hasDollarCeiling(budget pipeline.BudgetLimits, graph *pipeline.Graph) bool {
	if budget.MaxCostCents > 0 {
		return true
	}
	if positiveMaxCostUSD(graph.Attrs) {
		return true
	}
	for _, n := range graph.Nodes {
		if positiveMaxCostUSD(n.Attrs) {
			return true
		}
	}
	return false
}

// positiveMaxCostUSD reports whether attrs carries a positive max_cost_usd —
// shared by the graph-level and per-node checks in hasDollarCeiling.
func positiveMaxCostUSD(attrs map[string]string) bool {
	v, ok := attrs["max_cost_usd"]
	if !ok || v == "" {
		return false
	}
	f, err := strconv.ParseFloat(v, 64)
	return err == nil && f > 0
}
