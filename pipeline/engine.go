// ABOUTME: Core pipeline execution engine that traverses graphs, executes handlers, and manages control flow.
// ABOUTME: Supports edge selection (conditions, labels, weights), retries, goal gates, and checkpoint resume.
package pipeline

import (
	"context"
	"crypto/rand"
	"fmt"
	"sort"
	"strconv"
	"time"
)

// EngineResult holds the final outcome of a pipeline execution run.
type EngineResult struct {
	RunID          string
	Status         string
	CompletedNodes []string
	Context        map[string]string
}

// Engine executes a pipeline graph by traversing nodes, dispatching handlers,
// selecting edges, and managing retries and checkpoints.
type Engine struct {
	graph             *Graph
	registry          *HandlerRegistry
	eventHandler      PipelineEventHandler
	checkpointPath    string
	resolveStylesheet bool
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

	pctx := NewPipelineContext()
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

	// Track failed goal gates for final status determination.
	failedGoalGate := false

	for {
		// Check context cancellation at the top of each iteration.
		if err := ctx.Err(); err != nil {
			e.emit(PipelineEvent{
				Type:      EventPipelineFailed,
				Timestamp: time.Now(),
				RunID:     runID,
				Message:   "context cancelled",
				Err:       err,
			})
			return nil, fmt.Errorf("pipeline cancelled: %w", err)
		}

		node, ok := e.graph.Nodes[currentNodeID]
		if !ok {
			return nil, fmt.Errorf("node %q not found in graph", currentNodeID)
		}

		// Skip already-completed nodes on resume.
		if cp.IsCompleted(currentNodeID) {
			edges := e.graph.OutgoingEdges(currentNodeID)
			if len(edges) == 0 {
				// Completed exit node — we're done.
				break
			}
			next, err := e.selectEdge(edges, pctx)
			if err != nil {
				return nil, fmt.Errorf("select edge from completed node %q: %w", currentNodeID, err)
			}
			currentNodeID = next.To
			continue
		}

		// Apply stylesheet to node before execution.
		if stylesheet != nil {
			resolved := stylesheet.Resolve(node)
			for k, v := range resolved {
				node.Attrs[k] = v
			}
		}

		e.emit(PipelineEvent{
			Type:      EventStageStarted,
			Timestamp: time.Now(),
			RunID:     runID,
			NodeID:    currentNodeID,
			Message:   fmt.Sprintf("executing node %q", currentNodeID),
		})

		// Execute handler.
		outcome, err := e.registry.Execute(ctx, node, pctx)
		if err != nil {
			e.emit(PipelineEvent{
				Type:      EventStageFailed,
				Timestamp: time.Now(),
				RunID:     runID,
				NodeID:    currentNodeID,
				Message:   fmt.Sprintf("handler error at node %q", currentNodeID),
				Err:       err,
			})
			return nil, fmt.Errorf("handler error at node %q: %w", currentNodeID, err)
		}

		// Merge context updates from handler outcome.
		pctx.Merge(outcome.ContextUpdates)

		// Store outcome and preferred label in context for edge selection.
		if outcome.Status != "" {
			pctx.Set(ContextKeyOutcome, outcome.Status)
		}
		if outcome.PreferredLabel != "" {
			pctx.Set(ContextKeyPreferredLabel, outcome.PreferredLabel)
		}

		switch outcome.Status {
		case OutcomeRetry:
			maxRetries := e.maxRetries(node)
			if cp.RetryCount(currentNodeID) < maxRetries {
				cp.IncrementRetry(currentNodeID)
				e.emit(PipelineEvent{
					Type:      EventStageRetrying,
					Timestamp: time.Now(),
					RunID:     runID,
					NodeID:    currentNodeID,
					Message:   fmt.Sprintf("retrying node %q (attempt %d/%d)", currentNodeID, cp.RetryCount(currentNodeID), maxRetries),
				})
				// Jump to retry_target if specified, otherwise retry self.
				target := currentNodeID
				if rt, ok := node.Attrs["retry_target"]; ok {
					target = rt
				}
				cp.CurrentNode = target
				e.saveCheckpoint(cp, pctx, runID)
				currentNodeID = target
				continue
			}

			// Retries exhausted — check fallback.
			if fallback, ok := node.Attrs["fallback_retry_target"]; ok {
				cp.CurrentNode = fallback
				e.saveCheckpoint(cp, pctx, runID)
				currentNodeID = fallback
				continue
			}

			// No fallback — fail.
			e.emit(PipelineEvent{
				Type:      EventStageFailed,
				Timestamp: time.Now(),
				RunID:     runID,
				NodeID:    currentNodeID,
				Message:   fmt.Sprintf("retries exhausted for node %q", currentNodeID),
			})

			if isGoalGate(node) {
				return e.failResult(runID, cp, pctx), nil
			}
			return e.failResult(runID, cp, pctx), nil

		case OutcomeFail:
			e.emit(PipelineEvent{
				Type:      EventStageFailed,
				Timestamp: time.Now(),
				RunID:     runID,
				NodeID:    currentNodeID,
				Message:   fmt.Sprintf("node %q failed", currentNodeID),
			})

			if isGoalGate(node) {
				failedGoalGate = true
				return e.failResult(runID, cp, pctx), nil
			}

			// Non-goal-gate failure: mark completed and continue.
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
			// Treat unknown status as success.
			cp.MarkCompleted(currentNodeID)
		}

		// Check if this is an exit node — pipeline complete.
		if currentNodeID == e.graph.ExitNode {
			break
		}

		// Select next edge.
		edges := e.graph.OutgoingEdges(currentNodeID)
		if len(edges) == 0 {
			return nil, fmt.Errorf("no outgoing edges from non-exit node %q", currentNodeID)
		}

		next, err := e.selectEdge(edges, pctx)
		if err != nil {
			return nil, fmt.Errorf("select edge from %q: %w", currentNodeID, err)
		}

		currentNodeID = next.To
		cp.CurrentNode = currentNodeID
		e.saveCheckpoint(cp, pctx, runID)
	}

	if failedGoalGate {
		return e.failResult(runID, cp, pctx), nil
	}

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
	}, nil
}

// selectEdge picks the best outgoing edge using priority: condition > preferred label > weight > lexical.
func (e *Engine) selectEdge(edges []*Edge, pctx *PipelineContext) (*Edge, error) {
	// Priority 1: Condition match.
	for _, edge := range edges {
		if edge.Condition != "" {
			match, err := EvaluateCondition(edge.Condition, pctx)
			if err != nil {
				return nil, fmt.Errorf("evaluate condition on edge %s->%s: %w", edge.From, edge.To, err)
			}
			if match {
				return edge, nil
			}
		}
	}

	// Priority 2: Preferred label match (from outcome or context).
	if preferred, ok := pctx.Get(ContextKeyPreferredLabel); ok && preferred != "" {
		for _, edge := range edges {
			if edge.Label == preferred {
				return edge, nil
			}
		}
	}

	// Priority 3: Edge weight (higher wins).
	// Filter to edges without conditions (unconditional edges).
	var unconditional []*Edge
	for _, edge := range edges {
		if edge.Condition == "" {
			unconditional = append(unconditional, edge)
		}
	}
	if len(unconditional) == 0 {
		unconditional = edges
	}

	sort.SliceStable(unconditional, func(i, j int) bool {
		wi := edgeWeight(unconditional[i])
		wj := edgeWeight(unconditional[j])
		if wi != wj {
			return wi > wj
		}
		// Priority 4: Lexical ordering by To field.
		return unconditional[i].To < unconditional[j].To
	})

	return unconditional[0], nil
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
func (e *Engine) loadOrCreateCheckpoint(runID string) (*Checkpoint, error) {
	if e.checkpointPath != "" {
		cp, err := LoadCheckpoint(e.checkpointPath)
		if err == nil {
			return cp, nil
		}
		// File doesn't exist yet — that's fine, create new.
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
			Type:      EventCheckpointSaved,
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

// generateRunID creates a random 6-byte hex run identifier.
func generateRunID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return fmt.Sprintf("%x", b)
}
