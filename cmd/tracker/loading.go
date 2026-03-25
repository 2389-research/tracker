// ABOUTME: Pipeline file loading — reads .dip or .dot files and converts to Graph.
// ABOUTME: Auto-detects format from extension; resolves and loads subgraph references recursively.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/2389-research/dippin-lang/parser"
	"github.com/2389-research/dippin-lang/validator"
	"github.com/2389-research/tracker/pipeline"
)

// detectPipelineFormat returns "dip" or "dot" based on file extension.
func detectPipelineFormat(filename string) string {
	ext := filepath.Ext(filename)
	if ext == ".dip" {
		return "dip"
	}
	return "dot" // default to DOT for .dot and unknown extensions
}

// loadPipeline reads and parses a pipeline file, auto-detecting format from
// extension unless formatOverride is set. Emits a deprecation warning to stderr
// when the resolved format is "dot".
func loadPipeline(filename, formatOverride string) (*pipeline.Graph, error) {
	format := formatOverride
	if format == "" {
		format = detectPipelineFormat(filename)
	}

	if format == "dot" {
		emitDOTDeprecationWarning(os.Stderr)
	}

	fileBytes, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read pipeline file: %w", err)
	}

	switch format {
	case "dip":
		return loadDippinPipeline(string(fileBytes), filename)
	case "dot":
		return pipeline.ParseDOT(string(fileBytes))
	default:
		return nil, fmt.Errorf("unknown pipeline format: %q (valid: dip, dot)", format)
	}
}

// emitDOTDeprecationWarning prints a one-line warning that DOT is deprecated.
func emitDOTDeprecationWarning(w io.Writer) {
	fmt.Fprintln(w, "WARNING: DOT format is deprecated. Migrate pipelines to .dip format.")
}

// loadSubgraphs scans the graph for subgraph nodes and loads their referenced
// .dip files. Refs are resolved relative to the parent pipeline file's directory.
// Returns a map of ref → *Graph suitable for handlers.WithSubgraphs().
// Recursively loads nested subgraph refs.
func loadSubgraphs(graph *pipeline.Graph, parentFile string) (map[string]*pipeline.Graph, error) {
	parentDir := filepath.Dir(parentFile)
	subgraphs := make(map[string]*pipeline.Graph)
	return subgraphs, loadSubgraphsRecursive(graph, parentDir, subgraphs, make(map[string]bool))
}

func loadSubgraphsRecursive(graph *pipeline.Graph, baseDir string, subgraphs map[string]*pipeline.Graph, visited map[string]bool) error {
	for _, node := range graph.Nodes {
		ref := node.Attrs["subgraph_ref"]
		if ref == "" {
			continue
		}
		if subgraphs[ref] != nil {
			continue // already loaded
		}

		// Resolve path: try multiple strategies.
		resolved, err := resolveSubgraphPath(ref, baseDir)
		if err != nil {
			return fmt.Errorf("subgraph ref %q from node %q: %w", ref, node.ID, err)
		}

		// Use absolute path for cycle detection so different relative refs
		// to the same file are correctly deduplicated.
		absResolved, err := filepath.Abs(resolved)
		if err != nil {
			absResolved = resolved
		}
		if visited[absResolved] {
			return fmt.Errorf("circular subgraph reference detected: %q resolves to %q which is already being loaded (cycle)", ref, absResolved)
		}
		visited[absResolved] = true

		subGraph, err := loadPipeline(resolved, "")
		if err != nil {
			return fmt.Errorf("load subgraph %q (node %q): %w", ref, node.ID, err)
		}
		subgraphs[ref] = subGraph

		// Recursively load nested subgraphs.
		subDir := filepath.Dir(resolved)
		if err := loadSubgraphsRecursive(subGraph, subDir, subgraphs, visited); err != nil {
			return err
		}
	}
	return nil
}

// validateSubgraphRefs checks that every subgraph node in the graph has a
// corresponding entry in the loaded subgraphs map. Catches missing refs early
// instead of failing at execution time.
func validateSubgraphRefs(graph *pipeline.Graph, subgraphs map[string]*pipeline.Graph) error {
	for _, node := range graph.Nodes {
		if node.Handler != "subgraph" {
			continue
		}
		ref := node.Attrs["subgraph_ref"]
		if ref == "" {
			return fmt.Errorf("subgraph node %q has no subgraph_ref attribute", node.ID)
		}
		if subgraphs[ref] == nil {
			return fmt.Errorf("subgraph node %q references %q but it was not loaded", node.ID, ref)
		}
	}
	return nil
}

// resolveSubgraphPath finds the file for a subgraph ref. Tries (in order):
// 1. Relative to parent dir (ref as-is)
// 2. Relative to parent dir with .dip extension appended
// 3. Ref as-is from cwd
// 4. Ref with .dip extension from cwd
func resolveSubgraphPath(ref, baseDir string) (string, error) {
	candidates := []string{
		filepath.Join(baseDir, ref),
		filepath.Join(baseDir, ref+".dip"),
		ref,
		ref + ".dip",
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("file not found (tried: %s, %s, %s, %s)",
		candidates[0], candidates[1], candidates[2], candidates[3])
}

// loadDippinPipeline parses a .dip file using dippin-lang parser,
// runs Dippin's built-in validator and linter, then converts to Tracker's
// Graph representation. Validation errors are fatal; lint warnings are
// printed to stderr but do not block execution.
func loadDippinPipeline(source, filename string) (*pipeline.Graph, error) {
	p := parser.NewParser(source, filename)
	workflow, err := p.Parse()
	if err != nil {
		return nil, fmt.Errorf("parse Dippin file: %w", err)
	}

	// Run Dippin structural validation (DIP001–DIP009).
	valResult := validator.Validate(workflow)
	if valResult.HasErrors() {
		for _, d := range valResult.Diagnostics {
			fmt.Fprintln(os.Stderr, d.String())
		}
		return nil, fmt.Errorf("%d validation error(s) in %s", len(valResult.Errors()), filename)
	}

	// Run Dippin lint checks (DIP101–DIP115). Warnings only — don't block.
	lintResult := validator.Lint(workflow)
	for _, d := range lintResult.Diagnostics {
		fmt.Fprintln(os.Stderr, d.String())
	}

	graph, err := pipeline.FromDippinIR(workflow)
	if err != nil {
		return nil, fmt.Errorf("convert Dippin IR to graph: %w", err)
	}

	return graph, nil
}
