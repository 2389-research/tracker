package pipeline

import (
	"fmt"

	"github.com/2389-research/dippin-lang/parser"
	"github.com/2389-research/dippin-lang/validator"
)

// LoadDippinWorkflow parses a dippin-lang source, runs the built-in validator
// and linter, converts to a Graph, and marks the graph as dippin-validated.
// This ensures all .dip entry points apply consistent validation semantics.
//
// filename is used for error messages (e.g., "inline.dip" or "/path/to/file.dip").
// Returns the graph and any validation/lint diagnostics (warnings only).
// Validation errors are returned as fatal errors.
func LoadDippinWorkflow(source, filename string) (*Graph, []validator.Diagnostic, error) {
	p := parser.NewParser(source, filename)
	workflow, err := p.Parse()
	if err != nil {
		return nil, nil, fmt.Errorf("parse Dippin file: %w", err)
	}

	// Run Dippin structural validation (DIP001–DIP009).
	valResult := validator.Validate(workflow)
	if valResult.HasErrors() {
		return nil, valResult.Diagnostics, fmt.Errorf("%d validation error(s) in %s", len(valResult.Errors()), filename)
	}

	// Run Dippin lint checks (DIP101–DIP115). Warnings only — don't block.
	lintResult := validator.Lint(workflow)

	// Convert IR to tracker's Graph representation.
	graph, err := FromDippinIR(workflow)
	if err != nil {
		return nil, nil, fmt.Errorf("convert Dippin IR to graph: %w", err)
	}

	// Mark graph as already validated by dippin-lang so that tracker's
	// own validator skips redundant structural checks (DIP001–DIP009).
	graph.DippinValidated = true

	// Return all diagnostics (both validation and lint) so callers can log them.
	var allDiags []validator.Diagnostic
	allDiags = append(allDiags, valResult.Diagnostics...)
	allDiags = append(allDiags, lintResult.Diagnostics...)

	return graph, allDiags, nil
}
