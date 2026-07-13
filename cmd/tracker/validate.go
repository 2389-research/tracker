// ABOUTME: Validate subcommand — checks pipeline files (.dot or .dip) for structural errors and warnings.
// ABOUTME: Returns exit code 0 for valid pipelines, 1 for errors. Suitable for CI/pre-commit.
package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

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
// .dipx bundles dispatch through loadDipxPipeline (the bundle loader verifies
// hashes + canonicalizes subgraph refs); .dip and .dot files go through the
// plain loadPipeline path that does NOT eagerly walk subgraph references —
// preserving the pre-.dipx behavior where validate only validates the entry
// file. Subgraph ref validation for .dip happens at run time, not validate time.
func loadPipelineForValidation(pipelineFile, formatOverride string) (*pipeline.Graph, string, error) {
	resolved, isEmbedded, info, err := resolvePipelineSource(pipelineFile)
	if err != nil {
		return nil, "", err
	}

	var graph *pipeline.Graph
	var displayName string
	switch {
	case isEmbedded:
		graph, err = loadEmbeddedPipeline(info)
		displayName = info.Name
	case strings.EqualFold(filepath.Ext(resolved), ".dipx"):
		graph, _, _, err = loadDipxPipeline(resolved)
		displayName = resolved
	default:
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
//
// DIP1XX lint warnings are stored on graph.LintWarnings AND folded into
// result.Warnings by validateGraph (so non-CLI consumers of the validator see
// them via the single warnings channel). The loader has already printed the
// long-form version of each DIP1XX warning to stderr; emitting the short-form
// copy from result.Warnings here would render the same warning twice (#244).
// We skip every result.Warnings entry whose text matches a string in
// graph.LintWarnings, so stdout carries only tracker-side semantic warnings
// (e.g. conditionalFailEdges, edgeLabelConsistency). The summary count uses
// len(result.Warnings) — DIP warnings are still counted toward the total
// even though their text is emitted on stderr by the loader rather than here.
func printValidationResult(w io.Writer, displayName string, graph *pipeline.Graph, result *pipeline.ValidationError) error {
	if result == nil {
		fmt.Fprintf(w, "%s: valid (%d nodes, %d edges)\n", displayName, len(graph.Nodes), len(graph.Edges))
		return nil
	}

	lintDups := graphLintWarningSet(graph)
	for _, warn := range result.Warnings {
		if lintDups[warn] {
			continue
		}
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

// graphLintWarningSet returns graph.LintWarnings as a lookup set. These are
// the pre-formatted single-line "warning[DIPnnn]: ..." strings the dippin
// loader stashed on the graph and that validateGraph then appended to
// ValidationError.Warnings. printValidationResult uses this set to skip
// emitting those same strings on stdout — they have already been printed
// in long form by the loader to stderr at load time. Nil-safe.
func graphLintWarningSet(graph *pipeline.Graph) map[string]bool {
	if graph == nil || len(graph.LintWarnings) == 0 {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(graph.LintWarnings))
	for _, w := range graph.LintWarnings {
		out[w] = true
	}
	return out
}

// mockHandler is a minimal handler implementation for validation purposes.
type mockHandler struct {
	name string
}

func (h *mockHandler) Name() string { return h.name }

func (h *mockHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
}
