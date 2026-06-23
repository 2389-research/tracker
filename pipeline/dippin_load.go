package pipeline

import (
	"fmt"
	"path/filepath"

	"github.com/2389-research/dippin-lang/ir"
	"github.com/2389-research/dippin-lang/parser"
	"github.com/2389-research/dippin-lang/validator"
)

// LoadDippinWorkflow parses a dippin-lang source, then delegates to
// LoadDippinWorkflowFromIR for validation, lint, and conversion to a Graph.
// This ensures all .dip entry points apply consistent validation semantics.
//
// filename is used for error messages (e.g., "inline.dip" or "/path/to/file.dip")
// and as the anchor for file directives: command_file / prompt_file /
// system_prompt_file paths resolve relative to filepath.Dir(filename), matching
// the dippin CLI. For synthetic filenames like "inline.dip" the anchor is "."
// (cwd-relative) — the same semantics as `dippin check inline.dip` run from cwd.
// A directive resolution failure (missing/unreadable file, path escape) is fatal.
//
// Returns the graph and any validation/lint diagnostics (warnings only).
// Validation errors are returned as fatal errors.
func LoadDippinWorkflow(source, filename string) (*Graph, []validator.Diagnostic, error) {
	workflow, err := parser.NewParser(source, filename).Parse()
	if err != nil {
		return nil, nil, fmt.Errorf("parse Dippin file: %w", err)
	}
	// Parser entry points deliberately do not resolve file directives
	// ("CLI entry points call it after parsing") — tracker is a CLI entry
	// point, so resolve here. dippin's error already names the node ID,
	// directive, and user-written path.
	if err := parser.ResolveFileDirectives(workflow, filepath.Dir(filename)); err != nil {
		return nil, nil, fmt.Errorf("resolve file directives in %s: %w", filename, err)
	}
	return LoadDippinWorkflowFromIR(workflow, filename)
}

// LoadDippinWorkflowFromIR runs dippin's structural validator (DIP001–DIP009)
// and linter (DIP101–DIP115) on an already-parsed IR workflow, then converts
// it to tracker's Graph representation and marks it dippin-validated.
//
// Diagnostics returned cover both validate and lint passes; validation errors
// are fatal, lint warnings are non-fatal. This function exists separately from
// LoadDippinWorkflow so that callers which already hold a parsed *ir.Workflow
// (e.g., the .dipx bundle loader, which gets one back from dipx.Open) can
// reuse the validate/lint/convert tail without re-parsing source.
func LoadDippinWorkflowFromIR(workflow *ir.Workflow, filename string) (*Graph, []validator.Diagnostic, error) {
	if workflow == nil {
		return nil, nil, fmt.Errorf("nil workflow for %s", filename)
	}

	// Run Dippin structural validation (DIP001–DIP009).
	valResult := validator.Validate(workflow)
	if valResult.HasErrors() {
		errCount := 0
		for _, d := range valResult.Diagnostics {
			if d.Severity == validator.SeverityError {
				errCount++
			}
		}
		return nil, valResult.Diagnostics, fmt.Errorf("%d validation error(s) in %s", errCount, filename)
	}

	// Run Dippin lint checks (DIP101+). Warnings only — don't block.
	// dippin-lang is the single source of truth for DIP-coded lint; tracker
	// no longer maintains a parallel implementation.
	lintResult := validator.Lint(workflow)

	// Convert IR to tracker's Graph representation.
	graph, err := FromDippinIR(workflow)
	if err != nil {
		return nil, nil, fmt.Errorf("convert Dippin IR to graph: %w", err)
	}

	// Mark graph as already validated by dippin-lang so that tracker's
	// own validator skips redundant structural checks (DIP001–DIP009).
	graph.DippinValidated = true

	// Stash formatted lint warnings on the graph so ValidateAll/-WithLint
	// can surface them through the standard warnings channel without
	// re-running any DIP check on the tracker side.
	graph.LintWarnings = formatLintWarnings(lintResult.Diagnostics)

	// Return all diagnostics (both validation and lint) so callers can log them.
	var allDiags []validator.Diagnostic
	allDiags = append(allDiags, valResult.Diagnostics...)
	allDiags = append(allDiags, lintResult.Diagnostics...)

	return graph, allDiags, nil
}

// formatLintWarnings returns single-line "warning[CODE]: message" strings for
// every warning-severity diagnostic. Used to seed Graph.LintWarnings so the
// "Validation Warnings" output rendered by tracker validate / simulate stays
// one-line-per-warning regardless of dippin-lang's multi-line String() form.
// Errors are not included — those are returned as a fatal error.
func formatLintWarnings(diags []validator.Diagnostic) []string {
	if len(diags) == 0 {
		return nil
	}
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		if d.Severity != validator.SeverityWarning {
			continue
		}
		out = append(out, fmt.Sprintf("warning[%s]: %s", d.Code, d.Message))
	}
	return out
}
