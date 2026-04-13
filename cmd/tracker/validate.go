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
	graph, displayName, err := loadPipelineForValidation(pipelineFile, formatOverride)
	if err != nil {
		return err
	}

	registry := buildValidationRegistry()
	result := pipeline.ValidateAllWithLint(graph, registry)

	return printValidationResult(w, displayName, graph, result)
}

// loadPipelineForValidation resolves and loads a pipeline, returning the graph and display name.
func loadPipelineForValidation(pipelineFile, formatOverride string) (*pipeline.Graph, string, error) {
	resolved, isEmbedded, info, err := resolvePipelineSource(pipelineFile)
	if err != nil {
		return nil, "", err
	}

	var graph *pipeline.Graph
	var displayName string
	if isEmbedded {
		graph, err = loadEmbeddedPipeline(info)
		displayName = info.Name
	} else {
		graph, err = loadPipeline(resolved, formatOverride)
		displayName = resolved
	}
	if err != nil {
		return nil, "", fmt.Errorf("load pipeline: %w", err)
	}
	return graph, displayName, nil
}

// buildValidationRegistry creates a handler registry with all known handler names.
func buildValidationRegistry() *pipeline.HandlerRegistry {
	registry := pipeline.NewHandlerRegistry()
	for _, name := range []string{"codergen", "tool", "subgraph", "spawn", "start", "exit", "conditional", "wait.human", "parallel", "parallel.fan_in", "manager_loop"} {
		registry.Register(&mockHandler{name: name})
	}
	return registry
}

// printValidationResult writes the validation outcome to w and returns an error on failures.
func printValidationResult(w io.Writer, displayName string, graph *pipeline.Graph, result *pipeline.ValidationError) error {
	if result == nil {
		fmt.Fprintf(w, "%s: valid (%d nodes, %d edges)\n", displayName, len(graph.Nodes), len(graph.Edges))
		return nil
	}

	for _, warn := range result.Warnings {
		fmt.Fprintf(w, "%s\n", warn)
	}

	if len(result.Errors) > 0 {
		for _, e := range result.Errors {
			fmt.Fprintf(w, "%s: error: %s\n", displayName, e)
		}
		return fmt.Errorf("%d validation error(s)", len(result.Errors))
	}

	fmt.Fprintf(w, "%s: valid with %d warning(s) (%d nodes, %d edges)\n",
		displayName, len(result.Warnings), len(graph.Nodes), len(graph.Edges))
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
