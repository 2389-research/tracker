// ABOUTME: Core pipeline execution engine that traverses graphs, executes handlers, and manages control flow.
// ABOUTME: Supports edge selection (conditions, labels, weights), retries, goal gates, and checkpoint resume.
package pipeline

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
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

		// Clear per-node edge selection hints so stale values from a
		// previous node don't pollute routing for the current one.
		// This must happen before the IsCompleted skip so that resume
		// paths through selectEdge also see clean context.
		pctx.Set(ContextKeyOutcome, "")
		pctx.Set(ContextKeyPreferredLabel, "")
		pctx.Set("suggested_next_nodes", "")

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
			return nil, fmt.Errorf("handler error at node %q: %w", currentNodeID, err)
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

		// Build trace entry for this node execution. EdgeTo is filled in
		// after edge selection below; early-return paths set it directly.
		traceEntry := TraceEntry{
			Timestamp:   handlerStart,
			NodeID:      currentNodeID,
			HandlerName: execNode.Handler,
			Status:      outcome.Status,
			Duration:    handlerDuration,
		}

		switch outcome.Status {
		case OutcomeRetry:
			maxRetries := e.maxRetries(execNode)
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
			// Treat unknown status as success.
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

	// Priority 3: Suggested next nodes (handler suggests specific targets).
	if suggested, ok := pctx.Get("suggested_next_nodes"); ok && suggested != "" {
		for _, edge := range edges {
			for _, sid := range strings.Split(suggested, ",") {
				if strings.TrimSpace(sid) == edge.To {
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
		return nil, fmt.Errorf("no matching edges: all %d edges have conditions that evaluated to false", len(edges))
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
