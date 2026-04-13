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
	NodeID         string                 `json:"node_id"`
	Status         string                 `json:"status"`
	ContextUpdates map[string]string      `json:"context_updates,omitempty"`
	Error          string                 `json:"error,omitempty"`
	Stats          *pipeline.SessionStats `json:"stats,omitempty"`
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
	edges, err := h.resolveBranchEdges(node)
	if err != nil {
		return pipeline.Outcome{}, err
	}

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

	collected := h.executeBranches(ctx, node, edges, branchOverrides, pctx)

	resultsJSON, err := json.Marshal(collected)
	if err != nil {
		return pipeline.Outcome{}, fmt.Errorf("failed to marshal parallel results: %w", err)
	}
	pctx.Set("parallel.results", string(resultsJSON))

	status := aggregateStatus(collected)

	h.eventHandler.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventParallelCompleted,
		Timestamp: time.Now(),
		NodeID:    node.ID,
		Message:   fmt.Sprintf("fan-in complete, aggregate status: %s", status),
	})

	outcome := pipeline.Outcome{Status: status, Stats: aggregateBranchStats(collected)}
	if joinID := node.Attrs["parallel_join"]; joinID != "" {
		outcome.ContextUpdates = map[string]string{pipeline.ContextKeySuggestedNextNodes: joinID}
	}
	return outcome, nil
}

// resolveBranchEdges determines the branch target edges for a parallel node.
func (h *ParallelHandler) resolveBranchEdges(node *pipeline.Node) ([]*pipeline.Edge, error) {
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
		for _, e := range h.graph.OutgoingEdges(node.ID) {
			if e.To != joinID {
				edges = append(edges, e)
			}
		}
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("parallel node %q has no branch targets", node.ID)
	}
	return edges, nil
}

// branchResultMsg pairs a branch index with its parallel result.
type branchResultMsg struct {
	index  int
	result ParallelResult
}

// executeBranches spawns goroutines for each branch and collects results.
func (h *ParallelHandler) executeBranches(ctx context.Context, parallelNode *pipeline.Node, edges []*pipeline.Edge, branchOverrides map[string]map[string]string, pctx *pipeline.PipelineContext) []ParallelResult {
	snapshot := pctx.Snapshot()
	artifactDir, _ := pctx.GetInternal(pipeline.InternalKeyArtifactDir)

	// Parse max_concurrency — 0 means unlimited.
	var sem chan struct{}
	if maxStr := parallelNode.Attrs["max_concurrency"]; maxStr != "" {
		if n, err := strconv.Atoi(maxStr); err == nil && n > 0 {
			sem = make(chan struct{}, n)
		}
	}

	// Parse branch_timeout — zero means no timeout.
	var branchTimeout time.Duration
	if toStr := parallelNode.Attrs["branch_timeout"]; toStr != "" {
		if d, err := time.ParseDuration(toStr); err == nil && d > 0 {
			branchTimeout = d
		}
	}

	resultsCh := make(chan branchResultMsg, len(edges))
	var wg sync.WaitGroup

	for i, edge := range edges {
		targetNode, ok := h.graph.Nodes[edge.To]
		if !ok {
			resultsCh <- branchResultMsg{
				index:  i,
				result: ParallelResult{NodeID: edge.To, Status: pipeline.OutcomeFail, Error: fmt.Sprintf("target node %q not found in graph", edge.To)},
			}
			continue
		}

		execNode := applyBranchOverrides(targetNode, branchOverrides)
		wg.Add(1)
		go h.runBranch(ctx, i, execNode, snapshot, artifactDir, sem, branchTimeout, resultsCh, &wg)
	}

	wg.Wait()
	close(resultsCh)

	collected := make([]ParallelResult, len(edges))
	for br := range resultsCh {
		collected[br.index] = br.result
	}
	return collected
}

// runBranch executes a single parallel branch in its own goroutine.
// sem, if non-nil, is a buffered channel used as a semaphore to cap concurrency.
// branchTimeout, if > 0, is applied as a per-branch context deadline.
func (h *ParallelHandler) runBranch(ctx context.Context, idx int, tn *pipeline.Node, snapshot map[string]string, artifactDir string, sem chan struct{}, branchTimeout time.Duration, resultsCh chan<- branchResultMsg, wg *sync.WaitGroup) {
	// Acquire semaphore slot with context cancellation support.
	if sem != nil {
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		case <-ctx.Done():
			resultsCh <- branchResultMsg{
				index:  idx,
				result: ParallelResult{NodeID: tn.ID, Status: pipeline.OutcomeFail, Error: fmt.Sprintf("context canceled while waiting for concurrency slot: %v", ctx.Err())},
			}
			return
		}
	}

	defer wg.Done()
	defer func() {
		if r := recover(); r != nil {
			resultsCh <- branchResultMsg{
				index:  idx,
				result: ParallelResult{NodeID: tn.ID, Status: pipeline.OutcomeFail, Error: fmt.Sprintf("panic in parallel branch %q: %v", tn.ID, r)},
			}
			h.eventHandler.HandlePipelineEvent(pipeline.PipelineEvent{
				Type: pipeline.EventStageFailed, Timestamp: time.Now(), NodeID: tn.ID,
				Message: fmt.Sprintf("panic in branch %q: %v", tn.ID, r),
			})
		}
	}()

	h.eventHandler.HandlePipelineEvent(pipeline.PipelineEvent{
		Type: pipeline.EventStageStarted, Timestamp: time.Now(), NodeID: tn.ID,
		Message: fmt.Sprintf("parallel branch %q started", tn.ID),
	})

	branchCtx := pipeline.NewPipelineContextFrom(snapshot)
	if artifactDir != "" {
		branchCtx.SetInternal(pipeline.InternalKeyArtifactDir, artifactDir)
	}

	execCtx := ctx
	if branchTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, branchTimeout)
		defer cancel()
	}

	outcome, err := h.registry.Execute(execCtx, tn, branchCtx)

	// Auto-capture any pctx.Set() calls the handler made directly,
	// not just values returned via Outcome.ContextUpdates. This prevents
	// silent data loss when handlers write to the context as a side effect.
	mergedUpdates := branchCtx.DiffFrom(snapshot)
	for k, v := range outcome.ContextUpdates {
		mergedUpdates[k] = v // explicit ContextUpdates take priority
	}

	pr := ParallelResult{NodeID: tn.ID, Status: outcome.Status, ContextUpdates: mergedUpdates, Stats: outcome.Stats}
	if err != nil {
		pr.Status = pipeline.OutcomeFail
		pr.Error = err.Error()
	}

	if pr.Status == pipeline.OutcomeFail {
		h.eventHandler.HandlePipelineEvent(pipeline.PipelineEvent{
			Type: pipeline.EventStageFailed, Timestamp: time.Now(), NodeID: tn.ID, Message: pr.Error,
		})
	} else {
		h.eventHandler.HandlePipelineEvent(pipeline.PipelineEvent{
			Type: pipeline.EventStageCompleted, Timestamp: time.Now(), NodeID: tn.ID,
		})
	}

	resultsCh <- branchResultMsg{index: idx, result: pr}
}

// aggregateStatus returns success if at least one branch succeeded, fail otherwise.
func aggregateStatus(results []ParallelResult) string {
	for _, r := range results {
		if r.Status == pipeline.OutcomeSuccess {
			return pipeline.OutcomeSuccess
		}
	}
	return pipeline.OutcomeFail
}

// aggregateBranchStats sums SessionStats from all parallel branch results.
// Returns nil if no branches produced stats.
func aggregateBranchStats(results []ParallelResult) *pipeline.SessionStats {
	var agg *pipeline.SessionStats
	for _, r := range results {
		if r.Stats == nil {
			continue
		}
		if agg == nil {
			agg = &pipeline.SessionStats{
				ToolCalls: make(map[string]int),
			}
		}
		agg.Turns += r.Stats.Turns
		agg.TotalToolCalls += r.Stats.TotalToolCalls
		agg.InputTokens += r.Stats.InputTokens
		agg.OutputTokens += r.Stats.OutputTokens
		agg.TotalTokens += r.Stats.TotalTokens
		agg.CostUSD += r.Stats.CostUSD
		agg.Compactions += r.Stats.Compactions
		agg.CacheHits += r.Stats.CacheHits
		agg.CacheMisses += r.Stats.CacheMisses
		if r.Stats.LongestTurn > agg.LongestTurn {
			agg.LongestTurn = r.Stats.LongestTurn
		}
		agg.FilesModified = append(agg.FilesModified, r.Stats.FilesModified...)
		agg.FilesCreated = append(agg.FilesCreated, r.Stats.FilesCreated...)
		for name, count := range r.Stats.ToolCalls {
			agg.ToolCalls[name] += count
		}
	}
	return agg
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
