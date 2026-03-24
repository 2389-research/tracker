// ABOUTME: Core pipeline execution engine that traverses graphs, executes handlers, and manages control flow.
// ABOUTME: Supports edge selection (conditions, labels, weights), retries, goal gates, and checkpoint resume.
package pipeline

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EngineResult holds the final outcome of a pipeline execution run.
type EngineResult struct {
	RunID          string
	Status         string
	CompletedNodes []string
	Context        map[string]string
	Trace          *Trace
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

// Run executes the pipeline to completion or failure.
func (e *Engine) Run(ctx context.Context) (*EngineResult, error) {
	runID := generateRunID()
	nodeOutcomes := make(map[string]string)

	// Auto-derive checkpoint path from artifact directory when no explicit
	// checkpoint path is configured. This ensures every run automatically
	// saves checkpoint state for resume support.
	if e.checkpointPath == "" && e.artifactDir != "" {
		e.checkpointPath = filepath.Join(e.artifactDir, runID, "checkpoint.json")
	}

	pctx := NewPipelineContext()
	for key, value := range e.graph.Attrs {
		pctx.Set("graph."+key, value)
	}
	// Apply initial context values (e.g., from a parent subgraph handler).
	for k, v := range e.initialContext {
		pctx.Set(k, v)
	}
	cp, err := e.loadOrCreateCheckpoint(runID)
	if err != nil {
		return nil, fmt.Errorf("checkpoint load: %w", err)
	}
	if cp.RunID != "" {
		runID = cp.RunID
	}

	// Restore context from checkpoint.
	for k, v := range cp.Context {
		pctx.Set(k, v)
	}

	// Apply fidelity-aware context compaction on resume. When resuming from a
	// checkpoint, in-memory session state is lost, so we degrade fidelity one
	// level and compact the restored context accordingly.
	if cp.CurrentNode != "" && len(cp.CompletedNodes) > 0 {
		// Preserve routing hints before compaction strips them — the
		// resume loop needs outcome/preferred_label to route through
		// already-completed conditional nodes.
		routingHints := make(map[string]string)
		for _, key := range []string{ContextKeyOutcome, ContextKeyPreferredLabel, "suggested_next_nodes"} {
			if val, ok := pctx.Get(key); ok && val != "" {
				routingHints[key] = val
			}
		}

		fidelity := ResolveFidelity(
			e.nodeOrDefault(cp.CurrentNode),
			e.graph.Attrs,
		)
		degraded := DegradeFidelity(fidelity)
		compacted := CompactContext(pctx, cp.CompletedNodes, degraded, e.artifactDir, runID)
		// Replace pipeline context with the compacted version.
		for k := range pctx.Snapshot() {
			pctx.Set(k, "")
		}
		for k, v := range compacted {
			pctx.Set(k, v)
		}
		// Re-inject routing hints that compaction may have dropped.
		for k, v := range routingHints {
			if existing, ok := pctx.Get(k); !ok || existing == "" {
				pctx.Set(k, v)
			}
		}
	}

	// Set artifact directory so handlers can write artifacts outside the working directory.
	if e.artifactDir != "" {
		pctx.SetInternal(InternalKeyArtifactDir, filepath.Join(e.artifactDir, runID))
	}

	// Parse stylesheet if enabled and available.
	var stylesheet *Stylesheet
	if e.resolveStylesheet {
		if ssRaw, ok := e.graph.Attrs["model_stylesheet"]; ok {
			ss, err := ParseStylesheet(ssRaw)
			if err != nil {
				return nil, fmt.Errorf("parse stylesheet: %w", err)
			}
			stylesheet = ss
		}
	}

	trace := &Trace{
		RunID:     runID,
		StartTime: time.Now(),
	}

	e.emit(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     runID,
		Message:   "pipeline started",
	})

	// Determine starting node.
	currentNodeID := e.graph.StartNode
	if cp.CurrentNode != "" {
		currentNodeID = cp.CurrentNode
	}

	for {
		// Check context cancellation at the top of each iteration.
		if err := ctx.Err(); err != nil {
			e.saveCheckpoint(cp, pctx, runID)
			e.emit(PipelineEvent{
				Type:      EventPipelineFailed,
				Timestamp: time.Now(),
				RunID:     runID,
				Message:   "context cancelled",
				Err:       err,
			})
			return &EngineResult{
				RunID:          runID,
				Status:         OutcomeFail,
				CompletedNodes: cp.CompletedNodes,
				Context:        pctx.Snapshot(),
				Trace:          trace,
			}, fmt.Errorf("pipeline cancelled: %w", err)
		}

		node, ok := e.graph.Nodes[currentNodeID]
		if !ok {
			return nil, fmt.Errorf("node %q not found in graph", currentNodeID)
		}

		// Skip already-completed nodes on resume, emitting synthetic events
		// so the TUI shows them as green/done. Use stored edge selections
		// from the checkpoint to replay routing decisions deterministically,
		// falling back to selectEdge only when no stored selection exists.
		if cp.IsCompleted(currentNodeID) {
			e.emit(PipelineEvent{
				Type:      EventStageCompleted,
				Timestamp: time.Now(),
				RunID:     runID,
				NodeID:    currentNodeID,
				Message:   "previously completed (resumed)",
			})
			edges := e.graph.OutgoingEdges(currentNodeID)
			if len(edges) == 0 {
				// Completed exit node — we're done.
				break
			}
			// Prefer stored edge selection from the original run.
			if storedTo, ok := cp.GetEdgeSelection(currentNodeID); ok {
				currentNodeID = storedTo
				continue
			}
			next, err := e.selectEdge(edges, pctx)
			if err != nil {
				return nil, fmt.Errorf("select edge from completed node %q: %w", currentNodeID, err)
			}
			currentNodeID = next.To
			continue
		}

		// Clear per-node edge selection hints so stale values from a
		// previous node don't pollute routing for the current one.
		pctx.Set(ContextKeyOutcome, "")
		pctx.Set(ContextKeyPreferredLabel, "")
		pctx.Set("suggested_next_nodes", "")

		// Apply stylesheet to a copy of node attrs so the shared Graph
		// is not mutated. Handlers see resolved attrs; the original node
		// retains its explicit attributes for future runs.
		execNode := node
		if stylesheet != nil {
			resolved := stylesheet.Resolve(node)
			execNode = &Node{
				ID:      node.ID,
				Shape:   node.Shape,
				Label:   node.Label,
				Handler: node.Handler,
				Attrs:   resolved,
			}
		}
		if prompt := execNode.Attrs["prompt"]; prompt != "" {
			execAttrs := make(map[string]string, len(execNode.Attrs))
			for k, v := range execNode.Attrs {
				execAttrs[k] = v
			}
			execAttrs["prompt"] = ExpandPromptVariables(prompt, pctx)
			execNode = &Node{
				ID:      execNode.ID,
				Shape:   execNode.Shape,
				Label:   execNode.Label,
				Handler: execNode.Handler,
				Attrs:   execAttrs,
			}
		}

		e.emit(PipelineEvent{
			Type:      EventStageStarted,
			Timestamp: time.Now(),
			RunID:     runID,
			NodeID:    currentNodeID,
			Message:   fmt.Sprintf("executing node %q", currentNodeID),
		})

		// Execute handler with the (possibly stylesheet-resolved) node.
		handlerStart := time.Now()
		outcome, err := e.registry.Execute(ctx, execNode, pctx)
		handlerDuration := time.Since(handlerStart)
		if err != nil {
			trace.AddEntry(TraceEntry{
				Timestamp:   handlerStart,
				NodeID:      currentNodeID,
				HandlerName: execNode.Handler,
				Status:      "error",
				Duration:    handlerDuration,
				Error:       err.Error(),
			})
			e.emit(PipelineEvent{
				Type:      EventStageFailed,
				Timestamp: time.Now(),
				RunID:     runID,
				NodeID:    currentNodeID,
				Message:   fmt.Sprintf("handler error at node %q", currentNodeID),
				Err:       err,
			})
			e.saveCheckpoint(cp, pctx, runID)
			trace.EndTime = time.Now()
			return &EngineResult{
				RunID:          runID,
				Status:         OutcomeFail,
				CompletedNodes: cp.CompletedNodes,
				Context:        pctx.Snapshot(),
				Trace:          trace,
			}, fmt.Errorf("handler error at node %q: %w", currentNodeID, err)
		}

		// Merge context updates from handler outcome.
		pctx.Merge(outcome.ContextUpdates)

		// Store outcome and edge selection hints in context.
		if outcome.Status != "" {
			pctx.Set(ContextKeyOutcome, outcome.Status)
			nodeOutcomes[currentNodeID] = outcome.Status
		}
		if outcome.PreferredLabel != "" {
			pctx.Set(ContextKeyPreferredLabel, outcome.PreferredLabel)
		}
		if len(outcome.SuggestedNextNodes) > 0 {
			pctx.Set("suggested_next_nodes", strings.Join(outcome.SuggestedNextNodes, ","))
		}

		// Emit decision_outcome event capturing handler result details.
		{
			detail := &DecisionDetail{
				OutcomeStatus:   outcome.Status,
				ContextUpdates:  outcome.ContextUpdates,
				ContextSnapshot: e.routingContextSnapshot(pctx),
			}
			if outcome.Stats != nil {
				detail.TokenInput = outcome.Stats.CacheHits   // input tokens approximated by cache hits
				detail.TokenOutput = outcome.Stats.CacheMisses // output tokens approximated by cache misses
			}
			e.emit(PipelineEvent{
				Type:      EventDecisionOutcome,
				Timestamp: time.Now(),
				RunID:     runID,
				NodeID:    currentNodeID,
				Message:   fmt.Sprintf("node %q outcome: %s", currentNodeID, outcome.Status),
				Decision:  detail,
			})
		}

		// Build trace entry for this node execution. EdgeTo is filled in
		// after edge selection below; early-return paths set it directly.
		traceEntry := TraceEntry{
			Timestamp:   handlerStart,
			NodeID:      currentNodeID,
			HandlerName: execNode.Handler,
			Status:      outcome.Status,
			Duration:    handlerDuration,
			Stats:       outcome.Stats,
		}

		switch outcome.Status {
		case OutcomeRetry:
			policy := ResolveRetryPolicy(execNode, e.graph.Attrs)
			if cp.RetryCount(currentNodeID) < policy.MaxRetries {
				cp.IncrementRetry(currentNodeID)

				// Apply backoff before retrying, respecting context cancellation.
				// Use count-1 because IncrementRetry already advanced the counter
				// and ExponentialBackoff treats attempt 0 as the first base-delay wait.
				backoff := policy.BackoffFn(cp.RetryCount(currentNodeID)-1, policy.BaseDelay)
				if backoff > 0 {
					select {
					case <-time.After(backoff):
					case <-ctx.Done():
						e.saveCheckpoint(cp, pctx, runID)
						return &EngineResult{
							RunID:          runID,
							Status:         OutcomeFail,
							CompletedNodes: cp.CompletedNodes,
							Context:        pctx.Snapshot(),
							Trace:          trace,
						}, fmt.Errorf("pipeline cancelled during retry backoff: %w", ctx.Err())
					}
				}

				e.emit(PipelineEvent{
					Type:      EventStageRetrying,
					Timestamp: time.Now(),
					RunID:     runID,
					NodeID:    currentNodeID,
					Message:   fmt.Sprintf("retrying node %q (attempt %d/%d, policy=%s)", currentNodeID, cp.RetryCount(currentNodeID), policy.MaxRetries, policy.Name),
				})
				// Jump to retry_target if specified, otherwise retry self.
				target := currentNodeID
				if rt, ok := execNode.Attrs["retry_target"]; ok {
					target = rt
				}
				traceEntry.EdgeTo = target
				trace.AddEntry(traceEntry)
				// Clear completion status for all nodes reachable from the
				// retry target so they re-execute on the next pass.
				e.clearDownstream(target, cp)
				cp.CurrentNode = target
				e.saveCheckpoint(cp, pctx, runID)
				currentNodeID = target
				continue
			}

			// Retries exhausted — check fallback.
			if fallback, ok := execNode.Attrs["fallback_retry_target"]; ok {
				traceEntry.EdgeTo = fallback
				trace.AddEntry(traceEntry)
				e.clearDownstream(fallback, cp)
				cp.CurrentNode = fallback
				e.saveCheckpoint(cp, pctx, runID)
				currentNodeID = fallback
				continue
			}

			// No fallback — fail.
			trace.AddEntry(traceEntry)
			e.emit(PipelineEvent{
				Type:      EventStageFailed,
				Timestamp: time.Now(),
				RunID:     runID,
				NodeID:    currentNodeID,
				Message:   fmt.Sprintf("retries exhausted for node %q", currentNodeID),
			})
			trace.EndTime = time.Now()
			result := e.failResult(runID, cp, pctx)
			result.Trace = trace
			return result, nil

		case OutcomeFail:
			e.emit(PipelineEvent{
				Type:      EventStageFailed,
				Timestamp: time.Now(),
				RunID:     runID,
				NodeID:    currentNodeID,
				Message:   fmt.Sprintf("node %q failed", currentNodeID),
			})
			cp.MarkCompleted(currentNodeID)

		case OutcomeSuccess:
			e.emit(PipelineEvent{
				Type:      EventStageCompleted,
				Timestamp: time.Now(),
				RunID:     runID,
				NodeID:    currentNodeID,
				Message:   fmt.Sprintf("node %q completed", currentNodeID),
			})
			cp.MarkCompleted(currentNodeID)

		default:
			// Treat unknown status as success but warn so it's observable.
			e.emit(PipelineEvent{
				Type:      EventWarning,
				Timestamp: time.Now(),
				RunID:     runID,
				NodeID:    currentNodeID,
				Message:   fmt.Sprintf("unknown outcome status %q from node %q; treating as success", outcome.Status, currentNodeID),
			})
			cp.MarkCompleted(currentNodeID)
		}

		// Check if this is an exit node — pipeline complete.
		if currentNodeID == e.graph.ExitNode {
			target, retry, unsatisfied := e.goalGateRetryTarget(cp, nodeOutcomes)
			if retry {
				traceEntry.EdgeTo = target
				trace.AddEntry(traceEntry)
				e.clearDownstream(target, cp)
				cp.CurrentNode = target
				e.saveCheckpoint(cp, pctx, runID)
				currentNodeID = target
				continue
			}
			if unsatisfied {
				trace.AddEntry(traceEntry)
				trace.EndTime = time.Now()
				result := e.failResult(runID, cp, pctx)
				result.Trace = trace
				return result, nil
			}
			if outcome.Status == OutcomeFail {
				trace.AddEntry(traceEntry)
				trace.EndTime = time.Now()
				result := e.failResult(runID, cp, pctx)
				result.Trace = trace
				return result, nil
			}
			trace.AddEntry(traceEntry)
			break
		}

		// Select next edge.
		edges := e.graph.OutgoingEdges(currentNodeID)
		if len(edges) == 0 {
			trace.AddEntry(traceEntry)
			return nil, fmt.Errorf("no outgoing edges from non-exit node %q", currentNodeID)
		}

		next, err := e.selectEdge(edges, pctx)
		if err != nil {
			trace.AddEntry(traceEntry)
			return nil, fmt.Errorf("select edge from %q: %w", currentNodeID, err)
		}

		traceEntry.EdgeTo = next.To
		trace.AddEntry(traceEntry)

		// Store the edge selection so checkpoint resume can replay it.
		cp.SetEdgeSelection(currentNodeID, next.To)

		// If the next node was already completed in this run (loop-back),
		// trigger restart loop handling instead of just clearing one node.
		if cp.IsCompleted(next.To) {
			maxRestarts := e.maxRestartsAllowed()
			if cp.RestartCount >= maxRestarts {
				e.emit(PipelineEvent{
					Type:      EventPipelineFailed,
					Timestamp: time.Now(),
					RunID:     runID,
					Message:   fmt.Sprintf("max restarts (%d) exceeded", maxRestarts),
				})
				e.saveCheckpoint(cp, pctx, runID)
				trace.EndTime = time.Now()
				return &EngineResult{
					RunID:          runID,
					Status:         OutcomeFail,
					CompletedNodes: cp.CompletedNodes,
					Context:        pctx.Snapshot(),
					Trace:          trace,
				}, fmt.Errorf("max restarts (%d) exceeded", maxRestarts)
			}

			cp.RestartCount++

			// Determine restart target: graph attr overrides the re-entered node.
			restartTarget := next.To
			if rt, ok := e.graph.Attrs["restart_target"]; ok && rt != "" {
				if _, exists := e.graph.Nodes[rt]; exists {
					restartTarget = rt
				}
			}

			e.emit(PipelineEvent{
				Type:      EventLoopRestart,
				Timestamp: time.Now(),
				RunID:     runID,
				NodeID:    restartTarget,
				Message:   fmt.Sprintf("loop detected, restarting from %q (restart %d/%d)", restartTarget, cp.RestartCount, maxRestarts),
			})

			// Collect nodes that will be cleared for the audit trail.
			clearedNodes := append([]string{restartTarget}, downstreamNodes(e.graph, restartTarget)...)

			// Emit decision_restart event with restart details.
			e.emit(PipelineEvent{
				Type:      EventDecisionRestart,
				Timestamp: time.Now(),
				RunID:     runID,
				NodeID:    restartTarget,
				Message:   fmt.Sprintf("restart %d: clearing %d nodes from %q", cp.RestartCount, len(clearedNodes), restartTarget),
				Decision: &DecisionDetail{
					RestartCount:    cp.RestartCount,
					ClearedNodes:    clearedNodes,
					ContextSnapshot: e.routingContextSnapshot(pctx),
				},
			})

			// Clear the restart target and all its downstream nodes from completed.
			e.clearDownstream(restartTarget, cp)
			// Reset retry counts for cleared nodes so they get fresh budgets.
			e.clearDownstreamRetryCounts(restartTarget, cp)

			cp.CurrentNode = restartTarget
			e.saveCheckpoint(cp, pctx, runID)
			currentNodeID = restartTarget
			continue
		}

		currentNodeID = next.To
		cp.CurrentNode = currentNodeID
		e.saveCheckpoint(cp, pctx, runID)
	}

	trace.EndTime = time.Now()

	e.emit(PipelineEvent{
		Type:      EventPipelineCompleted,
		Timestamp: time.Now(),
		RunID:     runID,
		Message:   "pipeline completed",
	})

	return &EngineResult{
		RunID:          runID,
		Status:         OutcomeSuccess,
		CompletedNodes: cp.CompletedNodes,
		Context:        pctx.Snapshot(),
		Trace:          trace,
	}, nil
}

// selectEdge picks the best outgoing edge using priority: condition > preferred label > suggested IDs > weight > lexical.
func (e *Engine) selectEdge(edges []*Edge, pctx *PipelineContext) (*Edge, error) {
	// Build a context snapshot for decision audit trail.
	ctxSnap := e.routingContextSnapshot(pctx)

	// Priority 1: Condition match.
	for _, edge := range edges {
		if edge.Condition != "" {
			match, err := EvaluateCondition(edge.Condition, pctx)
			if err != nil {
				return nil, fmt.Errorf("evaluate condition on edge %s->%s: %w", edge.From, edge.To, err)
			}
			// Emit condition evaluation event for every tested condition.
			e.emit(PipelineEvent{
				Type:      EventDecisionCondition,
				Timestamp: time.Now(),
				NodeID:    edge.From,
				Message:   fmt.Sprintf("condition %q on edge %s->%s evaluated to %v", edge.Condition, edge.From, edge.To, match),
				Decision: &DecisionDetail{
					EdgeFrom:        edge.From,
					EdgeTo:          edge.To,
					EdgeCondition:   edge.Condition,
					ConditionMatch:  match,
					ContextSnapshot: ctxSnap,
				},
			})
			if match {
				e.emitEdgeSelected(edge, "condition", ctxSnap)
				return edge, nil
			}
		}
	}

	// Priority 2: Preferred label match (from outcome or context).
	if preferred, ok := pctx.Get(ContextKeyPreferredLabel); ok && preferred != "" {
		for _, edge := range edges {
			if edge.Label == preferred {
				e.emitEdgeSelected(edge, "label", ctxSnap)
				return edge, nil
			}
		}
	}

	// Priority 3: Suggested next nodes (handler suggests specific targets).
	if suggested, ok := pctx.Get("suggested_next_nodes"); ok && suggested != "" {
		for _, edge := range edges {
			for _, sid := range strings.Split(suggested, ",") {
				if strings.TrimSpace(sid) == edge.To {
					e.emitEdgeSelected(edge, "suggested", ctxSnap)
					return edge, nil
				}
			}
		}
	}

	// Priority 4: Edge weight (higher wins).
	// Filter to edges without conditions (unconditional edges).
	var unconditional []*Edge
	for _, edge := range edges {
		if edge.Condition == "" {
			unconditional = append(unconditional, edge)
		}
	}
	if len(unconditional) == 0 {
		// Build diagnostic: show each condition and the context values it references.
		var diag []string
		for _, edge := range edges {
			if edge.Condition != "" {
				outcomeVal, _ := pctx.Get(ContextKeyOutcome)
				diag = append(diag, fmt.Sprintf("  %s->%s condition=%q (outcome=%q)", edge.From, edge.To, edge.Condition, outcomeVal))
			}
		}
		return nil, fmt.Errorf("no matching edges: all %d edges have conditions that evaluated to false:\n%s", len(edges), strings.Join(diag, "\n"))
	}

	sort.SliceStable(unconditional, func(i, j int) bool {
		wi := edgeWeight(unconditional[i])
		wj := edgeWeight(unconditional[j])
		if wi != wj {
			return wi > wj
		}
		// Priority 5: Lexical ordering by To field.
		return unconditional[i].To < unconditional[j].To
	})

	// Determine the priority level used for the winning edge.
	priority := "weight"
	if len(unconditional) > 1 && edgeWeight(unconditional[0]) == edgeWeight(unconditional[1]) {
		priority = "lexical"
		// Warn when multiple unconditional edges have equal weight and lexical
		// tiebreaker is used — this may indicate a missing condition or weight.
		e.emit(PipelineEvent{
			Type:      EventEdgeTiebreaker,
			Timestamp: time.Now(),
			NodeID:    unconditional[0].From,
			Message:   fmt.Sprintf("lexical tiebreaker used: %d unconditional edges from %q with equal weight; selected %q", len(unconditional), unconditional[0].From, unconditional[0].To),
		})
	}

	e.emitEdgeSelected(unconditional[0], priority, ctxSnap)
	return unconditional[0], nil
}

// emitEdgeSelected emits a decision_edge event recording which edge was selected and why.
func (e *Engine) emitEdgeSelected(edge *Edge, priority string, ctxSnap map[string]string) {
	e.emit(PipelineEvent{
		Type:      EventDecisionEdge,
		Timestamp: time.Now(),
		NodeID:    edge.From,
		Message:   fmt.Sprintf("edge selected %s->%s via %s", edge.From, edge.To, priority),
		Decision: &DecisionDetail{
			EdgeFrom:        edge.From,
			EdgeTo:          edge.To,
			EdgeCondition:   edge.Condition,
			EdgePriority:    priority,
			ContextSnapshot: ctxSnap,
		},
	})
}

// routingContextSnapshot returns a map of the key context values relevant to edge routing.
func (e *Engine) routingContextSnapshot(pctx *PipelineContext) map[string]string {
	snap := make(map[string]string)
	for _, key := range []string{ContextKeyOutcome, ContextKeyPreferredLabel, ContextKeyToolStdout, ContextKeyHumanResponse, "suggested_next_nodes"} {
		if val, ok := pctx.Get(key); ok && val != "" {
			snap[key] = val
		}
	}
	return snap
}

// edgeWeight parses the "weight" attribute as an integer, defaulting to 0.
func edgeWeight(e *Edge) int {
	if w, ok := e.Attrs["weight"]; ok {
		if n, err := strconv.Atoi(w); err == nil {
			return n
		}
	}
	return 0
}

// maxRetries returns the max retry count for a node, checking node attrs then graph default.
func (e *Engine) maxRetries(node *Node) int {
	if mr, ok := node.Attrs["max_retries"]; ok {
		if n, err := strconv.Atoi(mr); err == nil {
			return n
		}
	}
	if mr, ok := e.graph.Attrs["default_max_retry"]; ok {
		if n, err := strconv.Atoi(mr); err == nil {
			return n
		}
	}
	return 3
}

// isGoalGate checks whether a node is marked as a goal gate.
func isGoalGate(node *Node) bool {
	return node.Attrs["goal_gate"] == "true"
}

// loadOrCreateCheckpoint loads an existing checkpoint or creates a fresh one.
// Returns an error if the checkpoint file exists but is corrupt.
func (e *Engine) loadOrCreateCheckpoint(runID string) (*Checkpoint, error) {
	if e.checkpointPath != "" {
		cp, err := LoadCheckpoint(e.checkpointPath)
		if err == nil {
			return cp, nil
		}
		// Only ignore file-not-found; corrupt checkpoints are real errors.
		if !os.IsNotExist(unwrapPathError(err)) {
			return nil, fmt.Errorf("corrupt checkpoint at %s: %w", e.checkpointPath, err)
		}
	}
	return &Checkpoint{
		RunID:          runID,
		CompletedNodes: []string{},
		RetryCounts:    map[string]int{},
		Context:        map[string]string{},
	}, nil
}

// saveCheckpoint persists the current checkpoint if a path is configured.
func (e *Engine) saveCheckpoint(cp *Checkpoint, pctx *PipelineContext, runID string) {
	if e.checkpointPath == "" {
		return
	}
	cp.RunID = runID
	cp.Context = pctx.Snapshot()
	cp.Timestamp = time.Now()
	if err := SaveCheckpoint(cp, e.checkpointPath); err != nil {
		// Log but don't fail the pipeline for checkpoint errors.
		e.emit(PipelineEvent{
			Type:      EventCheckpointFailed,
			Timestamp: time.Now(),
			RunID:     runID,
			Message:   fmt.Sprintf("checkpoint save error: %v", err),
			Err:       err,
		})
		return
	}
	e.emit(PipelineEvent{
		Type:      EventCheckpointSaved,
		Timestamp: time.Now(),
		RunID:     runID,
		Message:   "checkpoint saved",
	})
}

// emit sends a pipeline event to the configured handler.
func (e *Engine) emit(evt PipelineEvent) {
	e.eventHandler.HandlePipelineEvent(evt)
}

// failResult builds an EngineResult with fail status.
func (e *Engine) failResult(runID string, cp *Checkpoint, pctx *PipelineContext) *EngineResult {
	e.emit(PipelineEvent{
		Type:      EventPipelineFailed,
		Timestamp: time.Now(),
		RunID:     runID,
		Message:   "pipeline failed",
	})
	return &EngineResult{
		RunID:          runID,
		Status:         OutcomeFail,
		CompletedNodes: cp.CompletedNodes,
		Context:        pctx.Snapshot(),
	}
}

// clearDownstream uses BFS from startNode to clear the completed status of all
// reachable nodes. This is necessary when a retry loop jumps back to a prior
// node — all downstream nodes must re-execute on the next pass.
func (e *Engine) clearDownstream(startNode string, cp *Checkpoint) {
	visited := make(map[string]bool)
	queue := []string{startNode}
	visited[startNode] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		cp.ClearCompleted(current)

		for _, edge := range e.graph.OutgoingEdges(current) {
			if !visited[edge.To] {
				visited[edge.To] = true
				queue = append(queue, edge.To)
			}
		}
	}
}

// downstreamNodes returns all node IDs reachable from startNodeID via outgoing
// edges, NOT including startNodeID itself.
func downstreamNodes(graph *Graph, startNodeID string) []string {
	visited := make(map[string]bool)
	visited[startNodeID] = true
	queue := []string{startNodeID}
	var result []string

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		for _, edge := range graph.OutgoingEdges(current) {
			if !visited[edge.To] {
				visited[edge.To] = true
				queue = append(queue, edge.To)
				result = append(result, edge.To)
			}
		}
	}
	return result
}

// maxRestartsAllowed returns the max restart count from graph attrs, defaulting to 5.
func (e *Engine) maxRestartsAllowed() int {
	if mr, ok := e.graph.Attrs["max_restarts"]; ok {
		if n, err := strconv.Atoi(mr); err == nil {
			return n
		}
	}
	return 5
}

// clearDownstreamRetryCounts resets retry counters for all nodes downstream
// of the given start node (inclusive). This ensures nodes get fresh retry
// budgets after a loop restart.
func (e *Engine) clearDownstreamRetryCounts(startNode string, cp *Checkpoint) {
	if cp.RetryCounts == nil {
		return
	}
	delete(cp.RetryCounts, startNode)
	for _, nodeID := range downstreamNodes(e.graph, startNode) {
		delete(cp.RetryCounts, nodeID)
	}
}

func (e *Engine) goalGateRetryTarget(cp *Checkpoint, nodeOutcomes map[string]string) (string, bool, bool) {
	for _, nodeID := range cp.CompletedNodes {
		node := e.graph.Nodes[nodeID]
		if node == nil || !isGoalGate(node) {
			continue
		}
		status := nodeOutcomes[nodeID]
		if status == OutcomeSuccess || status == "partial_success" {
			continue
		}
		for _, target := range []string{
			node.Attrs["retry_target"],
			node.Attrs["fallback_retry_target"],
			e.graph.Attrs["retry_target"],
			e.graph.Attrs["fallback_retry_target"],
		} {
			if target == "" {
				continue
			}
			if _, ok := e.graph.Nodes[target]; ok {
				return target, true, true
			}
		}
		return "", false, true
	}
	return "", false, false
}

// nodeOrDefault returns the node from the graph, or a default empty node if not found.
// Used during checkpoint resume when the node may not exist in the graph.
func (e *Engine) nodeOrDefault(nodeID string) *Node {
	if n, ok := e.graph.Nodes[nodeID]; ok {
		return n
	}
	return &Node{ID: nodeID, Attrs: map[string]string{}}
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
