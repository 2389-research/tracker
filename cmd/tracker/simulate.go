// ABOUTME: Simulate subcommand — dry-runs a pipeline (.dot or .dip) without LLM calls.
// ABOUTME: Shows execution plan: node order, handlers, edges, conditions, and graph attributes.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline"
)

// runSimulateCmd parses a pipeline file and prints the execution plan without running anything.
// Auto-detects format based on file extension unless formatOverride is set.
func runSimulateCmd(pipelineFile, formatOverride string, w io.Writer) error {
	resolved, isEmbedded, info, err := resolvePipelineSource(pipelineFile)
	if err != nil {
		return err
	}

	// Keep the existing graph-level parsing for validation warnings
	// (library Simulate doesn't currently surface validation warnings).
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

	// Re-parse via library for the structured report.
	var source string
	if isEmbedded {
		data, _, oerr := tracker.OpenWorkflow(info.Name)
		if oerr != nil {
			return fmt.Errorf("open embedded workflow: %w", oerr)
		}
		source = string(data)
	} else {
		data, rerr := os.ReadFile(resolved)
		if rerr != nil {
			return fmt.Errorf("read pipeline file: %w", rerr)
		}
		source = string(data)
	}

	report, err := tracker.Simulate(source)
	if err != nil {
		return err
	}

	printSimReport(w, report, pipelineFile, graph)
	return nil
}

// printSimReport is the top-level entry point for printing a SimulateReport.
func printSimReport(w io.Writer, report *tracker.SimulateReport, displayName string, graph *pipeline.Graph) {
	printSimHeader(w, report, displayName, graph)
	printSimNodesFromReport(w, report.Nodes)
	printSimEdgesFromReport(w, report.Edges)
	printSimExecutionPlanFromReport(w, report)
	printSimFooter(w)
}

func printSimHeader(w io.Writer, report *tracker.SimulateReport, dotFile string, graph *pipeline.Graph) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "\u2550\u2550\u2550 Pipeline Simulation \u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550")
	name := report.Name
	if name == "" {
		name = dotFile
	}
	fmt.Fprintf(w, "  Pipeline:  %s\n", name)
	fmt.Fprintf(w, "  Nodes:     %d\n", len(report.Nodes))
	fmt.Fprintf(w, "  Edges:     %d\n", len(report.Edges))
	fmt.Fprintf(w, "  Start:     %s\n", report.StartNode)
	fmt.Fprintf(w, "  Exit:      %s\n", report.ExitNode)

	if graph != nil && len(graph.Attrs) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  Graph Attributes:")
		for k, v := range graph.Attrs {
			fmt.Fprintf(w, "    %s = %s\n", k, v)
		}
	}
}

func printSimNodesFromReport(w io.Writer, nodes []tracker.SimNode) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "\u2500\u2500\u2500 Nodes \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")
	fmt.Fprintf(w, "  %-20s  %-15s  %-12s  %s\n", "ID", "Handler", "Shape", "Label")
	fmt.Fprintf(w, "  %-20s  %-15s  %-12s  %s\n", "\u2500\u2500", "\u2500\u2500\u2500\u2500\u2500\u2500\u2500", "\u2500\u2500\u2500\u2500\u2500", "\u2500\u2500\u2500\u2500\u2500")

	for _, node := range nodes {
		printSimNodeRow(w, node)
	}
}

// printSimNodeRow prints one node's summary row and its key attributes.
func printSimNodeRow(w io.Writer, node tracker.SimNode) {
	label := node.Label
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

func printSimEdgesFromReport(w io.Writer, edges []tracker.SimEdge) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "\u2500\u2500\u2500 Edges \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")

	for _, edge := range edges {
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

func printSimExecutionPlanFromReport(w io.Writer, report *tracker.SimulateReport) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "\u2500\u2500\u2500 Execution Plan \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")
	fmt.Fprintln(w, "  (simulated BFS traversal from start node)")
	fmt.Fprintln(w)

	if report.StartNode == "" {
		fmt.Fprintln(w, "  ! No start node found")
		return
	}

	// Build a lookup map from node ID to SimNode for label/handler access.
	nodeByID := make(map[string]tracker.SimNode, len(report.Nodes))
	for _, n := range report.Nodes {
		nodeByID[n.ID] = n
	}

	for _, step := range report.ExecutionPlan {
		node := nodeByID[step.NodeID]
		printSimPlanStepFromReport(w, step, node, report.ExitNode)
	}

	if len(report.Unreachable) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  ! Unreachable nodes: %s\n", strings.Join(report.Unreachable, ", "))
	}
}

// printSimPlanStepFromReport prints a single step and its outgoing edges.
func printSimPlanStepFromReport(w io.Writer, step tracker.PlanStep, node tracker.SimNode, exitNode string) {
	// Use label if set (and different from ID), otherwise use ID — mirrors original behavior.
	label := node.Label
	if label == "" {
		label = step.NodeID
	}
	fmt.Fprintf(w, "  %2d. %s  (%s)\n", step.Step, label, node.Handler)
	for _, edge := range step.Edges {
		extra := formatSimEdgeAnnotation(edge)
		fmt.Fprintf(w, "      \u2514\u2500> %s%s\n", edge.To, extra)
	}
	if len(step.Edges) == 0 && step.NodeID != exitNode {
		fmt.Fprintln(w, "      ! dead end (no outgoing edges)")
	}
}

// formatSimEdgeAnnotation returns label and condition annotations for a SimEdge.
func formatSimEdgeAnnotation(edge tracker.SimEdge) string {
	extra := ""
	if edge.Label != "" {
		extra = fmt.Sprintf(" [%s]", edge.Label)
	}
	if edge.Condition != "" {
		extra += fmt.Sprintf(" (when: %s)", edge.Condition)
	}
	return extra
}

func printSimFooter(w io.Writer) {
	fmt.Fprintln(w, "\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550")
}
