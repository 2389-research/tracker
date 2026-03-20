// ABOUTME: Validate subcommand — checks pipeline files (.dot or .dip) for structural errors and warnings.
// ABOUTME: Returns exit code 0 for valid pipelines, 1 for errors. Suitable for CI/pre-commit.
package main

import (
	"fmt"
	"io"

	"github.com/2389-research/tracker/pipeline"
)

// runValidateCmd parses and validates a pipeline file, printing results to w.
// Returns an error if validation finds structural problems.
// Auto-detects format based on file extension unless formatOverride is set.
func runValidateCmd(pipelineFile, formatOverride string, w io.Writer) error {
	graph, err := loadPipeline(pipelineFile, formatOverride)
	if err != nil {
		return fmt.Errorf("load pipeline: %w", err)
	}

	result := pipeline.ValidateAll(graph)

	if result == nil {
		fmt.Fprintf(w, "%s: valid (%d nodes, %d edges)\n", pipelineFile, len(graph.Nodes), len(graph.Edges))
		return nil
	}

	if len(result.Warnings) > 0 {
		for _, warn := range result.Warnings {
			fmt.Fprintf(w, "%s: warning: %s\n", pipelineFile, warn)
		}
	}

	if len(result.Errors) > 0 {
		for _, e := range result.Errors {
			fmt.Fprintf(w, "%s: error: %s\n", pipelineFile, e)
		}
		return fmt.Errorf("%d validation error(s)", len(result.Errors))
	}

	// Warnings only — still valid.
	fmt.Fprintf(w, "%s: valid with %d warning(s) (%d nodes, %d edges)\n",
		pipelineFile, len(result.Warnings), len(graph.Nodes), len(graph.Edges))
	return nil
}
