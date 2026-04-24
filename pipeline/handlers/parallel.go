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
	if joinID := node.ParallelConfig().JoinID; joinID != "" {
		outcome.ContextUpdates = map[string]string{pipeline.ContextKeySuggestedNextNodes: joinID}
	}
	return outcome, nil
}

// resolveBranchEdges determines the branch target edges for a parallel node.
func (h *ParallelHandler) resolveBranchEdges(node *pipeline.Node) ([]*pipeline.Edge, error) {
	edges := h.collectBranchEdges(node)
	if len(edges) == 0 {
		return nil, fmt.Errorf("parallel node %q has no branch targets", node.ID)
	}
	return edges, nil
}

// collectBranchEdges builds the edge list from parallel_targets attr or outgoing edges.
func (h *ParallelHandler) collectBranchEdges(node *pipeline.Node) []*pipeline.Edge {
	if targetsAttr := node.ParallelConfig().ParallelTargets; targetsAttr != "" {
		return edgesFromTargetsAttr(node.ID, targetsAttr)
	}
	return h.edgesFromOutgoing(node)
}

// edgesFromTargetsAttr builds edges from a comma-separated parallel_targets attribute value.
func edgesFromTargetsAttr(fromID, targetsAttr string) []*pipeline.Edge {
	var edges []*pipeline.Edge
	for _, target := range strings.Split(targetsAttr, ",") {
		if target = strings.TrimSpace(target); target != "" {
			edges = append(edges, &pipeline.Edge{From: fromID, To: target})
		}
	}
	return edges
}

// edgesFromOutgoing returns outgoing edges excluding the join node.
func (h *ParallelHandler) edgesFromOutgoing(node *pipeline.Node) []*pipeline.Edge {
	joinID := node.ParallelConfig().JoinID
	var edges []*pipeline.Edge
	for _, e := range h.graph.OutgoingEdges(node.ID) {
		if e.To != joinID {
			edges = append(edges, e)
		}
	}
	return edges
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
	cfg := parallelNode.ParallelConfig()
	sem := makeSemaphore(cfg.MaxConcurrency)
	branchTimeout := cfg.BranchTimeout

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

// makeSemaphore returns a buffered channel used as a semaphore with the
// given capacity, or nil when max == 0 (unbounded concurrency).
func makeSemaphore(max int) chan struct{} {
	if max <= 0 {
		return nil
	}
	return make(chan struct{}, max)
}

// runBranch executes a single parallel branch in its own goroutine.
// sem, if non-nil, is a buffered channel used as a semaphore to cap concurrency.
// branchTimeout, if > 0, is applied as a per-branch context deadline.
func (h *ParallelHandler) runBranch(ctx context.Context, idx int, tn *pipeline.Node, snapshot map[string]string, artifactDir string, sem chan struct{}, branchTimeout time.Duration, resultsCh chan<- branchResultMsg, wg *sync.WaitGroup) {
	// Register wg.Done() up front so every early return path — including
	// the ctx.Done() branch on the semaphore wait below — still signals
	// completion. Previously the defer sat after the select, so a
	// cancellation while blocked on the concurrency slot could skip it
	// and deadlock wg.Wait() in executeBranches.
	defer wg.Done()

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

	defer h.recoverBranch(idx, tn, resultsCh)

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

	mergedUpdates := branchCtx.DiffFrom(snapshot)
	for k, v := range outcome.ContextUpdates {
		mergedUpdates[k] = v
	}

	pr := buildBranchResult(tn.ID, outcome, mergedUpdates, err)
	h.emitBranchComplete(tn.ID, pr)
	resultsCh <- branchResultMsg{index: idx, result: pr}
}

// buildBranchResult assembles a ParallelResult from the branch execution outcome.
func buildBranchResult(nodeID string, outcome pipeline.Outcome, mergedUpdates map[string]string, err error) ParallelResult {
	pr := ParallelResult{NodeID: nodeID, Status: outcome.Status, ContextUpdates: mergedUpdates, Stats: outcome.Stats}
	if err != nil {
		pr.Status = pipeline.OutcomeFail
		pr.Error = err.Error()
	}
	return pr
}

// recoverBranch is a deferred panic handler for parallel branch goroutines.
func (h *ParallelHandler) recoverBranch(idx int, tn *pipeline.Node, resultsCh chan<- branchResultMsg) {
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
}

// emitBranchComplete emits the appropriate pipeline event for a branch result.
func (h *ParallelHandler) emitBranchComplete(nodeID string, pr ParallelResult) {
	if pr.Status == pipeline.OutcomeFail {
		h.eventHandler.HandlePipelineEvent(pipeline.PipelineEvent{
			Type: pipeline.EventStageFailed, Timestamp: time.Now(), NodeID: nodeID, Message: pr.Error,
		})
	} else {
		h.eventHandler.HandlePipelineEvent(pipeline.PipelineEvent{
			Type: pipeline.EventStageCompleted, Timestamp: time.Now(), NodeID: nodeID,
		})
	}
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
			agg = &pipeline.SessionStats{ToolCalls: make(map[string]int)}
		}
		mergeSessionStats(agg, r.Stats)
	}
	return agg
}

// mergeSessionStats adds src fields into dst in-place. OR-propagates
// Estimated so that a heuristic-derived branch (e.g. ACP-backed) taints
// the aggregated parallel-node stats — otherwise downstream surfaces
// would render mixed metered+estimated parallel output as fully metered.
// EstimateSource is carried forward from the first estimated contributor;
// a later metered branch doesn't clear it.
func mergeSessionStats(dst, src *pipeline.SessionStats) {
	dst.Turns += src.Turns
	dst.TotalToolCalls += src.TotalToolCalls
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.TotalTokens += src.TotalTokens
	dst.CostUSD += src.CostUSD
	dst.Compactions += src.Compactions
	dst.CacheHits += src.CacheHits
	dst.CacheMisses += src.CacheMisses
	if src.LongestTurn > dst.LongestTurn {
		dst.LongestTurn = src.LongestTurn
	}
	dst.FilesModified = append(dst.FilesModified, src.FilesModified...)
	dst.FilesCreated = append(dst.FilesCreated, src.FilesCreated...)
	for name, count := range src.ToolCalls {
		dst.ToolCalls[name] += count
	}
	if src.Estimated {
		dst.Estimated = true
		if dst.EstimateSource == "" {
			dst.EstimateSource = src.EstimateSource
		}
	}
}

// parseBranchOverrides extracts branch.N.* attributes from a parallel node
// and returns a map of target node ID → override attrs.
// Format: branch.0.target=NodeA, branch.0.llm_model=gpt-4, etc.
func parseBranchOverrides(nodeAttrs map[string]string) map[string]map[string]string {
	indexed := indexBranchAttrs(nodeAttrs)
	return groupBranchOverridesByTarget(indexed)
}

// indexBranchAttrs groups branch.N.* node attributes by branch index N.
func indexBranchAttrs(nodeAttrs map[string]string) map[int]map[string]string {
	indexed := make(map[int]map[string]string)
	for key, val := range nodeAttrs {
		if idx, attrName, ok := parseBranchAttrKey(key); ok {
			if indexed[idx] == nil {
				indexed[idx] = make(map[string]string)
			}
			indexed[idx][attrName] = val
		}
	}
	return indexed
}

// parseBranchAttrKey parses a "branch.N.attrName" key.
// Returns (index, attrName, true) on success, (0, "", false) otherwise.
func parseBranchAttrKey(key string) (int, string, bool) {
	if !strings.HasPrefix(key, "branch.") {
		return 0, "", false
	}
	rest := key[len("branch."):]
	dotIdx := strings.Index(rest, ".")
	if dotIdx < 0 {
		return 0, "", false
	}
	idx, err := strconv.Atoi(rest[:dotIdx])
	if err != nil {
		return 0, "", false
	}
	return idx, rest[dotIdx+1:], true
}

// groupBranchOverridesByTarget converts indexed branch attrs to a target-keyed map.
func groupBranchOverridesByTarget(indexed map[int]map[string]string) map[string]map[string]string {
	byTarget := make(map[string]map[string]string)
	for _, branchAttrs := range indexed {
		target := branchAttrs["target"]
		if target == "" {
			continue
		}
		if overrides := branchAttrsToOverrides(branchAttrs); len(overrides) > 0 {
			byTarget[target] = overrides
		}
	}
	return byTarget
}

// branchAttrsToOverrides copies all branch attrs except "target" into an overrides map.
func branchAttrsToOverrides(branchAttrs map[string]string) map[string]string {
	overrides := make(map[string]string)
	for k, v := range branchAttrs {
		if k != "target" {
			overrides[k] = v
		}
	}
	return overrides
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
