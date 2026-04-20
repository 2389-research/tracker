// ABOUTME: Builds the ordered node list for TUI display from the pipeline graph.
// ABOUTME: Uses Kahn's topological sort algorithm with BFS tie-breaking.
package main

import (
	"strings"

	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/tui"
)

// buildNodeList creates an ordered list of node ID/label pairs from the
// pipeline graph in topological (execution) order. Start is first, Done is
// last, everything in between is ordered by when it would be reached during
// execution. Uses Kahn's algorithm with BFS tie-breaking from StartNode.
// Subgraph children are inserted recursively after their parent entries.
func buildNodeList(graph *pipeline.Graph, subgraphs map[string]*pipeline.Graph) []tui.NodeEntry {
	return buildNodeListVisited(graph, subgraphs, make(map[string]bool))
}

// buildNodeListVisited is the recursive core — visited tracks subgraph_ref
// values already being expanded so a cyclic map (A→B→A) cannot recurse to
// stack overflow during TUI init. Cycles are normally rejected upstream by
// cmd/tracker/loading.go:loadSubgraphsRecursive (keyed by absolute file
// path), but a library caller could pass a programmatically constructed
// map that bypasses that check. Defense in depth.
func buildNodeListVisited(graph *pipeline.Graph, subgraphs map[string]*pipeline.Graph, visited map[string]bool) []tui.NodeEntry {
	if graph.StartNode == "" {
		return nil
	}

	order := topoSortNodes(graph)
	entries := buildOrderedEntries(order, graph)
	return expandSubgraphChildren(entries, graph, subgraphs, visited)
}

// expandSubgraphChildren inserts child subgraph nodes directly after each
// subgraph parent entry, recursively handling nested subgraphs. Each ref
// is added to visited before recursing and removed after, so sibling
// subgraphs sharing a ref are still expanded but a cycle is broken.
func expandSubgraphChildren(entries []tui.NodeEntry, graph *pipeline.Graph, subgraphs map[string]*pipeline.Graph, visited map[string]bool) []tui.NodeEntry {
	if len(subgraphs) == 0 {
		return entries
	}

	var out []tui.NodeEntry
	for _, entry := range entries {
		out = append(out, entry)

		node, ok := graph.Nodes[entry.ID]
		if !ok || node.Handler != "subgraph" {
			continue
		}

		ref := node.Attrs["subgraph_ref"]
		if ref == "" || visited[ref] {
			continue // missing ref or cycle — skip expansion
		}
		childGraph, ok := subgraphs[ref]
		if !ok {
			continue
		}

		visited[ref] = true
		childEntries := buildNodeListVisited(childGraph, subgraphs, visited)
		delete(visited, ref)

		for _, child := range childEntries {
			child.ID = entry.ID + "/" + child.ID
			child.Label = tui.SubgraphChildLabel(child.ID)
			out = append(out, child)
		}
	}
	return out
}

// buildOrderedEntries converts a topological order into NodeEntry slice with exit node last.
func buildOrderedEntries(order []string, graph *pipeline.Graph) []tui.NodeEntry {
	var entries []tui.NodeEntry
	var exitEntry *tui.NodeEntry
	for _, nodeID := range order {
		node, ok := graph.Nodes[nodeID]
		if !ok {
			continue
		}
		entry := nodeEntryFor(node, graph)
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

// nodeEntryFor builds a NodeEntry for a single graph node.
func nodeEntryFor(node *pipeline.Node, graph *pipeline.Graph) tui.NodeEntry {
	label := node.Label
	if label == "" {
		label = node.ID
	}
	return tui.NodeEntry{ID: node.ID, Label: label, Flags: nodeFlags(node, graph)}
}

// nodeFlags determines the parallel execution role of a node from its attrs.
func nodeFlags(node *pipeline.Node, graph *pipeline.Graph) tui.NodeFlags {
	flags := tui.NodeFlags{}
	if _, ok := node.Attrs["parallel_targets"]; ok {
		flags.IsParallelDispatcher = true
	}
	if _, ok := node.Attrs["fan_in_sources"]; ok {
		flags.IsFanIn = true
	}
	if isParallelBranchNode(node.ID, graph) {
		flags.IsParallelBranch = true
	}
	return flags
}

// isParallelBranchNode returns true if any node in the graph lists nodeID as a parallel target.
func isParallelBranchNode(nodeID string, graph *pipeline.Graph) bool {
	for _, other := range graph.Nodes {
		if isTargetOf(nodeID, other.Attrs["parallel_targets"]) {
			return true
		}
	}
	return false
}

// isTargetOf returns true if nodeID appears in a comma-separated targets string.
func isTargetOf(nodeID, targets string) bool {
	for _, t := range splitTargets(targets) {
		if t == nodeID {
			return true
		}
	}
	return false
}

// splitTargets splits a comma-separated list of target node IDs.
func splitTargets(s string) []string {
	var result []string
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			result = append(result, t)
		}
	}
	return result
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
