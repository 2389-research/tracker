// ABOUTME: Parallel fan-out handler that spawns concurrent goroutines for each branch target.
// ABOUTME: Collects results, stores them as JSON in context, and returns aggregate success/fail.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

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
	graph        *pipeline.Graph
	registry     *pipeline.HandlerRegistry
	eventHandler pipeline.PipelineEventHandler
}

// NewParallelHandler creates a ParallelHandler with the given graph and registry.
func NewParallelHandler(graph *pipeline.Graph, registry *pipeline.HandlerRegistry, eventHandler pipeline.PipelineEventHandler) *ParallelHandler {
	if eventHandler == nil {
		eventHandler = pipeline.PipelineNoopHandler
	}
	return &ParallelHandler{graph: graph, registry: registry, eventHandler: eventHandler}
}

// Name returns the handler name used for registry lookup.
func (h *ParallelHandler) Name() string { return "parallel" }

// Execute fans out to all outgoing edge targets concurrently, collects results,
// and returns OutcomeSuccess if at least one branch succeeded, OutcomeFail if all failed.
// If the parallel node has branch.N.* attributes, those override the target node's
// attrs (e.g., llm_model, llm_provider, fidelity) for that specific branch.
func (h *ParallelHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	// Determine branch targets. Prefer the parallel_targets attr (set by the
	// dippin adapter from ParallelConfig.Targets) which excludes the fan-in
	// join edge. Fall back to outgoing graph edges for DOT-format pipelines
	// that don't have the attr.
	var edges []*pipeline.Edge
	joinID := node.Attrs["parallel_join"]
	if targetsAttr := node.Attrs["parallel_targets"]; targetsAttr != "" {
		for _, target := range strings.Split(targetsAttr, ",") {
			target = strings.TrimSpace(target)
			if target != "" {
				edges = append(edges, &pipeline.Edge{From: node.ID, To: target})
			}
		}
	} else {
		// DOT fallback: use outgoing edges, excluding the fan-in join.
		for _, e := range h.graph.OutgoingEdges(node.ID) {
			if e.To != joinID {
				edges = append(edges, e)
			}
		}
	}
	if len(edges) == 0 {
		return pipeline.Outcome{}, fmt.Errorf("parallel node %q has no branch targets", node.ID)
	}

	// Parse per-branch overrides from the parallel node's attrs.
	branchOverrides := parseBranchOverrides(node.Attrs)

	branchIDs := make([]string, len(edges))
	for i, edge := range edges {
		branchIDs[i] = edge.To
	}
	h.eventHandler.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventParallelStarted,
		Timestamp: time.Now(),
		NodeID:    node.ID,
		Message:   fmt.Sprintf("fan-out to %d branches: %v", len(edges), branchIDs),
	})

	// Snapshot the shared context once before spawning branches.
	snapshot := pctx.Snapshot()
	artifactDir, _ := pctx.GetInternal(pipeline.InternalKeyArtifactDir)

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

		// Apply per-branch overrides if present.
		execNode := applyBranchOverrides(targetNode, branchOverrides)

		wg.Add(1)
		go func(idx int, tn *pipeline.Node) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					resultsCh <- branchResult{
						index: idx,
						result: ParallelResult{
							NodeID: tn.ID,
							Status: pipeline.OutcomeFail,
							Error:  fmt.Sprintf("panic in parallel branch %q: %v", tn.ID, r),
						},
					}
					h.eventHandler.HandlePipelineEvent(pipeline.PipelineEvent{
						Type:      pipeline.EventStageFailed,
						Timestamp: time.Now(),
						NodeID:    tn.ID,
						Message:   fmt.Sprintf("panic in branch %q: %v", tn.ID, r),
					})
				}
			}()

			// Emit stage started so the TUI shows the branch as running.
			h.eventHandler.HandlePipelineEvent(pipeline.PipelineEvent{
				Type:      pipeline.EventStageStarted,
				Timestamp: time.Now(),
				NodeID:    tn.ID,
				Message:   fmt.Sprintf("parallel branch %q started", tn.ID),
			})

			// Each branch gets its own isolated context from the snapshot.
			branchCtx := pipeline.NewPipelineContextFrom(snapshot)
			if artifactDir != "" {
				branchCtx.SetInternal(pipeline.InternalKeyArtifactDir, artifactDir)
			}

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

			// Emit stage completed/failed so the TUI updates the branch status.
			if pr.Status == pipeline.OutcomeFail {
				h.eventHandler.HandlePipelineEvent(pipeline.PipelineEvent{
					Type:      pipeline.EventStageFailed,
					Timestamp: time.Now(),
					NodeID:    tn.ID,
					Message:   pr.Error,
				})
			} else {
				h.eventHandler.HandlePipelineEvent(pipeline.PipelineEvent{
					Type:      pipeline.EventStageCompleted,
					Timestamp: time.Now(),
					NodeID:    tn.ID,
				})
			}

			resultsCh <- branchResult{index: idx, result: pr}
		}(i, execNode)
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

	h.eventHandler.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventParallelCompleted,
		Timestamp: time.Now(),
		NodeID:    node.ID,
		Message:   fmt.Sprintf("fan-in complete, aggregate status: %s", status),
	})

	outcome := pipeline.Outcome{Status: status}

	// Hint the engine to navigate to the fan-in join node, skipping the
	// branch target edges (which the handler already dispatched internally).
	if joinID := node.Attrs["parallel_join"]; joinID != "" {
		outcome.ContextUpdates = map[string]string{
			"suggested_next_nodes": joinID,
		}
	}

	return outcome, nil
}

// branchOverride holds per-branch attr overrides parsed from the parallel node.
type branchOverride struct {
	target string
	attrs  map[string]string
}

// parseBranchOverrides extracts branch.N.* attributes from a parallel node
// and returns a map of target node ID → override attrs.
// Format: branch.0.target=NodeA, branch.0.llm_model=gpt-4, etc.
func parseBranchOverrides(nodeAttrs map[string]string) map[string]map[string]string {
	// First pass: group attrs by branch index.
	indexed := make(map[int]map[string]string)
	for key, val := range nodeAttrs {
		if !strings.HasPrefix(key, "branch.") {
			continue
		}
		rest := key[len("branch."):]
		dotIdx := strings.Index(rest, ".")
		if dotIdx < 0 {
			continue
		}
		idx, err := strconv.Atoi(rest[:dotIdx])
		if err != nil {
			continue
		}
		attrName := rest[dotIdx+1:]
		if indexed[idx] == nil {
			indexed[idx] = make(map[string]string)
		}
		indexed[idx][attrName] = val
	}

	// Second pass: key by target node ID.
	byTarget := make(map[string]map[string]string)
	for _, branchAttrs := range indexed {
		target := branchAttrs["target"]
		if target == "" {
			continue
		}
		overrides := make(map[string]string)
		for k, v := range branchAttrs {
			if k != "target" {
				overrides[k] = v
			}
		}
		if len(overrides) > 0 {
			byTarget[target] = overrides
		}
	}
	return byTarget
}

// applyBranchOverrides creates a shallow clone of the target node with
// branch-specific attr overrides applied. If no overrides exist for this
// target, returns the original node unchanged.
func applyBranchOverrides(target *pipeline.Node, overrides map[string]map[string]string) *pipeline.Node {
	branchAttrs, ok := overrides[target.ID]
	if !ok || len(branchAttrs) == 0 {
		return target
	}

	// Clone attrs and apply overrides.
	clonedAttrs := make(map[string]string, len(target.Attrs)+len(branchAttrs))
	for k, v := range target.Attrs {
		clonedAttrs[k] = v
	}
	for k, v := range branchAttrs {
		clonedAttrs[k] = v
	}

	return &pipeline.Node{
		ID:      target.ID,
		Shape:   target.Shape,
		Label:   target.Label,
		Handler: target.Handler,
		Attrs:   clonedAttrs,
	}
}
