// ABOUTME: Simulate subcommand — dry-runs a pipeline (.dot or .dip) without LLM calls.
// ABOUTME: Shows execution plan: node order, handlers, edges, conditions, and graph attributes.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	tracker "github.com/2389-research/tracker"
)

// runSimulateCmd parses a pipeline file and prints the execution plan without running anything.
// Format is resolved in this order: explicit formatOverride wins; otherwise,
// for on-disk files we use the file extension (.dip or .dot via
// detectPipelineFormat, which defaults to .dip for unknown extensions); for
// embedded workflows we pin to .dip. The library-internal content-sniff
// fallback is never reached here — we always pass a concrete format to
// ValidateSource.
//
// The source is read and parsed exactly once — ValidateSource returns the
// parsed graph alongside any structural errors and lint warnings, and
// SimulateGraph consumes that same graph for the structured report. No
// second os.ReadFile, no duplicated dippin-lang parser side effects, no
// TOCTOU window between validation and simulation.
func runSimulateCmd(pipelineFile, formatOverride string, w io.Writer) error {
	resolved, isEmbedded, info, err := resolvePipelineSource(pipelineFile)
	if err != nil {
		return err
	}

	source, displayName, err := readPipelineSource(resolved, isEmbedded, info)
	if err != nil {
		return fmt.Errorf("load pipeline: %w", err)
	}

	// Preserve the prior CLI behavior: format comes from the explicit flag,
	// otherwise from the file extension for on-disk inputs. Embedded
	// workflows are always .dip. detectPipelineFormat never returns the
	// empty string, so format is always concrete by the time we call
	// ValidateSource.
	format := formatOverride
	if format == "" {
		if isEmbedded {
			format = "dip"
		} else {
			format = detectPipelineFormat(resolved)
		}
	}
	if format == "dot" {
		emitDOTDeprecationWarning(os.Stderr)
	}

	opts := []tracker.ValidateOption{tracker.WithValidateFormat(format)}
	result, validateErr := tracker.ValidateSource(source, opts...)
	if validateErr != nil && (result == nil || result.Graph == nil) {
		// Unrecoverable parse or structural error — no graph to simulate.
		return fmt.Errorf("load pipeline: %w", validateErr)
	}

	// ValidationResult.Errors carries structural problems (unreachable nodes,
	// bad references, etc.); Warnings carries lint-style advisory items.
	// The old CLI printed only Errors under a "Validation Warnings" heading
	// — confusing. We now split them into explicit sections and continue to
	// simulate either way (matching prior continue-on-errors behavior so
	// users can still see the plan of a draft pipeline).
	if len(result.Errors) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "=== Validation Errors ===")
		for _, e := range result.Errors {
			fmt.Fprintf(w, "  ! %s\n", e)
		}
	}
	if len(result.Warnings) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "=== Validation Warnings ===")
		for _, msg := range result.Warnings {
			fmt.Fprintf(w, "  ~ %s\n", msg)
		}
	}

	report, err := tracker.SimulateGraph(context.Background(), result.Graph)
	if err != nil {
		return err
	}

	printSimReport(w, report, displayName)
	return nil
}

// readPipelineSource returns the raw pipeline source as a string together
// with a display name for the header. Embedded workflows are opened via
// tracker.OpenWorkflow; files are read once from disk.
func readPipelineSource(resolved string, isEmbedded bool, info WorkflowInfo) (source, displayName string, err error) {
	if isEmbedded {
		data, _, oerr := tracker.OpenWorkflow(info.Name)
		if oerr != nil {
			return "", "", fmt.Errorf("open embedded workflow: %w", oerr)
		}
		return string(data), info.Name, nil
	}
	data, rerr := os.ReadFile(resolved)
	if rerr != nil {
		return "", "", fmt.Errorf("read pipeline file: %w", rerr)
	}
	return string(data), resolved, nil
}

// printSimReport is the top-level entry point for printing a SimulateReport.
func printSimReport(w io.Writer, report *tracker.SimulateReport, displayName string) {
	printSimHeader(w, report, displayName)
	printSimNodesFromReport(w, report.Nodes)
	printSimEdgesFromReport(w, report.Edges)
	printSimExecutionPlanFromReport(w, report)
	printSimFooter(w)
}

func printSimHeader(w io.Writer, report *tracker.SimulateReport, dotFile string) {
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

	if len(report.GraphAttrs) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  Graph Attributes:")
		keys := make([]string, 0, len(report.GraphAttrs))
		for k := range report.GraphAttrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := report.GraphAttrs[k]
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
