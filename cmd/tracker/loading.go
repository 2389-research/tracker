// ABOUTME: Pipeline file loading — reads .dip or .dot files and converts to Graph.
// ABOUTME: Auto-detects format from extension; emits deprecation warning for DOT.
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
