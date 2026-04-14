// ABOUTME: Core pipeline execution engine that traverses graphs, executes handlers, and manages control flow.
// ABOUTME: Supports edge selection (conditions, labels, weights), retries, goal gates, and checkpoint resume.
package pipeline

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"time"
)

// EngineResult holds the final outcome of a pipeline execution run.
type EngineResult struct {
	RunID          string
	Status         string
	CompletedNodes []string
	Context        map[string]string
	Trace          *Trace
	Usage          *UsageSummary
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

	return &EngineResult{
		RunID:          s.runID,
		Status:         OutcomeSuccess,
		CompletedNodes: s.cp.CompletedNodes,
		Context:        s.pctx.Snapshot(),
		Trace:          s.trace,
		Usage:          s.trace.AggregateUsage(),
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

	execNode := e.prepareExecNode(node, s)

	outcome, traceEntry, err := e.executeNode(ctx, s, currentNodeID, execNode)
	if err != nil {
		e.saveCheckpoint(s.cp, s.pctx, s.runID)
		s.trace.EndTime = time.Now()
		return loopResult{
			action: loopReturn,
			result: &EngineResult{
				RunID:          s.runID,
				Status:         OutcomeFail,
				CompletedNodes: s.cp.CompletedNodes,
				Context:        s.pctx.Snapshot(),
				Trace:          s.trace,
				Usage:          s.trace.AggregateUsage(),
			},
			err: fmt.Errorf("handler error at node %q: %w", currentNodeID, err),
		}
	}

	e.applyOutcome(s, currentNodeID, outcome)

	if outcome.Status == OutcomeRetry {
		return e.processRetryOutcome(ctx, s, currentNodeID, execNode, &traceEntry)
	}

	e.handleOutcomeStatus(s, currentNodeID, outcome.Status)

	if currentNodeID == e.graph.ExitNode {
		return e.processExitNode(s, currentNodeID, outcome.Status, &traceEntry)
	}

	return e.advanceToNextNode(s, currentNodeID, &traceEntry)
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

	next, err := e.selectEdge(edges, s.pctx)
	if err != nil {
		s.trace.AddEntry(*traceEntry)
		return loopResult{action: loopReturn, err: fmt.Errorf("select edge from %q: %w", currentNodeID, err)}
	}

	traceEntry.EdgeTo = next.To
	s.trace.AddEntry(*traceEntry)
	e.emitCostUpdate(s)
	s.cp.SetEdgeSelection(currentNodeID, next.To)

	if s.cp.IsCompleted(next.To) {
		return e.handleCompletedTarget(s, next.To, traceEntry)
	}

	s.cp.CurrentNode = next.To
	e.saveCheckpoint(s.cp, s.pctx, s.runID)
	return loopResult{action: loopContinue, nextNodeID: next.To}
}

// checkStrictFailure enforces strict failure mode: a failed node with only
// unconditional outgoing edges stops the pipeline.
func (e *Engine) checkStrictFailure(s *runState, nodeID string, traceEntry *TraceEntry, edges []*Edge) *loopResult {
	outcome, _ := s.pctx.Get(ContextKeyOutcome)
	if outcome != OutcomeFail || hasAnyConditionalEdge(edges) {
		return nil
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
			RunID:          s.runID,
			Status:         OutcomeFail,
			CompletedNodes: s.cp.CompletedNodes,
			Context:        s.pctx.Snapshot(),
			Trace:          s.trace,
			Usage:          s.trace.AggregateUsage(),
		},
		err: fmt.Errorf("node %q failed with no conditional edges to handle failure", nodeID),
	}
	return &lr
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
		RunID:          s.runID,
		Status:         OutcomeFail,
		CompletedNodes: s.cp.CompletedNodes,
		Context:        s.pctx.Snapshot(),
		Trace:          s.trace,
		Usage:          s.trace.AggregateUsage(),
	}, fmt.Errorf("pipeline cancelled: %w", err)
}

// emit sends a pipeline event to the configured handler.
func (e *Engine) emit(evt PipelineEvent) {
	e.eventHandler.HandlePipelineEvent(evt)
}

// failResult builds an EngineResult with fail status.
func (e *Engine) failResult(runID string, cp *Checkpoint, pctx *PipelineContext, trace *Trace) *EngineResult {
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
		Trace:          trace,
		Usage:          trace.AggregateUsage(),
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
