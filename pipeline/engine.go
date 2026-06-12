// ABOUTME: Core pipeline execution engine that traverses graphs, executes handlers, and manages control flow.
// ABOUTME: Supports edge selection (conditions, labels, weights), retries, goal gates, and checkpoint resume.
package pipeline

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// EngineResult holds the final outcome of a pipeline execution run.
type EngineResult struct {
	RunID           string
	Status          TerminalStatus
	CompletedNodes  []string
	Context         map[string]string
	Trace           *Trace
	Usage           *UsageSummary
	BudgetLimitsHit []string // populated when a BudgetGuard halted the run
	// ValidationOverrides is the list of override edges traversed during this
	// run, in chronological order. Populated for every terminal path (success,
	// fail, budget, validation_overridden) so failure-after-override forensics
	// still see the override. Empty for runs with no override edges.
	//
	// The terminal-status rule writes Status=OutcomeValidationOverridden when
	// len(ValidationOverrides) > 0 AND the run reached the success exit;
	// failure paths return fail/budget regardless of override presence.
	ValidationOverrides []OverrideDetail
}

// OutcomeBudgetExceeded signals that a BudgetGuard halted the run.
const OutcomeBudgetExceeded TerminalStatus = "budget_exceeded"

// OutcomeValidationOverridden signals that the run reached the success exit
// after traversing at least one Edge.Override == true edge. Engine-terminal-only:
// handlers never return this value; the engine writes it post-loop based on the
// runState.validationOverrides slice. See docs/superpowers/specs/2026-05-29-validation-overridden-design.md.
const OutcomeValidationOverridden TerminalStatus = "validation_overridden"

// ChildRunContext is the execution context a handler may need when it
// launches a child run (subgraph, manager_loop). Carries the parent
// engine's BudgetGuard and a snapshot of usage already consumed so the
// child can enforce limits combined with the parent's running total.
// Retrieved via ChildRunContextFromContext.
type ChildRunContext struct {
	// BudgetGuard is the parent engine's budget guard. Child runs should
	// pass it via WithBudgetGuard so the same limits enforce within the
	// child. Nil when the parent has no budget configured.
	BudgetGuard *BudgetGuard

	// Baseline is an immutable snapshot of the parent's aggregated usage
	// at the moment the child was launched. Child runs should pass it via
	// WithBaselineUsage so the child's budget check folds baseline + its
	// own trace aggregate before comparing to limits. Without this, a
	// nested budget check would only see child-local spend and the
	// effective ceiling inside a subgraph would grow by the parent's
	// already-consumed amount.
	Baseline *UsageSummary
}

// childRunContextKey is the unexported ctx.Value key for ChildRunContext.
type childRunContextKey struct{}

// ChildRunContextFromContext returns the ChildRunContext stashed on ctx by
// the engine before dispatching a handler, or nil when no such value
// exists (top-level contexts outside a running engine).
func ChildRunContextFromContext(ctx context.Context) *ChildRunContext {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(childRunContextKey{}).(*ChildRunContext)
	return v
}

// Engine executes a pipeline graph by traversing nodes, dispatching handlers,
// selecting edges, and managing retries and checkpoints.
type Engine struct {
	graph             *Graph
	registry          *HandlerRegistry
	eventHandler      PipelineEventHandler
	checkpointPath    string
	resolveStylesheet bool
	initialContext    map[string]string
	artifactDir       string
	budgetGuard       *BudgetGuard
	baselineUsage     *UsageSummary // usage already consumed by a parent run; folded into budget checks
	gitArtifacts      bool
	steeringCh        <-chan map[string]string
	bundleIdentity    string // stamped on every emitted PipelineEvent; empty for non-bundle runs
}

// EngineOption configures optional Engine behavior.
type EngineOption func(*Engine)

// WithPipelineEventHandler sets the event handler for pipeline lifecycle events.
func WithPipelineEventHandler(h PipelineEventHandler) EngineOption {
	return func(e *Engine) {
		e.eventHandler = h
	}
}

// WithCheckpointPath enables checkpoint save/resume at the given file path.
func WithCheckpointPath(path string) EngineOption {
	return func(e *Engine) {
		e.checkpointPath = path
	}
}

// WithStylesheetResolution enables model stylesheet resolution on nodes before execution.
func WithStylesheetResolution(enabled bool) EngineOption {
	return func(e *Engine) {
		e.resolveStylesheet = enabled
	}
}

// WithArtifactDir sets the base directory for pipeline run artifacts.
// Node artifacts are written to <artifactDir>/<nodeID>/ instead of the working directory.
func WithArtifactDir(dir string) EngineOption {
	return func(e *Engine) {
		e.artifactDir = dir
	}
}

// WithInitialContext pre-populates the pipeline context with the given values.
// Used by subgraph execution to pass parent context into child pipelines.
func WithInitialContext(ctx map[string]string) EngineOption {
	return func(e *Engine) {
		e.initialContext = ctx
	}
}

// WithBudgetGuard attaches a BudgetGuard evaluated after every terminal
// node outcome. Nil guards are no-ops.
func WithBudgetGuard(guard *BudgetGuard) EngineOption {
	return func(e *Engine) { e.budgetGuard = guard }
}

// WithBaselineUsage pre-loads the engine's BudgetGuard with usage already
// consumed by a parent run. Used by subgraph execution so the child's guard
// check sees parent spend + child trace combined, preventing the "subgraph
// sandbox" escape where an operator's --max-tokens / --max-cost ceiling
// would otherwise be silently non-binding for nodes nested in a subgraph.
// Nil baselines are no-ops.
func WithBaselineUsage(baseline *UsageSummary) EngineOption {
	return func(e *Engine) { e.baselineUsage = baseline }
}

// WithGitArtifacts enables git-backed artifact tracking. When enabled, the
// artifact dir is initialized as a git repo at run start, and each terminal
// node outcome produces one commit capturing the artifact state at that
// point. Checkpoint saves made via saveCheckpointWithTag (not all
// saveCheckpoint call sites) also create a lightweight git tag of the form
// checkpoint/<runID>/<nodeID> pointing at the most recent node-outcome commit,
// intended as the basis for future checkpoint-replay support (Layer 2 of
// issue #77 — not wired up by this option).
//
// Requires git in PATH. Silently no-ops if artifactDir is not set.
func WithGitArtifacts(enabled bool) EngineOption {
	return func(e *Engine) { e.gitArtifacts = enabled }
}

// WithSteeringChan provides a channel for injecting context updates into the
// pipeline between node executions. The engine drains pending updates after
// each node's outcome is applied, making steered values visible to edge
// selection and the next node's prompt expansion. Nil channels are no-ops.
func WithSteeringChan(ch <-chan map[string]string) EngineOption {
	return func(e *Engine) { e.steeringCh = ch }
}

// WithBundleIdentity stamps every PipelineEvent the engine emits with the
// given content-addressed identity string (typically "sha256:<hex>"). Used
// to thread .dipx bundle identity into the activity log so every line of
// activity.jsonl carries provenance. Empty string (the default) is a no-op
// and matches the behavior for plain .dip runs.
func WithBundleIdentity(id string) EngineOption {
	return func(e *Engine) { e.bundleIdentity = id }
}

// NewEngine creates a pipeline engine for the given graph and handler registry.
func NewEngine(graph *Graph, registry *HandlerRegistry, opts ...EngineOption) *Engine {
	e := &Engine{
		graph:        graph,
		registry:     registry,
		eventHandler: PipelineNoopHandler,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Graph returns the graph this engine executes. Used by library callers
// that need to inspect graph attributes after construction.
func (e *Engine) Graph() *Graph { return e.graph }

// loopAction tells the main loop what to do after processing a node.
type loopAction int

const (
	loopContinue loopAction = iota // continue to next iteration with updated currentNodeID
	loopBreak                      // pipeline completed successfully
	loopReturn                     // return the result/error immediately
)

// loopResult holds the outcome of a single loop iteration.
type loopResult struct {
	action     loopAction
	nextNodeID string
	result     *EngineResult
	err        error
}

// Run executes the pipeline to completion or failure.
func (e *Engine) Run(ctx context.Context) (*EngineResult, error) {
	s, err := e.initRunState(ctx)
	if err != nil {
		return nil, err
	}

	e.emit(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     s.runID,
		Message:   "pipeline started",
	})

	currentNodeID := e.graph.StartNode
	if s.cp.CurrentNode != "" {
		currentNodeID = s.cp.CurrentNode
	}

	resumeVisited := make(map[string]bool)

	for {
		if err := ctx.Err(); err != nil {
			return e.cancelledResult(s, err)
		}

		lr := e.processNode(ctx, s, currentNodeID, resumeVisited)
		switch lr.action {
		case loopReturn:
			return lr.result, lr.err
		case loopBreak:
			goto done
		case loopContinue:
			currentNodeID = lr.nextNodeID
			continue
		}
	}

done:
	s.trace.EndTime = time.Now()

	e.emit(PipelineEvent{
		Type:      EventPipelineCompleted,
		Timestamp: time.Now(),
		RunID:     s.runID,
		Message:   "pipeline completed",
	})

	// Terminal-status rule: a success exit becomes validation_overridden if
	// any override fired during the run. Failure paths return fail/budget
	// regardless of override presence; only this success path flips.
	status := OutcomeSuccess
	if len(s.validationOverrides) > 0 {
		status = OutcomeValidationOverridden
	}
	return &EngineResult{
		RunID:               s.runID,
		Status:              status,
		CompletedNodes:      s.cp.CompletedNodes,
		Context:             s.pctx.Snapshot(),
		Trace:               s.trace,
		Usage:               s.trace.AggregateUsage(),
		ValidationOverrides: append([]OverrideDetail(nil), s.validationOverrides...),
	}, nil
}

// processNode handles a single iteration of the main engine loop.
func (e *Engine) processNode(ctx context.Context, s *runState, currentNodeID string, resumeVisited map[string]bool) loopResult {
	if _, ok := e.graph.Nodes[currentNodeID]; !ok {
		return loopResult{action: loopReturn, err: fmt.Errorf("node %q not found in graph", currentNodeID)}
	}

	if s.cp.IsCompleted(currentNodeID) {
		return e.processResumeSkip(s, currentNodeID, resumeVisited)
	}

	return e.processActiveNode(ctx, s, currentNodeID)
}

// processResumeSkip handles nodes that were already completed during checkpoint resume.
func (e *Engine) processResumeSkip(s *runState, currentNodeID string, resumeVisited map[string]bool) loopResult {
	nextID, done, err := e.resumeSkipNode(s, currentNodeID, resumeVisited)
	if err != nil {
		return loopResult{action: loopReturn, err: err}
	}
	if done {
		return loopResult{action: loopBreak}
	}
	return loopResult{action: loopContinue, nextNodeID: nextID}
}

// processActiveNode executes a node that has not been completed yet.
func (e *Engine) processActiveNode(ctx context.Context, s *runState, currentNodeID string) loopResult {
	node := e.graph.Nodes[currentNodeID]

	s.pctx.Set(ContextKeyOutcome, "")
	s.pctx.Set(ContextKeyPreferredLabel, "")
	s.pctx.Set(ContextKeySuggestedNextNodes, "")

	// #352 item 2: human_response is one-shot. Record the value visible to
	// this node so it can be cleared after the first prompt-consuming node
	// completes (see clearConsumedHumanResponse below).
	preHumanResponse, _ := s.pctx.Get(ContextKeyHumanResponse)

	execNode := e.prepareExecNode(node, s)

	outcome, traceEntry, err := e.executeNode(ctx, s, currentNodeID, execNode)
	if err != nil {
		// Preserve any dirty (possibly green) tree before this terminal handler
		// error halts the run (#302) — e.g. a tool/agent that wrote files then
		// died, or a cancellation mid-write. No-op on a clean tree. executeNode
		// already appended this node's trace entry, so mirror the recorded ref
		// onto it after the helper sets it on our local copy.
		e.commitWIPBeforeRouting(s, currentNodeID, &traceEntry)
		if traceEntry.WIPRef != "" && len(s.trace.Entries) > 0 {
			s.trace.Entries[len(s.trace.Entries)-1].WIPRef = traceEntry.WIPRef
		}
		// Scope any keys written before the error so checkpoints and downstream
		// nodes can still access this node's partial output via the scoped namespace.
		s.pctx.ScopeToNode(currentNodeID)
		e.saveCheckpoint(s.cp, s.pctx, s.runID)
		s.trace.EndTime = time.Now()
		return loopResult{
			action: loopReturn,
			result: &EngineResult{
				RunID:               s.runID,
				Status:              OutcomeFail,
				CompletedNodes:      s.cp.CompletedNodes,
				Context:             s.pctx.Snapshot(),
				Trace:               s.trace,
				Usage:               s.trace.AggregateUsage(),
				ValidationOverrides: append([]OverrideDetail(nil), s.validationOverrides...),
			},
			err: fmt.Errorf("handler error at node %q: %w", currentNodeID, err),
		}
	}

	e.applyOutcome(s, currentNodeID, outcome)

	// Copy every key written during this node's execution into the per-node
	// namespace "node.<nodeID>.<key>" so downstream nodes can read a specific
	// upstream node's output without collision. Bare keys keep their global
	// last-writer-wins value for backward compatibility.
	//
	// Scoping runs before drainSteering so that externally-injected steering
	// values are not misattributed to this node's scoped namespace.
	s.pctx.ScopeToNode(currentNodeID)

	// Drain any pending steering updates injected by an external supervisor
	// (e.g., manager_loop handler). Merged values become visible to edge
	// selection and the next node's prompt expansion. Steering uses
	// MergeWithoutDirty so the updates stay in the bare/global namespace and
	// never flow into any node's per-node scope.
	e.drainSteering(s)

	if outcome.Status == string(OutcomeRetry) {
		return e.processRetryOutcome(ctx, s, currentNodeID, execNode, &traceEntry)
	}

	// After the retry check: a retrying node re-executes and must still see
	// the response; a node that completed (success OR fail) has consumed it.
	e.clearConsumedHumanResponse(s, node, preHumanResponse)

	e.handleOutcomeStatus(s, currentNodeID, outcome.Status)

	if currentNodeID == e.graph.ExitNode {
		return e.processExitNode(s, currentNodeID, outcome.Status, &traceEntry)
	}

	return e.advanceToNextNode(s, currentNodeID, &traceEntry)
}

// consumesHumanResponse reports whether executing the node feeds pipeline
// context into an LLM prompt: codergen does (via ResolvePrompt), and a
// parallel node does when at least one of its branch targets is a codergen
// node (the parallel handler resolves each branch's prompt the same way; a
// tool-only fan-out resolves no prompts). subgraph and stack.manager_loop run
// nested engines with their own contexts and are not consumers here.
func (e *Engine) consumesHumanResponse(node *Node) bool {
	switch node.Handler {
	case "codergen":
		return true
	case "parallel":
		for _, id := range e.parallelBranchTargets(node) {
			if t, ok := e.graph.Nodes[id]; ok && t.Handler == "codergen" {
				return true
			}
		}
	}
	return false
}

// parallelBranchTargets mirrors the parallel handler's branch resolution
// (collectBranchEdges): the comma-separated parallel_targets attr when set,
// otherwise the node's outgoing edge targets. The outgoing-edge fallback
// includes the join node — harmless here, since a join is parallel.fan_in,
// never codergen.
func (e *Engine) parallelBranchTargets(node *Node) []string {
	if attr := node.ParallelConfig().ParallelTargets; attr != "" {
		var targets []string
		for _, t := range strings.Split(attr, ",") {
			if t = strings.TrimSpace(t); t != "" {
				targets = append(targets, t)
			}
		}
		return targets
	}
	var targets []string
	for _, edge := range e.graph.OutgoingEdges(node.ID) {
		targets = append(targets, edge.To)
	}
	return targets
}

// clearConsumedHumanResponse makes human_response one-shot (#352 item 2): the
// first prompt-consuming node to complete with a non-empty response clears the
// bare key so later nodes don't replay a stale human sign-off indefinitely.
// The gate's scoped copy (node.<gateID>.human_response) keeps the full value
// for explicit reference. The clear is an empty-set rather than a delete:
// PipelineContext has no delete, both injection paths skip empty values, and
// an empty value round-trips through checkpoint snapshots so a resumed run
// cannot resurrect the stale response. MergeWithoutDirty keeps the clear out
// of the next ScopeToNode call — no node "wrote" it.
func (e *Engine) clearConsumedHumanResponse(s *runState, node *Node, preVal string) {
	if preVal == "" || !e.consumesHumanResponse(node) {
		return
	}
	// Don't clobber a fresh response the node itself wrote via ContextUpdates.
	if cur, _ := s.pctx.Get(ContextKeyHumanResponse); cur != preVal {
		return
	}
	s.pctx.MergeWithoutDirty(map[string]string{ContextKeyHumanResponse: ""})
}

// processRetryOutcome handles a retry outcome from a handler.
func (e *Engine) processRetryOutcome(ctx context.Context, s *runState, currentNodeID string, execNode *Node, traceEntry *TraceEntry) loopResult {
	nextID, cont, result, err := e.handleRetry(ctx, s, currentNodeID, execNode, traceEntry)
	if err != nil {
		return loopResult{action: loopReturn, result: result, err: err}
	}
	if result != nil {
		return loopResult{action: loopReturn, result: result}
	}
	if cont {
		return loopResult{action: loopContinue, nextNodeID: nextID}
	}
	return loopResult{action: loopContinue, nextNodeID: currentNodeID}
}

// processExitNode handles the pipeline exit node.
func (e *Engine) processExitNode(s *runState, currentNodeID string, outcomeStatus string, traceEntry *TraceEntry) loopResult {
	shouldBreak, target, result := e.handleExitNode(s, currentNodeID, outcomeStatus, traceEntry)
	if result != nil {
		return loopResult{action: loopReturn, result: result}
	}
	if shouldBreak {
		return loopResult{action: loopBreak}
	}
	return loopResult{action: loopContinue, nextNodeID: target}
}

// hasAnyConditionalEdge returns true if any outgoing edge has a condition.
// When a node has conditional edges, the pipeline author has intentionally
// designed routing for different outcomes. When all edges are unconditional,
// a failure outcome would blindly continue — which is almost always a bug.
func hasAnyConditionalEdge(edges []*Edge) bool {
	for _, edge := range edges {
		if edge.Condition != "" {
			return true
		}
	}
	return false
}

// advanceToNextNode selects the next edge and advances, handling loop-backs.
// If the node's outcome was "fail" and no edge explicitly handles failure
// (via a condition like "ctx.outcome = fail"), the pipeline fails rather
// than silently continuing through an unconditional edge.
func (e *Engine) advanceToNextNode(s *runState, currentNodeID string, traceEntry *TraceEntry) loopResult {
	edges := e.graph.OutgoingEdges(currentNodeID)
	if len(edges) == 0 {
		s.trace.AddEntry(*traceEntry)
		return loopResult{action: loopReturn, err: fmt.Errorf("no outgoing edges from non-exit node %q", currentNodeID)}
	}

	if lr := e.checkStrictFailure(s, currentNodeID, traceEntry, edges); lr != nil {
		return *lr
	}

	next, err := e.selectEdge(s.runID, edges, s.pctx)
	if err != nil {
		s.trace.AddEntry(*traceEntry)
		return loopResult{action: loopReturn, err: fmt.Errorf("select edge from %q: %w", currentNodeID, err)}
	}

	// Override edge flip-point: if the selected edge has Override:true, append
	// a new OverrideDetail to the sticky list and persist synchronously.
	// Placed between selectEdge and SetEdgeSelection so the durable record is
	// written before any further state mutation that could be rolled back by
	// a crash. Idempotency: re-traversal of the same gate+label during
	// restart or goal-gate retry is a no-op.
	e.recordOverrideIfPresent(s, currentNodeID, next)

	traceEntry.EdgeTo = next.To
	s.trace.AddEntry(*traceEntry)
	e.emitGitCommit(s, currentNodeID, traceEntry)
	e.emitCostUpdate(s)
	if lr := e.checkBudgetAfterEmit(s); lr != nil {
		return *lr
	}
	s.cp.SetEdgeSelection(currentNodeID, next.To)

	if s.cp.IsCompleted(next.To) {
		return e.handleCompletedTarget(s, next.To, traceEntry)
	}

	s.cp.CurrentNode = next.To
	e.saveCheckpointWithTag(s.cp, s.pctx, s.runID, s, currentNodeID)
	return loopResult{action: loopContinue, nextNodeID: next.To}
}

// recordOverrideIfPresent is the flip-point that fires when the engine
// traverses an Edge.Override-marked edge. It builds an OverrideDetail from
// the current node + edge label + last outcome's OverrideActor, appends to
// the sticky list (both in-memory and checkpoint), emits
// EventValidationOverridden, and synchronously persists the checkpoint so a
// kill -9 between this point and the next selectEdge does not lose the
// override-fired state.
//
// Idempotency: own-graph entries are deduped by (gate node, label). Child-
// propagated entries (with non-empty SubgraphPath) are appended separately
// by the subgraph/manager_loop handlers and can never collide here.
func (e *Engine) recordOverrideIfPresent(s *runState, currentNodeID string, next *Edge) {
	if next == nil || !next.Override {
		return
	}
	if overrideAlreadyRecorded(s.validationOverrides, currentNodeID, next.Label) {
		return
	}
	actor := s.lastOutcome.OverrideActor
	if actor == "" {
		actor = ActorUnknown
	}
	detail := OverrideDetail{
		GateNodeID: currentNodeID,
		Label:      next.Label,
		Actor:      actor,
		Timestamp:  time.Now(),
	}
	s.appendOverride(detail)
	e.emit(PipelineEvent{
		Type:      EventValidationOverridden,
		Timestamp: detail.Timestamp,
		RunID:     s.runID,
		NodeID:    currentNodeID,
		Message:   fmt.Sprintf("validation override at %q via label %q (actor=%s)", currentNodeID, next.Label, detail.Actor),
		Override:  &detail,
	})
	// Synchronously persist so a kill -9 between this point and the next
	// selectEdge does not lose the override-fired state.
	e.saveCheckpointWithTag(s.cp, s.pctx, s.runID, s, currentNodeID)
}

// overrideAlreadyRecorded returns true if the sticky list already contains an
// own-graph override entry with the same gate node and label. Used by the
// flip-point for the restart re-traversal idempotency check.
//
// Trade-off (#273 review, Copilot): the predicate is keyed on (gateNodeID,
// label) only — it has no concept of "this is restart resume" vs "this is
// a fresh loop iteration." A workflow that legitimately loops back to the
// same gate and accepts the same label twice in a single run will record
// only the first acceptance; the second is silently deduped. In practice
// override-shape gates appear once per validation cycle and labels rotate
// per cycle, but operators who need per-traversal recording should use
// distinct labels per iteration (e.g., "accept attempt 1" / "accept
// attempt 2") until the dedup is rescoped to checkpoint generations.
// Tracking issue: #279.
//
// Note: only checks entries with empty SubgraphPath; child-propagated entries
// can never collide with own-graph entries.
func overrideAlreadyRecorded(list []OverrideDetail, gateNodeID, label string) bool {
	for _, d := range list {
		if len(d.SubgraphPath) == 0 && d.GateNodeID == gateNodeID && d.Label == label {
			return true
		}
	}
	return false
}

// checkStrictFailure enforces strict failure mode: a failed node with only
// unconditional outgoing edges stops the pipeline.
func (e *Engine) checkStrictFailure(s *runState, nodeID string, traceEntry *TraceEntry, edges []*Edge) *loopResult {
	outcome, _ := s.pctx.Get(ContextKeyOutcome)
	if outcome != string(OutcomeFail) || hasAnyConditionalEdge(edges) {
		return nil
	}
	// Preserve any dirty (possibly green) working tree to a recoverable ref
	// BEFORE deciding to halt or route to a fallback, so green-but-uncommitted
	// work is never discarded by the routing decision (#302). No-op on a clean
	// tree; warns and skips when no git adapter is configured.
	e.commitWIPBeforeRouting(s, nodeID, traceEntry)
	// Before dead-stopping, consult the node/graph-level fallback_target so an
	// unhandled failure (incl. turn-exhaustion) escalates to a safety node
	// instead of skipping every downstream node (#295). One-shot per node.
	if node := e.graph.Nodes[nodeID]; node != nil {
		if lr := e.strictFailureFallback(s, node, traceEntry); lr != nil {
			return lr
		}
	}
	e.emit(PipelineEvent{
		Type:      EventStageFailed,
		Timestamp: time.Now(),
		NodeID:    nodeID,
		Message:   fmt.Sprintf("node %q failed with no failure edge — stopping pipeline", nodeID),
	})
	s.trace.AddEntry(*traceEntry)
	s.trace.EndTime = time.Now()
	lr := loopResult{
		action: loopReturn,
		result: &EngineResult{
			RunID:               s.runID,
			Status:              OutcomeFail,
			CompletedNodes:      s.cp.CompletedNodes,
			Context:             s.pctx.Snapshot(),
			Trace:               s.trace,
			Usage:               s.trace.AggregateUsage(),
			ValidationOverrides: append([]OverrideDetail(nil), s.validationOverrides...),
		},
		err: fmt.Errorf("node %q failed with no conditional edges to handle failure", nodeID),
	}
	return &lr
}

// strictFailureFallback attempts to route an unhandled strict failure to a
// node- or graph-level fallback_target instead of halting. It mirrors
// goalGateExhaustedPath (engine_checkpoint.go): the fallback is taken at most
// once per node per run, guarded by cp.FallbackTaken (persisted in the
// checkpoint) to prevent loop-backs from re-escalating forever. Returns an
// advancing loopResult when a fallback resolves, or nil to let the caller
// perform today's terminal halt.
func (e *Engine) strictFailureFallback(s *runState, node *Node, traceEntry *TraceEntry) *loopResult {
	if s.cp.FallbackTaken[node.ID] {
		return nil
	}
	fb := e.findFallbackTarget(node)
	if fb == "" {
		return nil
	}
	traceEntry.EdgeTo = fb
	s.trace.AddEntry(*traceEntry)
	// Apply the same post-node budget check as advanceToNextNode before
	// advancing, so a node that already breached a hard ceiling halts the run
	// rather than spending more on the fallback node (#311 review).
	e.emitCostUpdate(s)
	if lr := e.checkBudgetAfterEmit(s); lr != nil {
		return lr
	}
	if s.cp.FallbackTaken == nil {
		s.cp.FallbackTaken = map[string]bool{}
	}
	s.cp.FallbackTaken[node.ID] = true
	e.emit(PipelineEvent{
		Type:      EventStageFailed,
		Timestamp: time.Now(),
		RunID:     s.runID,
		NodeID:    node.ID,
		Message:   fmt.Sprintf("node %q failed with no failure edge, routing to fallback %q", node.ID, fb),
	})
	e.clearDownstream(fb, s.cp)
	s.cp.CurrentNode = fb
	e.saveCheckpointWithTag(s.cp, s.pctx, s.runID, s, node.ID)
	return &loopResult{action: loopContinue, nextNodeID: fb}
}

// handleCompletedTarget handles the case where the selected next node was already completed.
func (e *Engine) handleCompletedTarget(s *runState, nextTo string, traceEntry *TraceEntry) loopResult {
	nextID, cont, result, err := e.handleLoopRestart(s, nextTo, traceEntry)
	if err != nil {
		return loopResult{action: loopReturn, result: result, err: err}
	}
	if result != nil {
		return loopResult{action: loopReturn, result: result}
	}
	if cont {
		return loopResult{action: loopContinue, nextNodeID: nextID}
	}
	s.cp.CurrentNode = nextTo
	e.saveCheckpoint(s.cp, s.pctx, s.runID)
	return loopResult{action: loopContinue, nextNodeID: nextTo}
}

// cancelledResult builds the result when the context is cancelled.
func (e *Engine) cancelledResult(s *runState, err error) (*EngineResult, error) {
	e.saveCheckpoint(s.cp, s.pctx, s.runID)
	s.trace.EndTime = time.Now()
	e.emit(PipelineEvent{
		Type:      EventPipelineFailed,
		Timestamp: time.Now(),
		RunID:     s.runID,
		Message:   "context cancelled",
		Err:       err,
	})
	return &EngineResult{
		RunID:               s.runID,
		Status:              OutcomeFail,
		CompletedNodes:      s.cp.CompletedNodes,
		Context:             s.pctx.Snapshot(),
		Trace:               s.trace,
		Usage:               s.trace.AggregateUsage(),
		ValidationOverrides: append([]OverrideDetail(nil), s.validationOverrides...),
	}, fmt.Errorf("pipeline cancelled: %w", err)
}

// emit sends a pipeline event to the configured handler. The configured
// bundle identity (via WithBundleIdentity) is stamped onto every event
// before forwarding, so downstream handlers (notably the JSONL activity
// log writer) see provenance on every line.
func (e *Engine) emit(evt PipelineEvent) {
	if evt.BundleIdentity == "" {
		evt.BundleIdentity = e.bundleIdentity
	}
	e.eventHandler.HandlePipelineEvent(evt)
}

// failResult builds an EngineResult with fail status. Populates
// ValidationOverrides from the run's sticky list so forensics see "this run
// had an override AND it failed."
func (e *Engine) failResult(s *runState) *EngineResult {
	e.emit(PipelineEvent{
		Type:      EventPipelineFailed,
		Timestamp: time.Now(),
		RunID:     s.runID,
		Message:   "pipeline failed",
	})
	return &EngineResult{
		RunID:               s.runID,
		Status:              OutcomeFail,
		CompletedNodes:      s.cp.CompletedNodes,
		Context:             s.pctx.Snapshot(),
		Trace:               s.trace,
		Usage:               s.trace.AggregateUsage(),
		ValidationOverrides: append([]OverrideDetail(nil), s.validationOverrides...),
	}
}

// unwrapPathError extracts the underlying error from wrapped checkpoint errors
// so that os.IsNotExist can detect file-not-found through fmt.Errorf wrapping.
func unwrapPathError(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr
	}
	return err
}

// generateRunID creates a random 6-byte hex run identifier.
func generateRunID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return fmt.Sprintf("%x", b)
}
