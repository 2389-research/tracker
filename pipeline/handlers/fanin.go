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
// returns OutcomeSuccess if any branch succeeded or OutcomeFail if all failed.
func (h *FanInHandler) Execute(_ context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	raw, ok := pctx.Get("parallel.results")
	if !ok {
		return pipeline.Outcome{}, fmt.Errorf("fan-in node %q: missing parallel.results in context", node.ID)
	}

	var results []ParallelResult
	if err := json.Unmarshal([]byte(raw), &results); err != nil {
		return pipeline.Outcome{}, fmt.Errorf("fan-in node %q: failed to unmarshal parallel.results: %w", node.ID, err)
	}

	merged, anySuccess := mergeSuccessfulBranches(results)
	status := pipeline.OutcomeFail
	if anySuccess {
		status = pipeline.OutcomeSuccess
	}

	return pipeline.Outcome{
		Status:         status,
		ContextUpdates: merged,
	}, nil
}

// mergeSuccessfulBranches collects context updates from successful branches.
// Returns the merged map and whether any branch succeeded.
func mergeSuccessfulBranches(results []ParallelResult) (map[string]string, bool) {
	merged := make(map[string]string)
	anySuccess := false
	for _, r := range results {
		if r.Status == pipeline.OutcomeSuccess {
			anySuccess = true
			for k, v := range r.ContextUpdates {
				merged[k] = v
			}
		}
	}
	return merged, anySuccess
}
