// ABOUTME: Parallel fan-out handler that spawns concurrent goroutines for each branch target.
// ABOUTME: Collects results, stores them as JSON in context, and returns aggregate success/fail.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/2389-research/tracker/pipeline"
)

// ParallelResult captures the outcome of a single branch executed during fan-out.
type ParallelResult struct {
	NodeID         string            `json:"node_id"`
	Status         string            `json:"status"`
	ContextUpdates map[string]string `json:"context_updates,omitempty"`
	Error          string            `json:"error,omitempty"`
}

// ParallelHandler implements fan-out execution: for each outgoing edge from
// the parallel node, it spawns a goroutine that executes the target node with
// an isolated context snapshot. It blocks until all branches complete, then
// stores the collected results as JSON in the pipeline context.
type ParallelHandler struct {
	graph    *pipeline.Graph
	registry *pipeline.HandlerRegistry
}

// NewParallelHandler creates a ParallelHandler with the given graph and registry.
func NewParallelHandler(graph *pipeline.Graph, registry *pipeline.HandlerRegistry) *ParallelHandler {
	return &ParallelHandler{graph: graph, registry: registry}
}

// Name returns the handler name used for registry lookup.
func (h *ParallelHandler) Name() string { return "parallel" }

// Execute fans out to all outgoing edge targets concurrently, collects results,
// and returns OutcomeSuccess if at least one branch succeeded, OutcomeFail if all failed.
func (h *ParallelHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	edges := h.graph.OutgoingEdges(node.ID)
	if len(edges) == 0 {
		return pipeline.Outcome{}, fmt.Errorf("parallel node %q has no outgoing edges", node.ID)
	}

	// Snapshot the shared context once before spawning branches.
	snapshot := pctx.Snapshot()

	type branchResult struct {
		index  int
		result ParallelResult
	}

	resultsCh := make(chan branchResult, len(edges))
	var wg sync.WaitGroup

	for i, edge := range edges {
		targetNode, ok := h.graph.Nodes[edge.To]
		if !ok {
			resultsCh <- branchResult{
				index: i,
				result: ParallelResult{
					NodeID: edge.To,
					Status: pipeline.OutcomeFail,
					Error:  fmt.Sprintf("target node %q not found in graph", edge.To),
				},
			}
			continue
		}

		wg.Add(1)
		go func(idx int, tn *pipeline.Node) {
			defer wg.Done()

			// Each branch gets its own isolated context from the snapshot.
			branchCtx := pipeline.NewPipelineContextFrom(snapshot)

			outcome, err := h.registry.Execute(ctx, tn, branchCtx)

			pr := ParallelResult{
				NodeID:         tn.ID,
				Status:         outcome.Status,
				ContextUpdates: outcome.ContextUpdates,
			}
			if err != nil {
				pr.Status = pipeline.OutcomeFail
				pr.Error = err.Error()
			}

			resultsCh <- branchResult{index: idx, result: pr}
		}(i, targetNode)
	}

	// Wait for all goroutines, then close the channel.
	wg.Wait()
	close(resultsCh)

	// Collect results preserving edge order.
	collected := make([]ParallelResult, len(edges))
	for br := range resultsCh {
		collected[br.index] = br.result
	}

	// Marshal results and store in context for the fan-in handler.
	resultsJSON, err := json.Marshal(collected)
	if err != nil {
		return pipeline.Outcome{}, fmt.Errorf("failed to marshal parallel results: %w", err)
	}
	pctx.Set("parallel.results", string(resultsJSON))

	// Determine aggregate status: success if at least one branch succeeded.
	anySuccess := false
	for _, r := range collected {
		if r.Status == pipeline.OutcomeSuccess {
			anySuccess = true
			break
		}
	}

	status := pipeline.OutcomeFail
	if anySuccess {
		status = pipeline.OutcomeSuccess
	}

	return pipeline.Outcome{Status: status}, nil
}
