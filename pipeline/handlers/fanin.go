// ABOUTME: Fan-in handler that reads parallel branch results and merges successful branch contexts.
// ABOUTME: Returns success if any branch succeeded, fail if all failed or results are empty.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/2389-research/tracker/pipeline"
)

// FanInHandler implements the fan-in side of parallel execution. It reads
// the JSON-encoded []ParallelResult stored by the parallel handler, merges
// context updates from successful branches, and returns an aggregate outcome.
type FanInHandler struct{}

// NewFanInHandler creates a FanInHandler. No graph or registry is needed
// because fan-in only reads results stored earlier by the parallel handler.
func NewFanInHandler() *FanInHandler {
	return &FanInHandler{}
}

// Name returns the handler name used for registry lookup.
func (h *FanInHandler) Name() string { return "parallel.fan_in" }

// Execute reads "parallel.results" from the pipeline context, unmarshals the
// branch results, merges context updates from all successful branches (in
// order, so later branches overwrite earlier ones for the same key), and
// evaluates the node's fan-in policy (#313) — default "any": OutcomeSuccess
// if any branch succeeded, OutcomeFail if all failed.
func (h *FanInHandler) Execute(_ context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	policy, err := resolveFanInPolicy(node.ID, node.ParallelConfig())
	if err != nil {
		return pipeline.Outcome{}, err
	}

	raw, ok := pctx.Get("parallel.results")
	if !ok {
		return pipeline.Outcome{}, fmt.Errorf("fan-in node %q: missing parallel.results in context", node.ID)
	}

	var results []ParallelResult
	if err := json.Unmarshal([]byte(raw), &results); err != nil {
		return pipeline.Outcome{}, fmt.Errorf("fan-in node %q: failed to unmarshal parallel.results: %w", node.ID, err)
	}

	merged := mergeSuccessfulBranches(results)
	successes, failed := tallyBranches(results)
	status := pipeline.OutcomeFail
	if policy.satisfied(successes, len(results)) {
		status = pipeline.OutcomeSuccess
	}
	if policy.name != "any" {
		// Record the policy evaluation in context so the audit trail /
		// diagnose can explain the routing (#313). Written on success too —
		// pipeline context persists across re-review loops, so a fail-only
		// write would leave a stale "failed" detail after a later pass
		// succeeds. Successful-branch context still merges above so
		// downstream escalation gates can reference partial output.
		merged["fan_in.policy_detail"] = policy.detail(successes, len(results), failed)
	}

	return pipeline.Outcome{
		Status:         string(status),
		ContextUpdates: merged,
	}, nil
}

// mergeSuccessfulBranches collects context updates from successful branches.
func mergeSuccessfulBranches(results []ParallelResult) map[string]string {
	merged := make(map[string]string)
	for _, r := range results {
		if r.Status == string(pipeline.OutcomeSuccess) {
			for k, v := range r.ContextUpdates {
				merged[k] = v
			}
		}
	}
	return merged
}
