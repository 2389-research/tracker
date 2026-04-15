// ABOUTME: CLI-side pipeline name resolver — filesystem first, then built-ins.
// ABOUTME: Shares the built-in lookup with the top-level tracker package.
package main

import (
	"fmt"
	"os"
	"strings"

	tracker "github.com/2389-research/tracker"
)

// resolvePipelineSource resolves a pipeline name to either a filesystem path
// or an embedded built-in workflow. Resolution order:
//  1. Contains "/" or ends in .dip/.dot → filesystem path as-is
//  2. name.dip exists in cwd → local file wins
//  3. name exists as a file → return that path
//  4. Built-in workflow by name → embedded
//  5. Error with suggestions
//
// The CLI still returns the path (not the contents) on filesystem hits so
// downstream code can call loadPipelineFile. tracker.ResolveSource is the
// library equivalent that returns the contents directly.
func resolvePipelineSource(name string) (path string, embedded bool, info WorkflowInfo, err error) {
	if isExplicitFilePath(name) {
		return name, false, WorkflowInfo{}, nil
	}

	dipPath := name + ".dip"
	if _, statErr := os.Stat(dipPath); statErr == nil {
		return dipPath, false, WorkflowInfo{}, nil
	}

	if _, statErr := os.Stat(name); statErr == nil {
		return name, false, WorkflowInfo{}, nil
	}

	if wf, ok := tracker.LookupWorkflow(name); ok {
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
	names := make([]string, 0, len(available))
	for _, wf := range available {
		names = append(names, wf.Name)
	}
	return fmt.Errorf(
		"unknown pipeline %q (no local file found, not a built-in workflow)\n  Available built-in workflows: %s\n  Run 'tracker workflows' to see details",
		name, strings.Join(names, ", "),
	)
}
