// ABOUTME: Parallel fan-out handler that spawns concurrent goroutines for each branch target.
// ABOUTME: Collects results, stores them as JSON in context, and returns aggregate success/fail.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
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

// Execute fans out to all outgoing edge targets concurrently, collects
// results, and aggregates them per the node's fan_in_policy (#313): the
// default "any" returns OutcomeSuccess if at least one branch succeeded,
// "all" requires every branch, and "quorum" requires at least `quorum`
// successful branches.
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

	// Refuse a branch-target fallback_target before dispatching any branch
	// (#313 defect 2): runBranch calls registry.Execute directly and never
	// passes through Engine.checkStrictFailure/findFallbackTarget, so the
	// attr would be silently inert at runtime. Route branch failure at the
	// aggregating node instead (fan_in_policy + a conditional fail edge).
	if err := refuseBranchFallbackTargets(node.ID, edges, h.graph, node.Attrs); err != nil {
		return pipeline.Outcome{}, err
	}

	h.emitParallelStarted(node.ID, edges, pctx)

	collected, branchOverridesOut, childUsages, pauseErr := h.executeBranches(ctx, node, edges, branchOverrides, pctx)

	resultsJSON, err := json.Marshal(collected)
	if err != nil {
		return pipeline.Outcome{}, fmt.Errorf("failed to marshal parallel results: %w", err)
	}
	pctx.Set("parallel.results", string(resultsJSON))

	status, policyDetail := aggregateStatus(collected, policy)
	h.emitParallelCompleted(node.ID, status, policy, policyDetail, pctx)

	outcome := buildParallelOutcome(node, policy, status, policyDetail, collected, branchOverridesOut, childUsages)
	// #487: a branch that hit billing/quota exhaustion must halt the run in a
	// resumable paused terminal — not be flattened to a branch fail (or masked as
	// node success under the "any" policy). Returning the PauseError lets the
	// engine's pause path (asPauseError -> haltForPause) fire. A branch pause
	// takes precedence over the policy tally: the node cannot legitimately
	// complete while a branch is blocked on an empty balance.
	if pauseErr != nil {
		return outcome, pauseErr
	}
	return outcome, nil
}

// emitParallelStarted emits the fan-out EventParallelStarted event naming
// every dispatched branch target.
func (h *ParallelHandler) emitParallelStarted(nodeID string, edges []*pipeline.Edge, pctx *pipeline.PipelineContext) {
	branchIDs := make([]string, len(edges))
	for i, edge := range edges {
		branchIDs[i] = edge.To
	}
	h.eventHandler.HandlePipelineEvent(stampRunID(pipeline.PipelineEvent{
		Type:      pipeline.EventParallelStarted,
		Timestamp: time.Now(),
		NodeID:    nodeID,
		Message:   fmt.Sprintf("fan-out to %d branches: %v", len(edges), branchIDs),
	}, pctx))
}

// emitParallelCompleted emits the fan-in EventParallelCompleted event,
// surfacing the policy evaluation (incl. failed branch IDs) for non-default
// policies so the TUI and `tracker diagnose` can explain a policy-caused
// failure (#313).
func (h *ParallelHandler) emitParallelCompleted(nodeID string, status pipeline.TerminalStatus, policy fanInPolicy, policyDetail string, pctx *pipeline.PipelineContext) {
	msg := fmt.Sprintf("fan-in complete, aggregate status: %s", status)
	if policy.name != "any" {
		msg += " (" + policyDetail + ")"
	}
	h.eventHandler.HandlePipelineEvent(stampRunID(pipeline.PipelineEvent{
		Type:      pipeline.EventParallelCompleted,
		Timestamp: time.Now(),
		NodeID:    nodeID,
		Message:   msg,
	}, pctx))
}

// buildParallelOutcome assembles the aggregate Outcome for a completed
// fan-out: status/stats/child-overrides, the join suggestion, and (for
// non-default policies) the fan_in.policy_detail breadcrumb.
//
// The join is suggested EXCEPT when a non-default policy is unsatisfied.
// Leaving the suggestion in place would let edge selection fall through
// selectBySuggested to the join (bypassing strict-failure because
// conditional edges exist), and a default-any fan-in downstream would mask
// the very failure the policy surfaced (Codex review, PR #344). Under the
// default any policy the suggestion is kept even on all-fail — existing
// workflows route that failure at the fan-in node.
//
// The policy-detail breadcrumb is recorded here too (not just at the
// fan-in node) — a policy failure suppresses the join suggestion, so the
// fan-in node (the other fan_in.policy_detail writer) may never run, and
// diagnose/audit would otherwise lose the structured breadcrumb (Copilot,
// PR #344). Written on success as well so a later pass can't leave a stale
// "failed" detail in context.
func buildParallelOutcome(node *pipeline.Node, policy fanInPolicy, status pipeline.TerminalStatus, policyDetail string, collected []ParallelResult, branchOverridesOut [][]pipeline.OverrideDetail, childUsages []*pipeline.UsageSummary) pipeline.Outcome {
	outcome := pipeline.Outcome{
		Status:        status,
		Stats:         aggregateBranchStats(collected),
		ChildOverride: aggregateChildOverrides(branchOverridesOut),
		// #595: a branch whose target is a subgraph/manager_loop reports its
		// nested spend via Outcome.ChildUsage (not Stats). Fold those across
		// branches into one aggregate so the parent trace's AggregateUsage and
		// BudgetGuard see parallel-nested child spend exactly once — the branch
		// Stats channel (codergen branches) stays disjoint, so no double-count.
		ChildUsage:     pipeline.CombineChildUsage(childUsages),
		ContextUpdates: make(map[string]string),
	}
	policyBlocked := policy.name != "any" && status != pipeline.OutcomeSuccess
	if joinID := node.ParallelConfig().JoinID; joinID != "" && !policyBlocked {
		outcome.SuggestedNextNodes = []string{joinID} // #451: typed hint; engine mirrors to context (engine_run.go)
	}
	if policy.name != "any" {
		outcome.ContextUpdates[pipeline.ContextKeyFanInPolicyDetail] = policyDetail
	}
	return outcome
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
	// childUsage carries a branch's aggregated child-run usage (subgraph or
	// manager_loop target) up to the parent so parallel-branch child spend
	// reaches Trace.AggregateUsage and BudgetGuard (#595). Kept off
	// ParallelResult (JSON-serialized audit value) — like childOverride, it is
	// a vertical propagation channel, not part of the audit record. nil for a
	// branch whose target reports no child usage (e.g. a plain codergen node,
	// whose spend flows via Stats instead).
	childUsage *pipeline.UsageSummary
	// pauseErr carries a branch's recoverable PauseError (e.g. billing/quota
	// exhaustion, #487) up to the parent so the parallel node propagates a
	// resumable pause instead of flattening it to a plain fail.
	pauseErr *pipeline.PauseError
}

// executeBranches spawns goroutines for each branch and collects results.
// Returns the per-branch ParallelResult slice (audit-facing, JSON-serialized
// into parallel.results), the per-branch ChildOverride slices, and the
// per-branch ChildUsage summaries — all indexed in branch-result-order for
// deterministic aggregation downstream.
func (h *ParallelHandler) executeBranches(ctx context.Context, parallelNode *pipeline.Node, edges []*pipeline.Edge, branchOverrides map[string]map[string]string, pctx *pipeline.PipelineContext) ([]ParallelResult, [][]pipeline.OverrideDetail, []*pipeline.UsageSummary, *pipeline.PauseError) {
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
		go h.runBranch(ctx, i, execNode, snapshot, artifactDir, sem, branchTimeout, resultsCh, &wg, pctx)
	}

	wg.Wait()
	close(resultsCh)

	collected := make([]ParallelResult, len(edges))
	overrides := make([][]pipeline.OverrideDetail, len(edges))
	childUsages := make([]*pipeline.UsageSummary, len(edges))
	var pauseErr *pipeline.PauseError
	for br := range resultsCh {
		collected[br.index] = br.result
		overrides[br.index] = br.childOverride
		childUsages[br.index] = br.childUsage
		if pauseErr == nil && br.pauseErr != nil {
			pauseErr = br.pauseErr // first branch pause wins; propagated as a resumable halt
		}
	}
	return collected, overrides, childUsages, pauseErr
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
func (h *ParallelHandler) runBranch(ctx context.Context, idx int, tn *pipeline.Node, snapshot map[string]string, artifactDir string, sem chan struct{}, branchTimeout time.Duration, resultsCh chan<- branchResultMsg, wg *sync.WaitGroup, pctx *pipeline.PipelineContext) {
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

	defer h.recoverBranch(idx, tn, resultsCh, pctx)

	h.eventHandler.HandlePipelineEvent(stampRunID(pipeline.PipelineEvent{
		Type: pipeline.EventStageStarted, Timestamp: time.Now(), NodeID: tn.ID,
		NodeKind: tn.Handler, AttemptNo: 1, Message: fmt.Sprintf("parallel branch %q started", tn.ID),
	}, pctx))

	branchCtx := pipeline.NewPipelineContextFrom(snapshot)
	if artifactDir != "" {
		branchCtx.SetInternal(pipeline.InternalKeyArtifactDir, artifactDir)
	}
	// #420: expose the branch identity to the branch target (and any subgraph
	// it fans out to, which inherits this context snapshot) so tool/agent nodes
	// can namespace their on-disk per-loop counters by branch — two concurrent
	// milestone fix-loops then write independent, non-clobbering state instead
	// of racing over one shared path. The target node ID is a stable,
	// author-controlled identifier, unique across parallel_targets, and safe as
	// a filesystem path segment. Set before Execute so it lands in the snapshot
	// a subgraph branch seeds its child engine from.
	branchCtx.Set(pipeline.ContextKeyBranchID, tn.ID)
	// Snapshot()/NewPipelineContextFrom copy only the values namespace, not
	// internal keys, so propagate the run id onto branchCtx — otherwise the
	// branch TARGET handler stamps its events with an empty run_id (unattributable
	// in a shared/multiplexed event sink, and dropped by a lazily-opened JSONL log).
	if runID, ok := pctx.GetInternal(pipeline.InternalKeyRunID); ok {
		branchCtx.SetInternal(pipeline.InternalKeyRunID, runID)
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
	h.emitBranchComplete(tn.ID, pr, pctx)
	// Capture a recoverable pause (billing/quota exhaustion, #487) so the parent
	// can propagate it instead of masking it as a flat branch fail (or, under the
	// "any" policy, as node success).
	var pe *pipeline.PauseError
	errors.As(err, &pe)
	// Carry the branch's ChildOverride up to the parent aggregation site (not
	// onto ParallelResult, which is JSON-serialized into the parallel.results
	// audit value). Empty/nil propagates as nil — the aggregator unions
	// per-branch slices and drops empties.
	resultsCh <- branchResultMsg{index: idx, result: pr, childOverride: outcome.ChildOverride, childUsage: outcome.ChildUsage, pauseErr: pe}
}

// buildBranchResult assembles a ParallelResult from the branch execution outcome.
func buildBranchResult(nodeID string, outcome pipeline.Outcome, mergedUpdates map[string]string, err error) ParallelResult {
	pr := ParallelResult{NodeID: nodeID, Status: string(outcome.Status), ContextUpdates: mergedUpdates, Stats: outcome.Stats}
	if err != nil {
		pr.Status = string(pipeline.OutcomeFail)
		pr.Error = err.Error()
	}
	return pr
}

// recoverBranch is a deferred panic handler for parallel branch goroutines.
func (h *ParallelHandler) recoverBranch(idx int, tn *pipeline.Node, resultsCh chan<- branchResultMsg, pctx *pipeline.PipelineContext) {
	if r := recover(); r != nil {
		resultsCh <- branchResultMsg{
			index:  idx,
			result: ParallelResult{NodeID: tn.ID, Status: string(pipeline.OutcomeFail), Error: fmt.Sprintf("panic in parallel branch %q: %v", tn.ID, r)},
		}
		h.eventHandler.HandlePipelineEvent(stampRunID(pipeline.PipelineEvent{
			Type: pipeline.EventStageFailed, Timestamp: time.Now(), NodeID: tn.ID,
			Message: fmt.Sprintf("panic in branch %q: %v", tn.ID, r),
		}, pctx))
	}
}

// emitBranchComplete emits the appropriate pipeline event for a branch result.
func (h *ParallelHandler) emitBranchComplete(nodeID string, pr ParallelResult, pctx *pipeline.PipelineContext) {
	if pr.Status == string(pipeline.OutcomeFail) {
		h.eventHandler.HandlePipelineEvent(stampRunID(pipeline.PipelineEvent{
			Type: pipeline.EventStageFailed, Timestamp: time.Now(), NodeID: nodeID, Message: pr.Error,
		}, pctx))
	} else {
		h.eventHandler.HandlePipelineEvent(stampRunID(pipeline.PipelineEvent{
			Type: pipeline.EventStageCompleted, Timestamp: time.Now(), NodeID: nodeID,
		}, pctx))
	}
}

// aggregateStatus evaluates the branch results against the fan-in policy
// (default "any": success if at least one branch succeeded) and returns the
// aggregate status plus a human-readable policy detail string.
func aggregateStatus(results []ParallelResult, policy fanInPolicy) (pipeline.TerminalStatus, string) {
	successes, failed := tallyBranches(results)
	status := pipeline.OutcomeFail
	if policy.satisfied(successes, len(results)) {
		status = pipeline.OutcomeSuccess
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

// groupBranchOverridesByTarget converts indexed branch attrs to a target-keyed
// map. Branch indices are visited in ascending order so that when multiple
// branches name the same target, the highest index deterministically wins
// (dippin's "last value wins" convention for duplicate keys) — map-order
// iteration would pick a random branch, which matters for security overrides
// like tool_access / writable_paths (#368 review).
func groupBranchOverridesByTarget(indexed map[int]map[string]string) map[string]map[string]string {
	byTarget := make(map[string]map[string]string)
	for _, idx := range slices.Sorted(maps.Keys(indexed)) {
		branchAttrs := indexed[idx]
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
