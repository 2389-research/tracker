// ABOUTME: Validate subcommand — checks DOT pipeline files for structural errors and warnings.
// ABOUTME: Returns exit code 0 for valid pipelines, 1 for errors. Suitable for CI/pre-commit.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/2389-research/tracker/pipeline"
)

// runValidate parses and validates a DOT file, printing results to w.
// Returns an error if validation finds structural problems.
func runValidate(dotFile string, w io.Writer) error {
	dotBytes, err := os.ReadFile(dotFile)
	if err != nil {
		return fmt.Errorf("read pipeline file: %w", err)
	}

	graph, err := pipeline.ParseDOT(string(dotBytes))
	if err != nil {
		return fmt.Errorf("parse pipeline: %w", err)
	}

	result := pipeline.ValidateAll(graph)

	if result == nil {
		fmt.Fprintf(w, "%s: valid (%d nodes, %d edges)\n", dotFile, len(graph.Nodes), len(graph.Edges))
		return nil
	}

	if len(result.Warnings) > 0 {
		for _, warn := range result.Warnings {
			fmt.Fprintf(w, "%s: warning: %s\n", dotFile, warn)
		}
	}

	if len(result.Errors) > 0 {
		for _, e := range result.Errors {
			fmt.Fprintf(w, "%s: error: %s\n", dotFile, e)
		}
		return fmt.Errorf("%d validation error(s)", len(result.Errors))
	}

	// Warnings only — still valid.
	fmt.Fprintf(w, "%s: valid with %d warning(s) (%d nodes, %d edges)\n",
		dotFile, len(result.Warnings), len(graph.Nodes), len(graph.Edges))
	return nil
}
