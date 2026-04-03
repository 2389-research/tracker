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
// cp.RetryCounts[goalGateNodeID] and e.maxRetries(node). When retries are
// exhausted, it looks for a fallback_target to route to instead.
// Returns (target, goalGateNodeID, shouldRetry, unsatisfied).
func (e *Engine) goalGateRetryTarget(cp *Checkpoint, nodeOutcomes map[string]string) (string, string, bool, bool) {
	for _, nodeID := range cp.CompletedNodes {
		node := e.graph.Nodes[nodeID]
		if node == nil || !isGoalGate(node) {
			continue
		}
		status := nodeOutcomes[nodeID]
		if status == OutcomeSuccess || status == "partial_success" {
			continue
		}

		// Check retry budget before allowing another loop.
		maxR := e.maxRetries(node)
		if cp.RetryCount(nodeID) >= maxR {
			// Retries exhausted — look for a fallback/escalation target.
			// Guard: only take the fallback once per gate to prevent infinite loops.
			if cp.FallbackTaken[nodeID] {
				// Fallback already taken — signal unsatisfied without retry.
				return "", nodeID, false, true
			}
			for _, fb := range []string{
				node.Attrs["fallback_target"],
				node.Attrs["fallback_retry_target"],
				e.graph.Attrs["fallback_target"],
				e.graph.Attrs["fallback_retry_target"],
			} {
				if fb == "" {
					continue
				}
				if _, ok := e.graph.Nodes[fb]; ok {
					// Mark fallback as taken so it won't loop.
					if cp.FallbackTaken == nil {
						cp.FallbackTaken = map[string]bool{}
					}
					cp.FallbackTaken[nodeID] = true
					return fb, nodeID, false, true
				}
			}
			// No fallback — signal unsatisfied without retry.
			return "", nodeID, false, true
		}

		// Retries remain — find a retry target.
		for _, target := range []string{
			node.Attrs["retry_target"],
			node.Attrs["fallback_target"],
			node.Attrs["fallback_retry_target"],
			e.graph.Attrs["retry_target"],
			e.graph.Attrs["fallback_target"],
			e.graph.Attrs["fallback_retry_target"],
		} {
			if target == "" {
				continue
			}
			if _, ok := e.graph.Nodes[target]; ok {
				return target, nodeID, true, true
			}
		}
		return "", nodeID, false, true
	}
	return "", "", false, false
}

// nodeOrDefault returns the node from the graph, or a default empty node if not found.
func (e *Engine) nodeOrDefault(nodeID string) *Node {
	if n, ok := e.graph.Nodes[nodeID]; ok {
		return n
	}
	return &Node{ID: nodeID, Attrs: map[string]string{}}
}
