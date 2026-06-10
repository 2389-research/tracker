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

	// Validate the aggregation policy before dispatching any branch — a
	// misconfigured policy must fail fast, not after burning branch work.
	policy, err := resolveFanInPolicy(node.ID, node.ParallelConfig())
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

	collected, branchOverridesOut := h.executeBranches(ctx, node, edges, branchOverrides, pctx)

	resultsJSON, err := json.Marshal(collected)
	if err != nil {
		return pipeline.Outcome{}, fmt.Errorf("failed to marshal parallel results: %w", err)
	}
	pctx.Set("parallel.results", string(resultsJSON))

	status, policyDetail := aggregateStatus(collected, policy)

	msg := fmt.Sprintf("fan-in complete, aggregate status: %s", status)
	if policy.name != "any" {
		// Surface the policy evaluation (incl. failed branch IDs) so the TUI
		// and `tracker diagnose` can explain a policy-caused failure (#313).
		msg += " (" + policyDetail + ")"
	}
	h.eventHandler.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventParallelCompleted,
		Timestamp: time.Now(),
		NodeID:    node.ID,
		Message:   msg,
	})

	outcome := pipeline.Outcome{
		Status:        status,
		Stats:         aggregateBranchStats(collected),
		ChildOverride: aggregateChildOverrides(branchOverridesOut),
	}
	if joinID := node.ParallelConfig().JoinID; joinID != "" {
		outcome.ContextUpdates = map[string]string{pipeline.ContextKeySuggestedNextNodes: joinID}
	}
	return outcome, nil
}

// aggregateChildOverrides unions OverrideDetail slices across parallel branches
// in branch-result-order. Branches that propagate no override contribute
// nothing; branches that do are concatenated leaf-to-leaf so the parent's
// engine sees every child-side override in a deterministic, replayable order.
//
// Returns nil when no branch propagated any override so the engine's
// `if len(outcome.ChildOverride) > 0` guard short-circuits and we match
// PrependSubgraphPath's nil-for-empty convention.
//
// This is the third sticky-write site (own-graph flip-point, subgraph/
// manager_loop single-child absorption, parallel multi-branch aggregation).
// Per-branch SubgraphPath prepending already happened inside each branch's
// child handler (a subgraph or manager_loop branch target prepends its own
// node ID via PrependSubgraphPath). The parallel node ID is intentionally
// NOT prepended here — parallel is a fan-out, not a subgraph boundary; the
// branch IDs already identify which fork the override came from via the
// subgraph child's prepend.
func aggregateChildOverrides(branchOverrides [][]pipeline.OverrideDetail) []pipeline.OverrideDetail {
	var aggregated []pipeline.OverrideDetail
	for _, br := range branchOverrides {
		if len(br) > 0 {
			aggregated = append(aggregated, br...)
		}
	}
	return aggregated
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
//
// childOverride is carried alongside ParallelResult (not folded into it) so
// the parent-level aggregation can union OverrideDetail entries across
// branches without leaking them into the JSON-serialized parallel.results
// context value. The ParallelResult is an audit-facing record; ChildOverride
// is a vertical propagation channel up to the engine's sticky-list absorption.
type branchResultMsg struct {
	index         int
	result        ParallelResult
	childOverride []pipeline.OverrideDetail
}

// executeBranches spawns goroutines for each branch and collects results.
// Returns the per-branch ParallelResult slice (audit-facing, JSON-serialized
// into parallel.results) and the per-branch ChildOverride slices, both indexed
// in branch-result-order for deterministic aggregation downstream.
func (h *ParallelHandler) executeBranches(ctx context.Context, parallelNode *pipeline.Node, edges []*pipeline.Edge, branchOverrides map[string]map[string]string, pctx *pipeline.PipelineContext) ([]ParallelResult, [][]pipeline.OverrideDetail) {
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
				result: ParallelResult{NodeID: edge.To, Status: string(pipeline.OutcomeFail), Error: fmt.Sprintf("target node %q not found in graph", edge.To)},
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
	overrides := make([][]pipeline.OverrideDetail, len(edges))
	for br := range resultsCh {
		collected[br.index] = br.result
		overrides[br.index] = br.childOverride
	}
	return collected, overrides
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
				result: ParallelResult{NodeID: tn.ID, Status: string(pipeline.OutcomeFail), Error: fmt.Sprintf("context canceled while waiting for concurrency slot: %v", ctx.Err())},
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
	// Carry the branch's ChildOverride up to the parent aggregation site (not
	// onto ParallelResult, which is JSON-serialized into the parallel.results
	// audit value). Empty/nil propagates as nil — the aggregator unions
	// per-branch slices and drops empties.
	resultsCh <- branchResultMsg{index: idx, result: pr, childOverride: outcome.ChildOverride}
}

// buildBranchResult assembles a ParallelResult from the branch execution outcome.
func buildBranchResult(nodeID string, outcome pipeline.Outcome, mergedUpdates map[string]string, err error) ParallelResult {
	pr := ParallelResult{NodeID: nodeID, Status: outcome.Status, ContextUpdates: mergedUpdates, Stats: outcome.Stats}
	if err != nil {
		pr.Status = string(pipeline.OutcomeFail)
		pr.Error = err.Error()
	}
	return pr
}

// recoverBranch is a deferred panic handler for parallel branch goroutines.
func (h *ParallelHandler) recoverBranch(idx int, tn *pipeline.Node, resultsCh chan<- branchResultMsg) {
	if r := recover(); r != nil {
		resultsCh <- branchResultMsg{
			index:  idx,
			result: ParallelResult{NodeID: tn.ID, Status: string(pipeline.OutcomeFail), Error: fmt.Sprintf("panic in parallel branch %q: %v", tn.ID, r)},
		}
		h.eventHandler.HandlePipelineEvent(pipeline.PipelineEvent{
			Type: pipeline.EventStageFailed, Timestamp: time.Now(), NodeID: tn.ID,
			Message: fmt.Sprintf("panic in branch %q: %v", tn.ID, r),
		})
	}
}

// emitBranchComplete emits the appropriate pipeline event for a branch result.
func (h *ParallelHandler) emitBranchComplete(nodeID string, pr ParallelResult) {
	if pr.Status == string(pipeline.OutcomeFail) {
		h.eventHandler.HandlePipelineEvent(pipeline.PipelineEvent{
			Type: pipeline.EventStageFailed, Timestamp: time.Now(), NodeID: nodeID, Message: pr.Error,
		})
	} else {
		h.eventHandler.HandlePipelineEvent(pipeline.PipelineEvent{
			Type: pipeline.EventStageCompleted, Timestamp: time.Now(), NodeID: nodeID,
		})
	}
}

// aggregateStatus evaluates the branch results against the fan-in policy
// (default "any": success if at least one branch succeeded) and returns the
// aggregate status plus a human-readable policy detail string.
func aggregateStatus(results []ParallelResult, policy fanInPolicy) (string, string) {
	successes, failed := tallyBranches(results)
	status := string(pipeline.OutcomeFail)
	if policy.satisfied(successes, len(results)) {
		status = string(pipeline.OutcomeSuccess)
	}
	return status, policy.detail(successes, len(results), failed)
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
