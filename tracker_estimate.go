// ABOUTME: EstimateRun — a rough pre-run cost/scale ballpark from static analysis.
// ABOUTME: Prices each agent node's model against a turn heuristic; deliberately a range.
package tracker

import (
	"context"
	"sort"
	"strconv"

	"github.com/2389-research/tracker/llm"
)

// Per-agent-turn token heuristics. Static analysis can't know real usage, so
// these are deliberately rough — the wide Low..High range is the honest signal.
const (
	estInputTokensPerTurn  = 4000
	estOutputTokensPerTurn = 1500
	estDefaultMaxTurns     = 8
)

// RunEstimate is a rough pre-run cost/scale ballpark. It is an ESTIMATE: actual
// cost depends on how many turns each agent uses and how many times loops run,
// which can't be known statically — hence the wide Low..High spread. Use it to
// set expectations and a budget ceiling, not as a precise quote.
type RunEstimate struct {
	Steps             int      `json:"steps"`              // execution-plan length
	AgentNodes        int      `json:"agent_nodes"`        // LLM agent nodes
	Models            []string `json:"models"`             // distinct models, sorted
	LowUSD            float64  `json:"low_usd"`            // ~1 turn per agent node
	ExpectedUSD       float64  `json:"expected_usd"`       // a typical fraction of max turns
	HighUSD           float64  `json:"high_usd"`           // max turns per agent node
	SubscriptionNodes int      `json:"subscription_nodes"` // openai agent nodes billed to the ChatGPT subscription ($0 here)
}

// EstimateRun parses source and returns a rough cost/scale estimate. It runs the
// same static simulation as Simulate, then prices each agent node's model.
// Options (WithSource) are forwarded to Simulate.
func EstimateRun(ctx context.Context, source string, opts ...SourceOption) (*RunEstimate, error) {
	report, err := Simulate(ctx, source, opts...)
	if err != nil {
		return nil, err
	}
	return estimateFromReport(report), nil
}

func estimateFromReport(r *SimulateReport) *RunEstimate {
	est := &RunEstimate{Steps: len(r.ExecutionPlan)}
	chatgpt := chatGPTSubscriptionActive()
	models := map[string]bool{}
	for _, n := range r.Nodes {
		if !isAgentNode(n) {
			continue
		}
		est.AgentNodes++
		priceAgentNode(est, n, r.GraphAttrs, chatgpt, models)
	}
	for m := range models {
		est.Models = append(est.Models, m)
	}
	sort.Strings(est.Models)
	return est
}

// priceAgentNode folds one agent node's cost range into est: records its
// model, resolves its billing provider, and adds its Low/Expected/High
// contribution. Under chatgpt, an openai node bills to the subscription
// (llm.OpenAISubscription — priced at $0 regardless of model) and counts
// toward est.SubscriptionNodes instead of its ordinary provider.
func priceAgentNode(est *RunEstimate, n SimNode, graphAttrs map[string]string, chatgpt bool, models map[string]bool) {
	model := nodeModel(n, graphAttrs)
	if model != "" {
		models[model] = true
	}
	provider := nodeProvider(n, graphAttrs)
	if chatgpt && provider == "openai" {
		provider = llm.OpenAISubscription
		est.SubscriptionNodes++
	}
	maxTurns := nodeMaxTurns(n)
	est.LowUSD += turnCost(provider, model, 1)
	est.ExpectedUSD += turnCost(provider, model, expectedTurns(maxTurns))
	est.HighUSD += turnCost(provider, model, maxTurns)
}

// isAgentNode reports whether a node makes LLM calls (a codergen node with a
// prompt or model — bare passthrough start/exit nodes have neither).
func isAgentNode(n SimNode) bool {
	if n.Handler != "codergen" {
		return false
	}
	return n.Attrs["prompt"] != "" || n.Attrs["llm_model"] != ""
}

func nodeModel(n SimNode, graphAttrs map[string]string) string {
	if m := n.Attrs["llm_model"]; m != "" {
		return m
	}
	return graphAttrs["llm_model"]
}

// nodeProvider resolves a node's llm_provider the same way nodeModel resolves
// llm_model: node attr first, else the graph default.
func nodeProvider(n SimNode, graphAttrs map[string]string) string {
	if p := n.Attrs["llm_provider"]; p != "" {
		return p
	}
	return graphAttrs["llm_provider"]
}

func nodeMaxTurns(n SimNode) int {
	if t, err := strconv.Atoi(n.Attrs["max_turns"]); err == nil && t > 0 {
		return t
	}
	return estDefaultMaxTurns
}

// expectedTurns is a rough "typical" turn count — about a third of the cap, since
// agents usually finish well before exhausting their turn budget.
func expectedTurns(maxTurns int) int {
	t := (maxTurns + 2) / 3
	if t < 1 {
		return 1
	}
	if t > maxTurns {
		return maxTurns
	}
	return t
}

// turnCost prices turns turns of a model for provider. provider is the
// *billing* provider — the caller substitutes llm.OpenAISubscription for an
// openai node under TRACKER_OPENAI_AUTH=chatgpt, which EstimateCostForProvider
// prices at $0 regardless of model.
func turnCost(provider, model string, turns int) float64 {
	if model == "" || turns <= 0 {
		return 0
	}
	return llm.EstimateCostForProvider(provider, model, llm.Usage{
		InputTokens:  turns * estInputTokensPerTurn,
		OutputTokens: turns * estOutputTokensPerTurn,
	})
}
