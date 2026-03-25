// ABOUTME: Builds the ordered node list for TUI display from the pipeline graph.
// ABOUTME: Uses Kahn's topological sort algorithm with BFS tie-breaking.
package main

import (
	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/tui"
)

// buildNodeList creates an ordered list of node ID/label pairs from the
// pipeline graph in topological (execution) order. Start is first, Done is
// last, everything in between is ordered by when it would be reached during
// execution. Uses Kahn's algorithm with BFS tie-breaking from StartNode.
func buildNodeList(graph *pipeline.Graph) []tui.NodeEntry {
	if graph.StartNode == "" {
		return nil
	}

	order := topoSortNodes(graph)

	// Build entries, ensuring exit node is always last.
	var entries []tui.NodeEntry
	var exitEntry *tui.NodeEntry
	for _, nodeID := range order {
		node, ok := graph.Nodes[nodeID]
		if !ok {
			continue
		}
		label := node.Label
		if label == "" {
			label = node.ID
		}
		entry := tui.NodeEntry{ID: node.ID, Label: label}
		if nodeID == graph.ExitNode {
			exitEntry = &entry
			continue
		}
		entries = append(entries, entry)
	}
	if exitEntry != nil {
		entries = append(entries, *exitEntry)
	}

	return entries
}

// topoSortNodes returns node IDs in topological order using Kahn's algorithm.
// Restart back-edges are excluded so the sort follows forward flow only.
func topoSortNodes(graph *pipeline.Graph) []string {
	inDegree := buildForwardInDegree(graph)
	queue := seedQueue(graph, inDegree)

	var order []string
	visited := make(map[string]bool)

	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]

		if visited[nodeID] {
			continue
		}
		visited[nodeID] = true
		order = append(order, nodeID)

		queue = enqueueSuccessors(graph, nodeID, inDegree, visited, queue)
	}

	// Add any nodes not reached by topo sort (shouldn't happen but be safe).
	for id := range graph.Nodes {
		if !visited[id] {
			order = append(order, id)
		}
	}

	return order
}

// buildForwardInDegree computes in-degree for each node, counting only
// non-restart edges (forward flow).
func buildForwardInDegree(graph *pipeline.Graph) map[string]int {
	inDegree := make(map[string]int)
	for id := range graph.Nodes {
		inDegree[id] = 0
	}
	for _, e := range graph.Edges {
		if e.Attrs == nil || e.Attrs["restart"] != "true" {
			inDegree[e.To]++
		}
	}
	return inDegree
}

// seedQueue returns the initial BFS queue: StartNode first (if zero in-degree),
// then all other zero in-degree nodes for deterministic ordering.
func seedQueue(graph *pipeline.Graph, inDegree map[string]int) []string {
	var queue []string
	if inDegree[graph.StartNode] == 0 {
		queue = append(queue, graph.StartNode)
	}
	for id := range graph.Nodes {
		if inDegree[id] == 0 && id != graph.StartNode {
			queue = append(queue, id)
		}
	}
	return queue
}

// enqueueSuccessors decrements in-degree for successors of nodeID (skipping
// restart back-edges) and appends newly-ready nodes to the queue.
func enqueueSuccessors(graph *pipeline.Graph, nodeID string, inDegree map[string]int, visited map[string]bool, queue []string) []string {
	for _, edge := range graph.OutgoingEdges(nodeID) {
		if edge.Attrs != nil && edge.Attrs["restart"] == "true" {
			continue
		}
		inDegree[edge.To]--
		if inDegree[edge.To] <= 0 && !visited[edge.To] {
			queue = append(queue, edge.To)
		}
	}
	return queue
}
