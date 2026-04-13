// ABOUTME: Simulate subcommand — dry-runs a pipeline (.dot or .dip) without LLM calls.
// ABOUTME: Shows execution plan: node order, handlers, edges, conditions, and graph attributes.
package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/2389-research/tracker/pipeline"
)

// runSimulateCmd parses a pipeline file and prints the execution plan without running anything.
// Auto-detects format based on file extension unless formatOverride is set.
func runSimulateCmd(pipelineFile, formatOverride string, w io.Writer) error {
	resolved, isEmbedded, info, err := resolvePipelineSource(pipelineFile)
	if err != nil {
		return err
	}

	var graph *pipeline.Graph
	if isEmbedded {
		graph, err = loadEmbeddedPipeline(info)
		pipelineFile = info.Name
	} else {
		graph, err = loadPipeline(resolved, formatOverride)
		pipelineFile = resolved
	}
	if err != nil {
		return fmt.Errorf("load pipeline: %w", err)
	}

	if validationErr := pipeline.ValidateAll(graph); validationErr != nil && len(validationErr.Errors) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "=== Validation Warnings ===")
		for _, e := range validationErr.Errors {
			fmt.Fprintf(w, "  ! %s\n", e)
		}
	}

	printSimHeader(w, graph, pipelineFile)
	printSimNodes(w, graph)
	printSimEdges(w, graph)
	printSimExecutionPlan(w, graph)
	printSimFooter(w)

	return nil
}

func printSimHeader(w io.Writer, graph *pipeline.Graph, dotFile string) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "\u2550\u2550\u2550 Pipeline Simulation \u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550")
	name := graph.Name
	if name == "" {
		name = dotFile
	}
	fmt.Fprintf(w, "  Pipeline:  %s\n", name)
	fmt.Fprintf(w, "  Nodes:     %d\n", len(graph.Nodes))
	fmt.Fprintf(w, "  Edges:     %d\n", len(graph.Edges))
	fmt.Fprintf(w, "  Start:     %s\n", graph.StartNode)
	fmt.Fprintf(w, "  Exit:      %s\n", graph.ExitNode)

	if len(graph.Attrs) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  Graph Attributes:")
		for k, v := range graph.Attrs {
			fmt.Fprintf(w, "    %s = %s\n", k, v)
		}
	}
}

func printSimNodes(w io.Writer, graph *pipeline.Graph) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "\u2500\u2500\u2500 Nodes \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")
	fmt.Fprintf(w, "  %-20s  %-15s  %-12s  %s\n", "ID", "Handler", "Shape", "Label")
	fmt.Fprintf(w, "  %-20s  %-15s  %-12s  %s\n", "\u2500\u2500", "\u2500\u2500\u2500\u2500\u2500\u2500\u2500", "\u2500\u2500\u2500\u2500\u2500", "\u2500\u2500\u2500\u2500\u2500")

	ordered := bfsNodeOrder(graph)
	for _, node := range ordered {
		printSimNodeRow(w, node)
	}
}

// bfsNodeOrder returns graph nodes in BFS order from start, with orphaned nodes appended.
func bfsNodeOrder(graph *pipeline.Graph) []*pipeline.Node {
	visited := make(map[string]bool)
	ordered := bfsTraversal(graph, visited)
	return appendOrphanedNodes(graph, visited, ordered)
}

// bfsTraversal walks the graph from StartNode in BFS order, marking visited nodes.
func bfsTraversal(graph *pipeline.Graph, visited map[string]bool) []*pipeline.Node {
	queue := []string{graph.StartNode}
	var ordered []*pipeline.Node

	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		if visited[nodeID] {
			continue
		}
		visited[nodeID] = true

		node, ok := graph.Nodes[nodeID]
		if !ok {
			continue
		}
		ordered = append(ordered, node)
		queue = enqueueUnvisited(queue, graph.OutgoingEdges(nodeID), visited)
	}
	return ordered
}

// enqueueUnvisited appends unvisited edge targets to the queue.
func enqueueUnvisited(queue []string, edges []*pipeline.Edge, visited map[string]bool) []string {
	for _, edge := range edges {
		if !visited[edge.To] {
			queue = append(queue, edge.To)
		}
	}
	return queue
}

// appendOrphanedNodes appends nodes not reachable from the start node.
func appendOrphanedNodes(graph *pipeline.Graph, visited map[string]bool, ordered []*pipeline.Node) []*pipeline.Node {
	for _, node := range graph.Nodes {
		if !visited[node.ID] {
			ordered = append(ordered, node)
		}
	}
	return ordered
}

// printSimNodeRow prints one node's summary row and its key attributes.
func printSimNodeRow(w io.Writer, node *pipeline.Node) {
	label := node.Label
	if label == node.ID {
		label = ""
	}
	id := node.ID
	if len(id) > 20 {
		id = id[:17] + "..."
	}
	fmt.Fprintf(w, "  %-20s  %-15s  %-12s  %s\n", id, node.Handler, node.Shape, label)

	for _, key := range []string{"llm_model", "llm_provider", "retry_policy", "max_retries", "fidelity", "prompt"} {
		if v, ok := node.Attrs[key]; ok {
			fmt.Fprintf(w, "  %-20s  > %s=%s\n", "", key, v)
		}
	}
}

func printSimEdges(w io.Writer, graph *pipeline.Graph) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "\u2500\u2500\u2500 Edges \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")

	for _, edge := range graph.Edges {
		label := ""
		if edge.Label != "" {
			label = fmt.Sprintf("  [%s]", edge.Label)
		}
		condition := ""
		if edge.Condition != "" {
			condition = fmt.Sprintf("  (when: %s)", edge.Condition)
		}
		fmt.Fprintf(w, "  %s -> %s%s%s\n", edge.From, edge.To, label, condition)
	}
}

func printSimExecutionPlan(w io.Writer, graph *pipeline.Graph) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "\u2500\u2500\u2500 Execution Plan \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")
	fmt.Fprintln(w, "  (simulated BFS traversal from start node)")
	fmt.Fprintln(w)

	if graph.StartNode == "" {
		fmt.Fprintln(w, "  ! No start node found")
		return
	}

	visited := make(map[string]bool)
	queue := []string{graph.StartNode}
	step := 0

	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		if visited[nodeID] {
			continue
		}
		visited[nodeID] = true
		step++

		node, ok := graph.Nodes[nodeID]
		if !ok {
			continue
		}

		printSimPlanStep(w, node, step)
		queue = printSimPlanEdges(w, graph, nodeID, visited, queue)
	}

	printSimUnreachable(w, graph, visited)
}

// printSimPlanStep prints a single step in the execution plan.
func printSimPlanStep(w io.Writer, node *pipeline.Node, step int) {
	label := node.Label
	if label == "" || label == node.ID {
		label = node.ID
	}
	fmt.Fprintf(w, "  %2d. %s  (%s)\n", step, label, node.Handler)
}

// printSimPlanEdges prints outgoing edges for a node and enqueues unvisited targets.
func printSimPlanEdges(w io.Writer, graph *pipeline.Graph, nodeID string, visited map[string]bool, queue []string) []string {
	edges := graph.OutgoingEdges(nodeID)
	for _, edge := range edges {
		extra := formatEdgeAnnotation(*edge)
		fmt.Fprintf(w, "      \u2514\u2500> %s%s\n", edge.To, extra)
		if !visited[edge.To] {
			queue = append(queue, edge.To)
		}
	}

	if len(edges) == 0 && nodeID != graph.ExitNode {
		fmt.Fprintln(w, "      ! dead end (no outgoing edges)")
	}
	return queue
}

// formatEdgeAnnotation returns label and condition annotations for an edge.
func formatEdgeAnnotation(edge pipeline.Edge) string {
	extra := ""
	if edge.Label != "" {
		extra = fmt.Sprintf(" [%s]", edge.Label)
	}
	if edge.Condition != "" {
		extra += fmt.Sprintf(" (when: %s)", edge.Condition)
	}
	return extra
}

// printSimUnreachable reports any nodes not visited during BFS.
func printSimUnreachable(w io.Writer, graph *pipeline.Graph, visited map[string]bool) {
	var unreachable []string
	for id := range graph.Nodes {
		if !visited[id] {
			unreachable = append(unreachable, id)
		}
	}
	if len(unreachable) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  ! Unreachable nodes: %s\n", strings.Join(unreachable, ", "))
	}
}

func printSimFooter(w io.Writer) {
	fmt.Fprintln(w, "\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550")
}
