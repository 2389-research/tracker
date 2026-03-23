// ABOUTME: Validate subcommand — checks pipeline files (.dot or .dip) for structural errors and warnings.
// ABOUTME: Returns exit code 0 for valid pipelines, 1 for errors. Suitable for CI/pre-commit.
package main

import (
	"context"
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

	// Create a handler registry for semantic validation
	registry := pipeline.NewHandlerRegistry()
	// Register standard handlers (these are the ones Tracker supports)
	// Note: We don't need actual LLM clients for validation, just handler names
	registry.Register(&mockHandler{name: "codergen"})
	registry.Register(&mockHandler{name: "tool"})
	registry.Register(&mockHandler{name: "subgraph"})
	registry.Register(&mockHandler{name: "spawn"})
	registry.Register(&mockHandler{name: "start"})
	registry.Register(&mockHandler{name: "exit"})
	registry.Register(&mockHandler{name: "conditional"})
	registry.Register(&mockHandler{name: "wait.human"})
	registry.Register(&mockHandler{name: "parallel"})
	registry.Register(&mockHandler{name: "parallel.fan_in"})
	registry.Register(&mockHandler{name: "manager_loop"})

	// Run structural + semantic validation + lint
	result := pipeline.ValidateAllWithLint(graph, registry)

	if result == nil {
		fmt.Fprintf(w, "%s: valid (%d nodes, %d edges)\n", pipelineFile, len(graph.Nodes), len(graph.Edges))
		return nil
	}

	if len(result.Warnings) > 0 {
		for _, warn := range result.Warnings {
			fmt.Fprintf(w, "%s\n", warn)
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

// mockHandler is a minimal handler implementation for validation purposes.
type mockHandler struct {
	name string
}

func (h *mockHandler) Name() string { return h.name }

func (h *mockHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
}
