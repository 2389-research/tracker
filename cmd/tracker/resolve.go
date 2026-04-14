// ABOUTME: Pipeline name resolution — filesystem first, then embedded built-ins.
// ABOUTME: Single function encapsulating the "local file wins over built-in" logic.
package main

import (
	"fmt"
	"os"
	"strings"
)

// resolvePipelineSource resolves a pipeline name to either a filesystem path
// or an embedded built-in workflow. Resolution order:
//  1. Contains "/" or ends in .dip/.dot → filesystem path as-is
//  2. name.dip exists in cwd → local file wins
//  3. name exists as a file → return that path
//  4. Built-in workflow by name → embedded
//  5. Error with suggestions
func resolvePipelineSource(name string) (path string, embedded bool, info WorkflowInfo, err error) {
	// Explicit file path — pass through.
	if isExplicitFilePath(name) {
		return name, false, WorkflowInfo{}, nil
	}

	// Local .dip file in cwd takes priority.
	dipPath := name + ".dip"
	if _, statErr := os.Stat(dipPath); statErr == nil {
		return dipPath, false, WorkflowInfo{}, nil
	}

	// Bare file name that exists.
	if _, statErr := os.Stat(name); statErr == nil {
		return name, false, WorkflowInfo{}, nil
	}

	// Built-in workflow lookup.
	if wf, ok := lookupBuiltinWorkflow(name); ok {
		return "", true, wf, nil
	}

	return "", false, WorkflowInfo{}, buildPipelineNotFoundError(name)
}

// isExplicitFilePath returns true if name is a file path (contains / or has a .dip/.dot extension).
func isExplicitFilePath(name string) bool {
	return strings.Contains(name, "/") || strings.HasSuffix(name, ".dip") || strings.HasSuffix(name, ".dot")
}

// buildPipelineNotFoundError builds an error message listing available built-ins.
func buildPipelineNotFoundError(name string) error {
	available := listBuiltinWorkflows()
	if len(available) == 0 {
		return fmt.Errorf("unknown pipeline %q: file not found", name)
	}
	var names []string
	for _, wf := range available {
		names = append(names, wf.Name)
	}
	return fmt.Errorf(
		"unknown pipeline %q (no local file found, not a built-in workflow)\n  Available built-in workflows: %s\n  Run 'tracker workflows' to see details",
		name, strings.Join(names, ", "),
	)
}
