// ABOUTME: Checkpoint and graph utility methods extracted from engine.go.
// ABOUTME: Handles checkpoint load/save, downstream clearing, goal gates, and restart limits.
package pipeline

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// loadOrCreateCheckpoint loads an existing checkpoint or creates a fresh one.
// Returns an error if the checkpoint file exists but is corrupt.
func (e *Engine) loadOrCreateCheckpoint(runID string) (*Checkpoint, error) {
	if e.checkpointPath != "" {
		cp, err := LoadCheckpoint(e.checkpointPath)
		if err == nil {
			return cp, nil
		}
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

// maxRestartsAllowed returns the max restart count from graph attrs, defaulting to 5.
func (e *Engine) maxRestartsAllowed() int {
	if mr, ok := e.graph.Attrs["max_restarts"]; ok {
		if n, err := strconv.Atoi(mr); err == nil {
			return n
		}
	}
	return 5
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

// goalGateRetryTarget checks if any completed goal gate node is unsatisfied and
// returns a retry target if available. The retry budget is checked against
// cp.RetryCounts[goalGateNodeID] and e.maxRetries(node).
//
// While retries remain, candidates are checked in order: retry_target,
// fallback_target, fallback_retry_target (node-level then graph-level).
// This means fallback_target can serve as a retry destination when no
// retry_target is configured. When retries are exhausted, fallback_target
// and fallback_retry_target are used for one-shot escalation routing
// (guarded by cp.FallbackTaken to prevent infinite loops).
//
// Returns (target, goalGateNodeID, shouldRetry, unsatisfied).
func (e *Engine) goalGateRetryTarget(cp *Checkpoint, nodeOutcomes map[string]string) (string, string, bool, bool) {
	for _, nodeID := range cp.CompletedNodes {
		if result, found := e.checkGoalGateNode(cp, nodeID, nodeOutcomes); found {
			return result.target, result.nodeID, result.shouldRetry, result.unsatisfied
		}
	}
	return "", "", false, false
}

// goalGateCheckResult holds the output of checkGoalGateNode.
type goalGateCheckResult struct {
	target      string
	nodeID      string
	shouldRetry bool
	unsatisfied bool
}

// checkGoalGateNode evaluates a single completed node as a potential goal gate retry point.
// Returns (result, true) if this node is an unsatisfied goal gate, (zero, false) otherwise.
func (e *Engine) checkGoalGateNode(cp *Checkpoint, nodeID string, nodeOutcomes map[string]string) (goalGateCheckResult, bool) {
	node := e.graph.Nodes[nodeID]
	if node == nil || !isGoalGate(node) {
		return goalGateCheckResult{}, false
	}
	status := nodeOutcomes[nodeID]
	if status == OutcomeSuccess || status == "partial_success" {
		return goalGateCheckResult{}, false
	}

	var t, n string
	var retry, unsat bool
	if cp.RetryCount(nodeID) >= e.maxRetries(node) {
		t, n, retry, unsat = e.goalGateExhaustedPath(cp, node, nodeID)
	} else {
		t, n, retry, unsat = e.goalGateRemainingPath(node, nodeID)
	}
	return goalGateCheckResult{target: t, nodeID: n, shouldRetry: retry, unsatisfied: unsat}, true
}

// goalGateExhaustedPath handles the retry-budget-exhausted case for a goal gate.
// Returns (target, nodeID, shouldRetry=false, unsatisfied=true).
func (e *Engine) goalGateExhaustedPath(cp *Checkpoint, node *Node, nodeID string) (string, string, bool, bool) {
	// Guard: only take the fallback once per gate to prevent infinite loops.
	if cp.FallbackTaken[nodeID] {
		return "", nodeID, false, true
	}
	fb := e.findFallbackTarget(node)
	if fb == "" {
		return "", nodeID, false, true
	}
	if cp.FallbackTaken == nil {
		cp.FallbackTaken = map[string]bool{}
	}
	cp.FallbackTaken[nodeID] = true
	return fb, nodeID, false, true
}

// findFallbackTarget returns the first valid fallback node ID from node and graph attrs.
func (e *Engine) findFallbackTarget(node *Node) string {
	candidates := []string{
		node.Attrs["fallback_target"],
		node.Attrs["fallback_retry_target"],
		e.graph.Attrs["fallback_target"],
		e.graph.Attrs["fallback_retry_target"],
	}
	for _, fb := range candidates {
		if fb != "" {
			if _, ok := e.graph.Nodes[fb]; ok {
				return fb
			}
		}
	}
	return ""
}

// goalGateRemainingPath handles the retries-still-available case for a goal gate.
// Returns (target, nodeID, shouldRetry=true, unsatisfied=true) when a target is found,
// or (empty, nodeID, false, true) if no valid retry target exists.
func (e *Engine) goalGateRemainingPath(node *Node, nodeID string) (string, string, bool, bool) {
	retryTargets := []string{
		node.Attrs["retry_target"],
		node.Attrs["fallback_target"],
		node.Attrs["fallback_retry_target"],
		e.graph.Attrs["retry_target"],
		e.graph.Attrs["fallback_target"],
		e.graph.Attrs["fallback_retry_target"],
	}
	for _, target := range retryTargets {
		if target == "" {
			continue
		}
		if _, ok := e.graph.Nodes[target]; ok {
			return target, nodeID, true, true
		}
	}
	return "", nodeID, false, true
}

// nodeOrDefault returns the node from the graph, or a default empty node if not found.
func (e *Engine) nodeOrDefault(nodeID string) *Node {
	if n, ok := e.graph.Nodes[nodeID]; ok {
		return n
	}
	return &Node{ID: nodeID, Attrs: map[string]string{}}
}
