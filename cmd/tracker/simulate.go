// ABOUTME: Simulate subcommand — dry-runs a DOT pipeline without LLM calls.
// ABOUTME: Shows execution plan: node order, handlers, edges, conditions, and graph attributes.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/2389-research/tracker/pipeline"
)

// runSimulate parses a DOT file and prints the execution plan without running anything.
func runSimulate(dotFile string, w io.Writer) error {
	dotBytes, err := os.ReadFile(dotFile)
	if err != nil {
		return fmt.Errorf("read pipeline file: %w", err)
	}

	graph, err := pipeline.ParseDOT(string(dotBytes))
	if err != nil {
		return fmt.Errorf("parse pipeline: %w", err)
	}

	if validationErr := pipeline.ValidateAll(graph); validationErr != nil && len(validationErr.Errors) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "=== Validation Warnings ===")
		for _, e := range validationErr.Errors {
			fmt.Fprintf(w, "  ! %s\n", e)
		}
	}

	printSimHeader(w, graph, dotFile)
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

	// Walk in BFS order from start node for consistent ordering.
	visited := make(map[string]bool)
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

		for _, edge := range graph.OutgoingEdges(nodeID) {
			if !visited[edge.To] {
				queue = append(queue, edge.To)
			}
		}
	}

	// Include any orphaned nodes not reachable from start.
	for _, node := range graph.Nodes {
		if !visited[node.ID] {
			ordered = append(ordered, node)
		}
	}

	for _, node := range ordered {
		label := node.Label
		if label == node.ID {
			label = ""
		}
		id := node.ID
		if len(id) > 20 {
			id = id[:17] + "..."
		}
		fmt.Fprintf(w, "  %-20s  %-15s  %-12s  %s\n", id, node.Handler, node.Shape, label)

		// Show important node attributes.
		for _, key := range []string{"llm_model", "llm_provider", "retry_policy", "max_retries", "fidelity", "prompt"} {
			if v, ok := node.Attrs[key]; ok {
				fmt.Fprintf(w, "  %-20s  > %s=%s\n", "", key, v)
			}
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

		label := node.Label
		if label == "" || label == node.ID {
			label = node.ID
		}

		// Show step number with handler type.
		fmt.Fprintf(w, "  %2d. %s  (%s)\n", step, label, node.Handler)

		// Show outgoing edges.
		edges := graph.OutgoingEdges(nodeID)
		for _, edge := range edges {
			arrow := "\u2514\u2500>"
			extra := ""
			if edge.Label != "" {
				extra = fmt.Sprintf(" [%s]", edge.Label)
			}
			if edge.Condition != "" {
				extra += fmt.Sprintf(" (when: %s)", edge.Condition)
			}
			fmt.Fprintf(w, "      %s %s%s\n", arrow, edge.To, extra)
			if !visited[edge.To] {
				queue = append(queue, edge.To)
			}
		}

		if len(edges) == 0 && nodeID != graph.ExitNode {
			fmt.Fprintln(w, "      ! dead end (no outgoing edges)")
		}
	}

	// Check for unreachable nodes.
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
