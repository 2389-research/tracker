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

// runState holds per-run mutable state threaded through the main loop.
type runState struct {
	runID        string
	pctx         *PipelineContext
	cp           *Checkpoint
	trace        *Trace
	nodeOutcomes map[string]string
	stylesheet   *Stylesheet
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

	return &runState{
		runID:        runID,
		pctx:         pctx,
		cp:           cp,
		trace:        &Trace{RunID: runID, StartTime: time.Now()},
		nodeOutcomes: make(map[string]string),
		stylesheet:   stylesheet,
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

	fidelity := ResolveFidelity(e.nodeOrDefault(cp.CurrentNode), e.graph.Attrs)
	degraded := DegradeFidelity(fidelity)
	compacted := CompactContext(pctx, cp.CompletedNodes, degraded, e.artifactDir, runID)

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

	next, err := e.selectEdge(edges, s.pctx)
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
	outcome, err := e.registry.Execute(ctx, execNode, s.pctx)
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
	return &outcome, traceEntry, nil
}

// applyOutcome merges handler outcome into pipeline context and emits the decision_outcome event.
func (e *Engine) applyOutcome(s *runState, currentNodeID string, outcome *Outcome) {
	s.pctx.Merge(outcome.ContextUpdates)

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
}

// handleRetry processes a retry outcome. Returns (nextNodeID, shouldContinue, result, error).
// If shouldContinue is true, the main loop should continue with nextNodeID.
// If result is non-nil, the pipeline should return that result.
func (e *Engine) handleRetry(ctx context.Context, s *runState, currentNodeID string, execNode *Node, traceEntry *TraceEntry) (string, bool, *EngineResult, error) {
	policy := ResolveRetryPolicy(execNode, e.graph.Attrs)
	if s.cp.RetryCount(currentNodeID) < policy.MaxRetries {
		s.cp.IncrementRetry(currentNodeID)

		backoff := policy.BackoffFn(s.cp.RetryCount(currentNodeID)-1, policy.BaseDelay)
		if backoff > 0 {
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				e.saveCheckpoint(s.cp, s.pctx, s.runID)
				s.trace.EndTime = time.Now()
				return "", false, &EngineResult{
					RunID:          s.runID,
					Status:         OutcomeFail,
					CompletedNodes: s.cp.CompletedNodes,
					Context:        s.pctx.Snapshot(),
					Trace:          s.trace,
					Usage:          s.trace.AggregateUsage(),
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
		e.clearDownstream(target, s.cp)
		s.cp.CurrentNode = target
		e.saveCheckpoint(s.cp, s.pctx, s.runID)
		return target, true, nil, nil
	}

	// Retries exhausted — check fallback.
	if fallback, ok := execNode.Attrs["fallback_retry_target"]; ok {
		traceEntry.EdgeTo = fallback
		s.trace.AddEntry(*traceEntry)
		e.clearDownstream(fallback, s.cp)
		s.cp.CurrentNode = fallback
		e.saveCheckpoint(s.cp, s.pctx, s.runID)
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
	s.trace.EndTime = time.Now()
	result := e.failResult(s.runID, s.cp, s.pctx, s.trace)
	return "", false, result, nil
}

// handleOutcomeStatus emits events and marks completion for non-retry outcomes.
func (e *Engine) handleOutcomeStatus(s *runState, currentNodeID string, status string) {
	switch status {
	case OutcomeFail:
		e.emit(PipelineEvent{
			Type:      EventStageFailed,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    currentNodeID,
			Message:   fmt.Sprintf("node %q failed", currentNodeID),
		})
		s.cp.MarkCompleted(currentNodeID)

	case OutcomeSuccess:
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
			Type:      EventWarning,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    currentNodeID,
			Message:   fmt.Sprintf("unknown outcome status %q from node %q; treating as success", status, currentNodeID),
		})
		s.cp.MarkCompleted(currentNodeID)
	}
}

// handleExitNode processes the exit node. Returns (shouldBreak, result, error).
// If shouldBreak is true, the main loop should break (success).
// If result is non-nil, return early with that result.
// If neither, a retry target was found and currentNodeID should be updated by the caller.
func (e *Engine) handleExitNode(s *runState, currentNodeID string, outcomeStatus string, traceEntry *TraceEntry) (bool, string, *EngineResult) {
	target, gateNodeID, retry, unsatisfied := e.goalGateRetryTarget(s.cp, s.nodeOutcomes)
	if retry {
		s.cp.IncrementRetry(gateNodeID)
		gateNode := e.nodeOrDefault(gateNodeID)
		e.emit(PipelineEvent{
			Type:      EventStageRetrying,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    gateNodeID,
			Message: fmt.Sprintf("goal-gate retry for %q → %q (attempt %d/%d)",
				gateNodeID, target,
				s.cp.RetryCount(gateNodeID), e.maxRetries(gateNode)),
		})
		traceEntry.EdgeTo = target
		s.trace.AddEntry(*traceEntry)
		e.clearDownstream(target, s.cp)
		s.cp.CurrentNode = target
		e.saveCheckpoint(s.cp, s.pctx, s.runID)
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
		e.clearDownstream(target, s.cp)
		s.cp.CurrentNode = target
		e.saveCheckpoint(s.cp, s.pctx, s.runID)
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
		s.trace.EndTime = time.Now()
		result := e.failResult(s.runID, s.cp, s.pctx, s.trace)
		return false, "", result
	}
	if outcomeStatus == OutcomeFail {
		s.trace.AddEntry(*traceEntry)
		s.trace.EndTime = time.Now()
		result := e.failResult(s.runID, s.cp, s.pctx, s.trace)
		return false, "", result
	}
	s.trace.AddEntry(*traceEntry)
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
			RunID:          s.runID,
			Status:         OutcomeFail,
			CompletedNodes: s.cp.CompletedNodes,
			Context:        s.pctx.Snapshot(),
			Trace:          s.trace,
			Usage:          s.trace.AggregateUsage(),
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
