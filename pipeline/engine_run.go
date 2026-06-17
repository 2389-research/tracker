// ABOUTME: Extracted helper methods for Engine.Run to reduce cyclomatic/cognitive complexity.
// ABOUTME: Handles node preparation, execution, outcome processing, retries, restarts, and checkpoint resume.
package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// emitGitCommit records the node outcome as a git commit in the artifact repo.
// Best-effort: errors are emitted as warnings and do not stop the pipeline.
func (e *Engine) emitGitCommit(s *runState, nodeID string, traceEntry *TraceEntry) {
	if s.gitRepo == nil {
		return
	}
	handler := ""
	if traceEntry != nil {
		handler = traceEntry.HandlerName
	}
	status := ""
	if traceEntry != nil {
		status = traceEntry.Status
	}
	if err := s.gitRepo.CommitNode(nodeID, handler, status, traceEntry); err != nil {
		e.emit(PipelineEvent{
			Type:      EventWarning,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    nodeID,
			Message:   fmt.Sprintf("git commit failed for node %q: %v", nodeID, err),
		})
	}
}

// commitWIPBeforeRouting preserves a failed/exhausted node's uncommitted work
// to a recoverable git ref BEFORE the engine routes away from (or halts at) the
// node, so green-but-uncommitted work is never silently discarded (#302). It
// records the ref on the trace entry and durably in the checkpoint (cp.WIPRefs)
// so the work is retrievable after the run.
//
// It is a no-op when the working tree is clean (no empty commit, no ref). When
// no git artifact adapter is configured it emits an EventWarning and skips —
// it never reaches into the user's real working repo. A WIP-commit failure is
// surfaced as a warning and never masks the original node failure or changes
// the routing outcome (CLAUDE.md: never silently swallow errors).
func (e *Engine) commitWIPBeforeRouting(s *runState, nodeID string, traceEntry *TraceEntry) {
	if s.gitRepo == nil {
		// gitRepo is nil when git artifacts are disabled, when enabled but no
		// artifact dir was configured, or when repo init failed (initRunState
		// already warned about that case) — so don't claim a single cause.
		e.emit(PipelineEvent{
			Type:      EventWarning,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    nodeID,
			Message:   fmt.Sprintf("cannot preserve uncommitted work for failed node %q: git artifact repository unavailable (enable with --git-artifacts and an artifact dir, or see the earlier init-failure warning)", nodeID),
		})
		return
	}
	ref, err := s.gitRepo.CommitWIP(nodeID)
	if err != nil {
		e.emit(PipelineEvent{
			Type:      EventWarning,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    nodeID,
			Message:   fmt.Sprintf("WIP commit failed for node %q (work may be unpreserved): %v", nodeID, err),
		})
		return
	}
	if ref == "" {
		return // clean tree — nothing to preserve
	}
	s.cp.RecordWIPRef(nodeID, ref)
	if traceEntry != nil {
		traceEntry.WIPRef = ref
	}
	// Persist immediately so the recoverable ref survives even on terminal-halt
	// paths that do not otherwise save the checkpoint.
	e.saveCheckpoint(s.cp, s.pctx, s.runID)
	// Surface the recovery handle on the out-of-band warning channel so the
	// preserved work is discoverable at runtime, not just in the trace.
	e.emit(PipelineEvent{
		Type:      EventWarning,
		Timestamp: time.Now(),
		RunID:     s.runID,
		NodeID:    nodeID,
		Message:   fmt.Sprintf("preserved uncommitted work for failed node %q to recoverable ref %s", nodeID, ref),
	})
}

// saveCheckpointWithTag saves the checkpoint and creates a lightweight git tag
// checkpoint/<runID>/<nodeID> pointing at HEAD (the most recent node-outcome
// commit). Because checkpoint.json is in .gitignore it is never committed, so
// the tag deliberately points at the preceding node-outcome commit — which is
// exactly the state a checkpoint resume would replay from.
// The git tag is best-effort; errors are emitted as warnings.
func (e *Engine) saveCheckpointWithTag(cp *Checkpoint, pctx *PipelineContext, runID string, s *runState, nodeID string) {
	e.saveCheckpoint(cp, pctx, runID)
	if s.gitRepo == nil {
		return
	}
	if err := s.gitRepo.TagCheckpoint(nodeID); err != nil {
		e.emit(PipelineEvent{
			Type:      EventWarning,
			Timestamp: time.Now(),
			RunID:     runID,
			NodeID:    nodeID,
			Message:   fmt.Sprintf("git tag failed for checkpoint at node %q: %v", nodeID, err),
		})
	}
}

// emitCostUpdate emits an EventCostUpdated carrying the current aggregate
// usage. For child engines running under a parent (subgraph), this is the
// combined parent-baseline + child-trace snapshot that BudgetGuard also
// sees, so operator-facing cost events match the numbers that actually
// trigger budget halts. Safe to call when no LLM activity has occurred yet —
// combinedUsageForBudget returns nil and the event is suppressed.
func (e *Engine) emitCostUpdate(s *runState) {
	summary := e.combinedUsageForBudget(s)
	if summary == nil {
		return
	}
	e.emit(PipelineEvent{
		Type:      EventCostUpdated,
		Timestamp: time.Now(),
		RunID:     s.runID,
		Cost: &CostSnapshot{
			TotalTokens:    summary.TotalTokens,
			TotalCostUSD:   summary.TotalCostUSD,
			ProviderTotals: summary.ProviderTotals,
			WallElapsed:    time.Since(s.trace.StartTime),
			Estimated:      summary.Estimated,
		},
	})
}

// haltForBudget produces the terminal loopResult emitted when a BudgetGuard
// trips. It saves the checkpoint (so restarts skip already-completed nodes),
// sets the trace end time, emits EventBudgetExceeded with the same combined
// usage snapshot the guard used to detect the breach (so diagnostics report
// the actual trigger value, not a child-local sub-total that sits below the
// ceiling), and packages an EngineResult with Status=OutcomeBudgetExceeded.
//
// EngineResult.Usage intentionally holds the child-local aggregate only,
// not the combined snapshot. The subgraph handler copies this onto
// Outcome.ChildUsage and the parent trace's AggregateUsage folds it back
// in; using the combined value here would double-count the parent's own
// spend once the parent aggregates a second time.
func (e *Engine) haltForBudget(s *runState, breach BudgetBreach) loopResult {
	e.saveCheckpoint(s.cp, s.pctx, s.runID)
	s.trace.EndTime = time.Now()
	combined := e.combinedUsageForBudget(s)
	var costSnap *CostSnapshot
	if combined != nil {
		costSnap = &CostSnapshot{
			TotalTokens:    combined.TotalTokens,
			TotalCostUSD:   combined.TotalCostUSD,
			ProviderTotals: combined.ProviderTotals,
			WallElapsed:    time.Since(s.trace.StartTime),
			Estimated:      combined.Estimated,
		}
	}
	e.emit(PipelineEvent{
		Type:      EventBudgetExceeded,
		Timestamp: time.Now(),
		RunID:     s.runID,
		Message:   breach.Message,
		Cost:      costSnap,
	})
	return loopResult{
		action: loopReturn,
		result: &EngineResult{
			RunID:               s.runID,
			Status:              OutcomeBudgetExceeded,
			CompletedNodes:      s.cp.CompletedNodes,
			Context:             s.pctx.Snapshot(),
			Trace:               s.trace,
			Usage:               s.trace.AggregateUsage(),
			BudgetLimitsHit:     []string{breach.Kind.String()},
			ValidationOverrides: append([]OverrideDetail(nil), s.validationOverrides...),
		},
	}
}

// checkBudgetAfterEmit runs the BudgetGuard against the current aggregate
// usage (combined with any baseline from a parent run). Returns a non-nil
// loopResult when a breach halts the run, or nil to continue.
func (e *Engine) checkBudgetAfterEmit(s *runState) *loopResult {
	breach := e.budgetGuard.Check(e.combinedUsageForBudget(s), s.trace.StartTime)
	if breach.Kind == BudgetOK {
		return nil
	}
	lr := e.haltForBudget(s, breach)
	return &lr
}

// combinedUsageForBudget returns the usage snapshot that BudgetGuard sees.
// Child engines run with a baseline loaded from the parent's trace so the
// guard enforces against combined parent+child spend. When no baseline is
// set (top-level runs, or subgraph runs without an attached guard), the
// local trace aggregate is returned unchanged.
func (e *Engine) combinedUsageForBudget(s *runState) *UsageSummary {
	local := s.trace.AggregateUsage()
	if e.baselineUsage == nil {
		return local
	}
	merged := cloneUsageSummary(e.baselineUsage)
	if local != nil {
		foldChildUsageIntoSummary(merged, local)
	}
	return merged
}

// cloneUsageSummary returns a deep-enough copy that mutations on the result
// do not affect the input. Used so combinedUsageForBudget can fold a trace
// aggregate into a baseline without corrupting the baseline on repeat calls.
func cloneUsageSummary(u *UsageSummary) *UsageSummary {
	if u == nil {
		return &UsageSummary{ProviderTotals: make(map[string]ProviderUsage)}
	}
	clone := *u
	clone.ProviderTotals = make(map[string]ProviderUsage, len(u.ProviderTotals))
	for k, v := range u.ProviderTotals {
		clone.ProviderTotals[k] = v
	}
	return &clone
}

// checkBudgetHaltForExit is a thin wrapper used by handleExitNode, which has
// a different return signature from advanceToNextNode.
func (e *Engine) checkBudgetHaltForExit(s *runState) *EngineResult {
	if lr := e.checkBudgetAfterEmit(s); lr != nil {
		return lr.result
	}
	return nil
}

// runState holds per-run mutable state threaded through the main loop.
type runState struct {
	runID        string
	pctx         *PipelineContext
	cp           *Checkpoint
	trace        *Trace
	nodeOutcomes map[string]string
	stylesheet   *Stylesheet
	gitRepo      *gitArtifactRepo // non-nil when git artifact tracking is enabled

	// validationOverrides is the per-run sticky list of override events
	// appended at the flip-point in advanceToNextNode. Mirrors
	// cp.ValidationOverrides; the runState copy is the in-memory hot-path read,
	// the cp copy is the durable record. Populated on every terminal-result
	// path (success, fail, budget) so forensics see overrides even when
	// failure dominates.
	validationOverrides []OverrideDetail

	// lastOutcome carries the most recent handler outcome through edge selection
	// so advanceToNextNode can read Outcome.OverrideActor when an override edge
	// is traversed. Set in applyOutcome before the engine advances.
	//
	// Stored as a shallow copy via `s.lastOutcome = *outcome`: value-type fields
	// (Status, OverrideActor, PreferredLabel) are safely snapshotted, but slice
	// and pointer fields (Truncations, ChildOverride, ChildUsage, MissingMarker, MissingStatus,
	// MissingRoute) share backing storage with the original outcome — treat them
	// as read-only here. Mutating those fields through s.lastOutcome would
	// silently corrupt the handler's outcome value (and vice versa).
	lastOutcome Outcome
}

// appendOverride appends an OverrideDetail to BOTH the in-memory hot-path
// slice (s.validationOverrides) and the checkpoint slice (s.cp.ValidationOverrides).
// They MUST stay in sync — the hot-path slice serves the engine's terminal-status
// rule and event-emission; the checkpoint slice is the durable record for resume
// and audit-log fallback. Any code path that records a new override must use
// this helper.
func (s *runState) appendOverride(d OverrideDetail) {
	s.validationOverrides = append(s.validationOverrides, d)
	s.cp.ValidationOverrides = append(s.cp.ValidationOverrides, d)
}

// initRunState initializes all per-run state: context, checkpoint, trace, and stylesheet.
func (e *Engine) initRunState(ctx context.Context) (*runState, error) {
	runID := generateRunID()

	if e.checkpointPath == "" && e.artifactDir != "" {
		e.checkpointPath = filepath.Join(e.artifactDir, runID, "checkpoint.json")
	}

	pctx := e.buildInitialContext()

	cp, runID, err := e.loadCheckpointAndMerge(runID, pctx)
	if err != nil {
		return nil, err
	}

	if e.artifactDir != "" {
		pctx.SetInternal(InternalKeyArtifactDir, filepath.Join(e.artifactDir, runID))
	}

	stylesheet, err := e.maybeParseStylesheet()
	if err != nil {
		return nil, err
	}

	// Clear the dirty set after all bootstrap writes (graph attrs, initial
	// context, checkpoint restore, compaction) so that baseline values are not
	// copied into the first node's scoped namespace when ScopeToNode is called.
	pctx.ClearDirty()

	// Initialize git artifact repo if requested and an artifact dir is set.
	var gitRepo *gitArtifactRepo
	if e.gitArtifacts && e.artifactDir != "" {
		repoDir := filepath.Join(e.artifactDir, runID)
		gitRepo = newGitArtifactRepo(repoDir, runID)
		if err := gitRepo.Init(); err != nil {
			// Best-effort: emit a warning and continue without git tracking.
			e.emit(PipelineEvent{
				Type:      EventWarning,
				Timestamp: time.Now(),
				RunID:     runID,
				Message:   fmt.Sprintf("git artifact init failed (continuing without git tracking): %v", err),
			})
			gitRepo = nil
		}
	}

	// Seed the in-memory sticky list from any prior checkpoint so a resumed
	// run preserves overrides that fired before the kill / SIGINT. The cp
	// copy is the durable record; the runState copy is the hot-path read.
	var stickyOverrides []OverrideDetail
	if len(cp.ValidationOverrides) > 0 {
		stickyOverrides = append(stickyOverrides, cp.ValidationOverrides...)
	}

	return &runState{
		runID:               runID,
		pctx:                pctx,
		cp:                  cp,
		trace:               &Trace{RunID: runID, StartTime: time.Now()},
		nodeOutcomes:        make(map[string]string),
		stylesheet:          stylesheet,
		gitRepo:             gitRepo,
		validationOverrides: stickyOverrides,
	}, nil
}

// buildInitialContext creates a PipelineContext seeded with graph and initial context values.
func (e *Engine) buildInitialContext() *PipelineContext {
	pctx := NewPipelineContext()
	for key, value := range e.graph.Attrs {
		pctx.Set("graph."+key, value)
	}
	for k, v := range e.initialContext {
		pctx.Set(k, v)
	}
	return pctx
}

// loadCheckpointAndMerge loads or creates a checkpoint, merges its context into pctx,
// and returns the checkpoint, resolved run ID, and any error.
func (e *Engine) loadCheckpointAndMerge(runID string, pctx *PipelineContext) (*Checkpoint, string, error) {
	cp, err := e.loadOrCreateCheckpoint(runID)
	if err != nil {
		return nil, "", fmt.Errorf("checkpoint load: %w", err)
	}
	if cp.RunID != "" {
		runID = cp.RunID
	}
	for k, v := range cp.Context {
		pctx.Set(k, v)
	}
	// Re-seed graph.* from the live graph.Attrs after checkpoint merge.
	// Workflow-level params (graph.params.*) and other graph attributes
	// are authoritative from the current graph, not whatever was captured
	// in a prior run's checkpoint — otherwise ${params.*} overrides
	// supplied on this invocation would silently regress to stale values.
	for key, value := range e.graph.Attrs {
		pctx.Set("graph."+key, value)
	}
	e.compactResumeContext(cp, pctx, runID)
	return cp, runID, nil
}

// maybeParseStylesheet parses the model stylesheet from graph attrs if enabled.
func (e *Engine) maybeParseStylesheet() (*Stylesheet, error) {
	if !e.resolveStylesheet {
		return nil, nil
	}
	ssRaw, ok := e.graph.Attrs["model_stylesheet"]
	if !ok {
		return nil, nil
	}
	ss, err := ParseStylesheet(ssRaw)
	if err != nil {
		return nil, fmt.Errorf("parse stylesheet: %w", err)
	}
	return ss, nil
}

// compactResumeContext applies fidelity-aware compaction when resuming from a checkpoint.
func (e *Engine) compactResumeContext(cp *Checkpoint, pctx *PipelineContext, runID string) {
	if cp.CurrentNode == "" || len(cp.CompletedNodes) == 0 {
		return
	}

	routingHints := captureRoutingHints(pctx)

	currentNode := e.nodeOrDefault(cp.CurrentNode)
	fidelity := ResolveFidelity(currentNode, e.graph.Attrs)
	degraded := DegradeFidelity(fidelity)
	compacted := CompactContextWithPinnedKeys(
		pctx,
		cp.CompletedNodes,
		degraded,
		e.artifactDir,
		runID,
		ParseDeclaredKeys(currentNode.Attrs["reads"]),
	)

	replaceContextValues(pctx, compacted)
	restoreRoutingHints(pctx, routingHints)
}

// captureRoutingHints saves the current routing hint values from context.
func captureRoutingHints(pctx *PipelineContext) map[string]string {
	hints := make(map[string]string)
	for _, key := range []string{ContextKeyOutcome, ContextKeyPreferredLabel, ContextKeySuggestedNextNodes} {
		if val, ok := pctx.Get(key); ok && val != "" {
			hints[key] = val
		}
	}
	return hints
}

// replaceContextValues clears the context and repopulates it with compacted values.
func replaceContextValues(pctx *PipelineContext, compacted map[string]string) {
	for k := range pctx.Snapshot() {
		pctx.Set(k, "")
	}
	for k, v := range compacted {
		pctx.Set(k, v)
	}
}

// restoreRoutingHints re-applies routing hints that were cleared during compaction.
func restoreRoutingHints(pctx *PipelineContext, hints map[string]string) {
	for k, v := range hints {
		if existing, ok := pctx.Get(k); !ok || existing == "" {
			pctx.Set(k, v)
		}
	}
}

// resumeSkipNode handles a node that was already completed during a checkpoint resume.
// Returns (nextNodeID, done, error) where done=true means pipeline is finished.
func (e *Engine) resumeSkipNode(s *runState, currentNodeID string, resumeVisited map[string]bool) (string, bool, error) {
	if resumeVisited[currentNodeID] {
		e.clearDownstream(currentNodeID, s.cp)
		e.clearDownstreamRetryCounts(currentNodeID, s.cp)
		return currentNodeID, false, nil
	}
	resumeVisited[currentNodeID] = true

	e.emit(PipelineEvent{
		Type:      EventStageCompleted,
		Timestamp: time.Now(),
		RunID:     s.runID,
		NodeID:    currentNodeID,
		Message:   "previously completed (resumed)",
	})

	edges := e.graph.OutgoingEdges(currentNodeID)
	if len(edges) == 0 {
		return "", true, nil
	}

	if storedTo, ok := s.cp.GetEdgeSelection(currentNodeID); ok {
		return storedTo, false, nil
	}

	next, err := e.selectEdge(s.runID, edges, s.pctx)
	if err != nil {
		return "", false, fmt.Errorf("select edge from completed node %q: %w", currentNodeID, err)
	}
	return next.To, false, nil
}

// prepareExecNode applies stylesheet and variable expansion, returning the node to execute.
func (e *Engine) prepareExecNode(node *Node, s *runState) *Node {
	execNode := node
	if s.stylesheet != nil {
		resolved := s.stylesheet.Resolve(node)
		execNode = &Node{
			ID:      node.ID,
			Shape:   node.Shape,
			Label:   node.Label,
			Handler: node.Handler,
			Attrs:   resolved,
		}
	}

	graphVars := GraphVarMap(s.pctx)
	execAttrs := make(map[string]string, len(execNode.Attrs))
	changed := false
	for k, v := range execNode.Attrs {
		expanded := ExpandGraphVariables(v, graphVars)
		if k == "prompt" {
			expanded = ExpandPromptVariables(expanded, s.pctx)
		}
		execAttrs[k] = expanded
		if expanded != v {
			changed = true
		}
	}
	if changed {
		execNode = &Node{
			ID:      execNode.ID,
			Shape:   execNode.Shape,
			Label:   execNode.Label,
			Handler: execNode.Handler,
			Attrs:   execAttrs,
		}
	}
	return execNode
}

// executeNode runs the handler for a node and records the outcome in the trace.
// Returns the outcome, trace entry, and any error.
func (e *Engine) executeNode(ctx context.Context, s *runState, currentNodeID string, execNode *Node) (*Outcome, TraceEntry, error) {
	e.emit(PipelineEvent{
		Type:      EventStageStarted,
		Timestamp: time.Now(),
		RunID:     s.runID,
		NodeID:    currentNodeID,
		Message:   fmt.Sprintf("executing node %q", currentNodeID),
	})

	handlerStart := time.Now()
	// Stash the engine's budget guard + current usage snapshot on ctx so
	// handlers that launch child runs (subgraph, manager_loop) can
	// propagate them to the child engine. Without this, the child's
	// BudgetGuard.Check runs only against child-local spend and the
	// operator's --max-tokens / --max-cost ceiling becomes an effective
	// ceiling *per nesting level*, not per run. See #183.
	//
	// Skip entirely when no guard is configured: there's nothing to
	// propagate, and computing combinedUsageForBudget on every handler
	// dispatch would burn clones/folds for no benefit.
	execCtx := ctx
	if e.budgetGuard != nil {
		execCtx = context.WithValue(ctx, childRunContextKey{}, &ChildRunContext{
			BudgetGuard: e.budgetGuard,
			Baseline:    e.combinedUsageForBudget(s),
		})
	}
	outcome, err := e.registry.Execute(execCtx, execNode, s.pctx)
	handlerDuration := time.Since(handlerStart)

	traceEntry := TraceEntry{
		Timestamp:   handlerStart,
		NodeID:      currentNodeID,
		HandlerName: execNode.Handler,
		Status:      "",
		Duration:    handlerDuration,
		Stats:       nil,
	}

	if err != nil {
		traceEntry.Status = "error"
		traceEntry.Error = err.Error()
		// Preserve ChildUsage even on handler error so that cancelled child
		// runs (e.g. manager_loop ctx-cancellation) still contribute their
		// accumulated spend to the parent trace's AggregateUsage and
		// BudgetGuard rollup. Without this, any handler that returns both a
		// non-nil ChildUsage and a non-nil error (e.g. cancellation path)
		// would silently drop the child's token/cost data from the parent.
		traceEntry.ChildUsage = outcome.ChildUsage
		s.trace.AddEntry(traceEntry)
		e.emit(PipelineEvent{
			Type:      EventStageFailed,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    currentNodeID,
			Message:   fmt.Sprintf("handler error at node %q", currentNodeID),
			Err:       err,
		})
		return nil, traceEntry, err
	}

	traceEntry.Status = outcome.Status
	traceEntry.Stats = outcome.Stats
	traceEntry.ChildUsage = outcome.ChildUsage

	// Surface tool-output truncation as structured events so `tracker
	// diagnose`, the TUI activity log, and NDJSON consumers can correlate
	// routing misses with dropped output (issue #208). One event per
	// truncated stream — stdout and stderr can both fire if both
	// overflowed the per-stream cap.
	for i := range outcome.Truncations {
		td := &outcome.Truncations[i]
		e.emit(PipelineEvent{
			Type:       EventToolOutputTruncated,
			Timestamp:  time.Now(),
			RunID:      s.runID,
			NodeID:     currentNodeID,
			Message:    fmt.Sprintf("tool node %q: %s truncated — captured last %d bytes, dropped %d bytes from head (limit %d)", currentNodeID, td.Stream, td.CapturedBytes, td.DroppedBytes, td.Limit),
			Truncation: td,
		})
	}

	// Surface marker_grep no-match as a typed audit event so `tracker
	// diagnose` can call out exactly why a node failed (issue #210).
	// The tool handler already set Status = OutcomeFail; this is the
	// audit-trail companion. Emit before returning so the event ordering
	// matches the rest of the per-node emissions. The message branches
	// on MissingMarker.Error: a populated Error means the regex itself
	// was invalid (author error), an empty Error means the regex was
	// fine but matched nothing in the captured stdout.
	if outcome.MissingMarker != nil {
		var msg string
		if outcome.MissingMarker.Error != "" {
			msg = fmt.Sprintf("tool node %q: marker_grep regex %q failed to compile: %s — failing node to avoid silent fallback",
				currentNodeID, outcome.MissingMarker.Pattern, outcome.MissingMarker.Error)
		} else {
			msg = fmt.Sprintf("tool node %q: marker_grep %q matched nothing in captured stdout — failing node to avoid silent fallback",
				currentNodeID, outcome.MissingMarker.Pattern)
		}
		e.emit(PipelineEvent{
			Type:      EventToolMarkerMissing,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    currentNodeID,
			Message:   msg,
			Marker:    outcome.MissingMarker,
		})
	}

	// Same shape as the MissingMarker emission above, different
	// mechanism: route_required: true was set on the node but no
	// _TRACKER_ROUTE= sentinel was present in captured stdout (#212).
	if outcome.MissingRoute != nil {
		e.emit(PipelineEvent{
			Type:      EventToolRouteMissing,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    currentNodeID,
			Message: fmt.Sprintf("tool node %q: route_required is set but no _TRACKER_ROUTE= sentinel line was emitted to stdout — failing node to avoid silent fallback",
				currentNodeID),
			Route: outcome.MissingRoute,
		})
	}

	// Same shape again for auto_status (#346): the agent completed normally
	// but emitted no parseable STATUS line. The handler already chose the
	// status (fail-closed on goal gates, legacy success default otherwise);
	// this is the audit-trail companion so the TUI and `tracker diagnose`
	// can surface the anomaly instead of it registering as a silent verdict.
	if outcome.MissingStatus != nil {
		var msg string
		if outcome.MissingStatus.FailClosed {
			msg = fmt.Sprintf("node %q: auto_status is set but no parseable STATUS line was found — failing goal gate closed (an unparseable verdict on a gate is an anomaly, not a pass)",
				currentNodeID)
		} else {
			msg = fmt.Sprintf("node %q: auto_status is set but no parseable STATUS line was found — the STATUS verdict defaulted to success (legacy behavior; the node's final status may still differ, e.g. on a declared-writes failure; mark the node goal_gate: true to fail closed)",
				currentNodeID)
		}
		e.emit(PipelineEvent{
			Type:       EventAutoStatusMissing,
			Timestamp:  time.Now(),
			RunID:      s.runID,
			NodeID:     currentNodeID,
			Message:    msg,
			AutoStatus: outcome.MissingStatus,
		})
	}

	return &outcome, traceEntry, nil
}

// applyOutcome merges handler outcome into pipeline context and emits the decision_outcome event.
func (e *Engine) applyOutcome(s *runState, currentNodeID string, outcome *Outcome) {
	// Snapshot the outcome so advanceToNextNode can read OverrideActor (and
	// future override-related fields) when an override edge is traversed.
	// The pointer-derived copy is intentional: applyOutcome already mutates
	// pctx based on the outcome, so the snapshot here is a stable record of
	// what the handler returned for downstream edge handling.
	s.lastOutcome = *outcome

	s.pctx.Merge(outcome.ContextUpdates)

	// #348 defect 1: a goal gate that executes has, by definition,
	// re-evaluated — clear any pending recheck regardless of outcome,
	// INCLUDING an empty/unknown status (a fresh fail re-arms via the
	// normal exit-time gate check). This must not be gated on
	// outcome.Status != "": pending re-entries are budget-free, so a
	// still-pending flag after an empty-status execution would re-enter
	// the gate without ever charging retry budget.
	if isGoalGate(e.nodeOrDefault(currentNodeID)) {
		s.cp.ClearGateRecheckPending(currentNodeID)
	}
	if outcome.Status != "" {
		s.pctx.Set(ContextKeyOutcome, outcome.Status)
		s.nodeOutcomes[currentNodeID] = outcome.Status
	}
	if outcome.PreferredLabel != "" {
		s.pctx.Set(ContextKeyPreferredLabel, outcome.PreferredLabel)
	}
	if len(outcome.SuggestedNextNodes) > 0 {
		s.pctx.Set(ContextKeySuggestedNextNodes, strings.Join(outcome.SuggestedNextNodes, ","))
	}

	detail := &DecisionDetail{
		OutcomeStatus:   outcome.Status,
		ContextUpdates:  outcome.ContextUpdates,
		ContextSnapshot: e.routingContextSnapshot(s.pctx),
	}
	if outcome.Stats != nil {
		detail.TokenInput = outcome.Stats.InputTokens
		detail.TokenOutput = outcome.Stats.OutputTokens
	}
	e.emit(PipelineEvent{
		Type:      EventDecisionOutcome,
		Timestamp: time.Now(),
		RunID:     s.runID,
		NodeID:    currentNodeID,
		Message:   fmt.Sprintf("node %q outcome: %s", currentNodeID, outcome.Status),
		Decision:  detail,
	})

	// Absorb any child-propagated validation overrides into the run's sticky
	// list. This is the SECOND sticky-write site — the first is the flip-point
	// in advanceToNextNode (recordOverrideIfPresent) which handles own-graph
	// Edge.Override traversals. The two sites cover different sources:
	//   - own-graph: this run took an override edge directly.
	//   - child-propagated: a child run (subgraph, manager_loop, future
	//     parallel-with-subgraph branches) terminated with overrides, which
	//     the child handler folded into Outcome.ChildOverride with its own
	//     subgraph node ID prepended to SubgraphPath.
	// We append unconditionally — child-propagated entries always carry a
	// non-empty SubgraphPath and can never collide with own-graph entries
	// (which always have empty SubgraphPath), so the dedup check in
	// overrideAlreadyRecorded is unnecessary here.
	if len(outcome.ChildOverride) > 0 {
		for i := range outcome.ChildOverride {
			d := outcome.ChildOverride[i]
			s.appendOverride(d)
			// Emit a stage-level EventValidationOverridden so the audit
			// timeline records when the parent learned of the child's
			// override. NodeID is the subgraph node (the parent's view),
			// not the leaf gate (which lives in d.GateNodeID).
			e.emit(PipelineEvent{
				Type:      EventValidationOverridden,
				Timestamp: time.Now(),
				RunID:     s.runID,
				NodeID:    currentNodeID,
				Message:   fmt.Sprintf("validation override propagated from subgraph child via %q", currentNodeID),
				Override:  &d,
			})
		}
		// Synchronously persist after child propagation so a kill -9 between
		// here and the next selectEdge does not lose the propagated state.
		e.saveCheckpointWithTag(s.cp, s.pctx, s.runID, s, currentNodeID)
	}
}

// drainSteering non-blockingly drains all pending steering context updates from
// the steering channel and merges them into the run's pipeline context. Called
// between node executions so steered values are visible to edge selection and
// the next node. Mirrors agent/session_run.go:drainSteering().
func (e *Engine) drainSteering(s *runState) {
	if e.steeringCh == nil {
		return
	}
	for {
		select {
		case update, ok := <-e.steeringCh:
			if !ok {
				return
			}
			s.pctx.MergeWithoutDirty(update)
		default:
			return
		}
	}
}

// handleRetry processes a retry outcome. Returns (nextNodeID, shouldContinue, result, error).
// If shouldContinue is true, the main loop should continue with nextNodeID.
// If result is non-nil, the pipeline should return that result.
func (e *Engine) handleRetry(ctx context.Context, s *runState, currentNodeID string, execNode *Node, traceEntry *TraceEntry) (string, bool, *EngineResult, error) {
	policy := ResolveRetryPolicy(execNode, e.graph.Attrs)
	if s.cp.RetryCount(currentNodeID) < policy.MaxRetries {
		return e.handleRetryWithinBudget(ctx, s, currentNodeID, execNode, traceEntry, policy)
	}
	return e.handleRetryExhausted(s, currentNodeID, execNode, traceEntry)
}

// handleRetryWithinBudget runs a retry when budget remains: waits backoff, emits event, routes to target.
func (e *Engine) handleRetryWithinBudget(ctx context.Context, s *runState, currentNodeID string, execNode *Node, traceEntry *TraceEntry, policy *RetryPolicy) (string, bool, *EngineResult, error) {
	s.cp.IncrementRetry(currentNodeID)

	backoff := policy.BackoffFn(s.cp.RetryCount(currentNodeID)-1, policy.BaseDelay)
	if backoff > 0 {
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			e.saveCheckpoint(s.cp, s.pctx, s.runID)
			s.trace.EndTime = time.Now()
			return "", false, &EngineResult{
				RunID:               s.runID,
				Status:              OutcomeFail,
				CompletedNodes:      s.cp.CompletedNodes,
				Context:             s.pctx.Snapshot(),
				Trace:               s.trace,
				Usage:               s.trace.AggregateUsage(),
				ValidationOverrides: append([]OverrideDetail(nil), s.validationOverrides...),
			}, fmt.Errorf("pipeline cancelled during retry backoff: %w", ctx.Err())
		}
	}

	e.emit(PipelineEvent{
		Type:      EventStageRetrying,
		Timestamp: time.Now(),
		RunID:     s.runID,
		NodeID:    currentNodeID,
		Message:   fmt.Sprintf("retrying node %q (attempt %d/%d, policy=%s)", currentNodeID, s.cp.RetryCount(currentNodeID), policy.MaxRetries, policy.Name),
	})

	target := currentNodeID
	if rt, ok := execNode.Attrs["retry_target"]; ok {
		target = rt
	}
	traceEntry.EdgeTo = target
	s.trace.AddEntry(*traceEntry)
	e.emitGitCommit(s, currentNodeID, traceEntry)
	e.emitCostUpdate(s)
	if lr := e.checkBudgetAfterEmit(s); lr != nil {
		return "", false, lr.result, nil
	}
	e.clearDownstream(target, s.cp)
	s.cp.CurrentNode = target
	e.saveCheckpointWithTag(s.cp, s.pctx, s.runID, s, currentNodeID)
	return target, true, nil, nil
}

// handleRetryExhausted handles the case when retry budget is depleted.
// Routes to fallback target if available, otherwise fails the pipeline.
func (e *Engine) handleRetryExhausted(s *runState, currentNodeID string, execNode *Node, traceEntry *TraceEntry) (string, bool, *EngineResult, error) {
	// Preserve any dirty (possibly green) tree to a recoverable ref before
	// routing away from the exhausted node (#302). No-op on a clean tree.
	e.commitWIPBeforeRouting(s, currentNodeID, traceEntry)
	if fallback, ok := execNode.Attrs["fallback_retry_target"]; ok {
		traceEntry.EdgeTo = fallback
		s.trace.AddEntry(*traceEntry)
		e.emitGitCommit(s, currentNodeID, traceEntry)
		e.emitCostUpdate(s)
		if lr := e.checkBudgetAfterEmit(s); lr != nil {
			return "", false, lr.result, nil
		}
		e.budgetGuard.NotifyProgress()
		e.clearDownstream(fallback, s.cp)
		s.cp.CurrentNode = fallback
		e.saveCheckpointWithTag(s.cp, s.pctx, s.runID, s, currentNodeID)
		return fallback, true, nil, nil
	}

	// No fallback — fail.
	s.trace.AddEntry(*traceEntry)
	e.emit(PipelineEvent{
		Type:      EventStageFailed,
		Timestamp: time.Now(),
		RunID:     s.runID,
		NodeID:    currentNodeID,
		Message:   fmt.Sprintf("retries exhausted for node %q", currentNodeID),
	})
	e.emitGitCommit(s, currentNodeID, traceEntry)
	s.trace.EndTime = time.Now()
	result := e.failResult(s)
	return "", false, result, nil
}

// handleOutcomeStatus emits events and marks completion for non-retry outcomes.
func (e *Engine) handleOutcomeStatus(s *runState, currentNodeID string, status string) {
	switch status {
	case string(OutcomeFail):
		e.emit(PipelineEvent{
			Type:      EventStageFailed,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    currentNodeID,
			Message:   fmt.Sprintf("node %q failed", currentNodeID),
		})
		s.cp.MarkCompleted(currentNodeID)

	case string(OutcomeSuccess):
		e.emit(PipelineEvent{
			Type:      EventStageCompleted,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    currentNodeID,
			Message:   fmt.Sprintf("node %q completed", currentNodeID),
		})
		s.cp.MarkCompleted(currentNodeID)

	default:
		e.emit(PipelineEvent{
			Type:      EventStageFailed,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    currentNodeID,
			Message:   fmt.Sprintf("node %q returned unknown outcome status %q; treating as failure", currentNodeID, status),
		})
		s.pctx.Set(ContextKeyOutcome, string(OutcomeFail))
	}
}

// handleExitNode processes the exit node. Returns (shouldBreak, result, error).
// If shouldBreak is true, the main loop should break (success).
// If result is non-nil, return early with that result.
// If neither, a retry target was found and currentNodeID should be updated by the caller.
func (e *Engine) handleExitNode(s *runState, currentNodeID string, outcomeStatus string, traceEntry *TraceEntry) (bool, string, *EngineResult) {
	target, gateNodeID, retry, unsatisfied := e.goalGateRetryTarget(s.cp, s.nodeOutcomes)
	if retry {
		// A pending re-entry (target == the gate itself, flagged by a prior
		// redirect) completes that redirect's retry cycle — the budget was
		// charged when the redirect fired, so it is not charged again here.
		// It cannot loop: the gate executes next, clearing the pending flag.
		reentry := s.cp.IsGateRecheckPending(gateNodeID) && target == gateNodeID
		gateNode := e.nodeOrDefault(gateNodeID)
		msg := fmt.Sprintf("goal-gate recheck: re-entering %q so the gate re-judges the current tree (attempt %d/%d)",
			gateNodeID, s.cp.RetryCount(gateNodeID), e.maxRetries(gateNode))
		if !reentry {
			s.cp.IncrementRetry(gateNodeID)
			msg = fmt.Sprintf("goal-gate retry for %q → %q (attempt %d/%d)",
				gateNodeID, target,
				s.cp.RetryCount(gateNodeID), e.maxRetries(gateNode))
		}
		e.emit(PipelineEvent{
			Type:      EventStageRetrying,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    gateNodeID,
			Message:   msg,
		})
		traceEntry.EdgeTo = target
		s.trace.AddEntry(*traceEntry)
		e.emitGitCommit(s, currentNodeID, traceEntry)
		// #348 defect 1: the redirect's clearDownstream below may remove the
		// gate from CompletedNodes while the executed path routes around it
		// to the exit. Mark the gate recheck-pending so it stays visible to
		// this check and the next retry re-enters at the gate itself; the
		// flag clears when the gate actually re-executes (applyOutcome).
		s.cp.SetGateRecheckPending(gateNodeID)
		e.clearDownstream(target, s.cp)
		s.cp.CurrentNode = target
		e.saveCheckpointWithTag(s.cp, s.pctx, s.runID, s, currentNodeID)
		return false, target, nil
	}
	// Fallback/escalation: target is set but not a retry (one-time redirect).
	if unsatisfied && target != "" {
		e.emit(PipelineEvent{
			Type:      EventStageFailed,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    gateNodeID,
			Message: fmt.Sprintf("goal-gate retries exhausted for %q after %d attempts, routing to fallback %q",
				gateNodeID, s.cp.RetryCount(gateNodeID), target),
		})
		traceEntry.EdgeTo = target
		s.trace.AddEntry(*traceEntry)
		// Same #348 marking as the retry path above: the one-shot fallback's
		// clearDownstream must not let the gate vanish from the exit check.
		s.cp.SetGateRecheckPending(gateNodeID)
		e.clearDownstream(target, s.cp)
		s.cp.CurrentNode = target
		e.saveCheckpointWithTag(s.cp, s.pctx, s.runID, s, currentNodeID)
		return false, target, nil
	}
	if unsatisfied {
		if gateNodeID != "" {
			e.emit(PipelineEvent{
				Type:      EventStageFailed,
				Timestamp: time.Now(),
				RunID:     s.runID,
				NodeID:    gateNodeID,
				Message: fmt.Sprintf("goal-gate retries exhausted for %q after %d attempts",
					gateNodeID, s.cp.RetryCount(gateNodeID)),
			})
		}
		s.trace.AddEntry(*traceEntry)
		e.emitGitCommit(s, currentNodeID, traceEntry)
		s.trace.EndTime = time.Now()
		result := e.failResult(s)
		return false, "", result
	}
	if outcomeStatus == string(OutcomeFail) {
		// Preserve any dirty (possibly green) tree to a recoverable ref before
		// the failing exit node halts the run (#302). No-op on a clean tree.
		e.commitWIPBeforeRouting(s, currentNodeID, traceEntry)
		s.trace.AddEntry(*traceEntry)
		e.emitGitCommit(s, currentNodeID, traceEntry)
		s.trace.EndTime = time.Now()
		result := e.failResult(s)
		return false, "", result
	}
	s.trace.AddEntry(*traceEntry)
	e.emitGitCommit(s, currentNodeID, traceEntry)
	e.emitCostUpdate(s)
	if halt := e.checkBudgetHaltForExit(s); halt != nil {
		return false, "", halt
	}
	e.budgetGuard.NotifyProgress()
	return true, "", nil
}

// handleLoopRestart processes a loop-back to an already-completed node.
// Returns (nextNodeID, shouldContinue, result, error).
func (e *Engine) handleLoopRestart(s *runState, nextTo string, traceEntry *TraceEntry) (string, bool, *EngineResult, error) {
	maxRestarts := e.maxRestartsAllowed()
	if s.cp.RestartCount >= maxRestarts {
		e.emit(PipelineEvent{
			Type:      EventPipelineFailed,
			Timestamp: time.Now(),
			RunID:     s.runID,
			Message:   fmt.Sprintf("max restarts (%d) exceeded", maxRestarts),
		})
		e.saveCheckpoint(s.cp, s.pctx, s.runID)
		s.trace.EndTime = time.Now()
		return "", false, &EngineResult{
			RunID:               s.runID,
			Status:              OutcomeFail,
			CompletedNodes:      s.cp.CompletedNodes,
			Context:             s.pctx.Snapshot(),
			Trace:               s.trace,
			Usage:               s.trace.AggregateUsage(),
			ValidationOverrides: append([]OverrideDetail(nil), s.validationOverrides...),
		}, fmt.Errorf("max restarts (%d) exceeded", maxRestarts)
	}

	s.cp.RestartCount++

	restartTarget := nextTo
	if rt, ok := e.graph.Attrs["restart_target"]; ok && rt != "" {
		if _, exists := e.graph.Nodes[rt]; exists {
			restartTarget = rt
		}
	}

	e.emit(PipelineEvent{
		Type:      EventLoopRestart,
		Timestamp: time.Now(),
		RunID:     s.runID,
		NodeID:    restartTarget,
		Message:   fmt.Sprintf("loop detected, restarting from %q (restart %d/%d)", restartTarget, s.cp.RestartCount, maxRestarts),
	})

	clearedNodes := append([]string{restartTarget}, downstreamNodes(e.graph, restartTarget)...)

	e.emit(PipelineEvent{
		Type:      EventDecisionRestart,
		Timestamp: time.Now(),
		RunID:     s.runID,
		NodeID:    restartTarget,
		Message:   fmt.Sprintf("restart %d: clearing %d nodes from %q", s.cp.RestartCount, len(clearedNodes), restartTarget),
		Decision: &DecisionDetail{
			RestartCount:    s.cp.RestartCount,
			ClearedNodes:    clearedNodes,
			ContextSnapshot: e.routingContextSnapshot(s.pctx),
		},
	})

	e.clearDownstream(restartTarget, s.cp)
	e.clearDownstreamRetryCounts(restartTarget, s.cp)

	s.cp.CurrentNode = restartTarget
	e.saveCheckpoint(s.cp, s.pctx, s.runID)
	return restartTarget, true, nil, nil
}
