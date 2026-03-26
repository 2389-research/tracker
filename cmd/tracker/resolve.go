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
	if strings.Contains(name, "/") || strings.HasSuffix(name, ".dip") || strings.HasSuffix(name, ".dot") {
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

	// Nothing matched — build a helpful error.
	available := listBuiltinWorkflows()
	if len(available) > 0 {
		var names []string
		for _, wf := range available {
			names = append(names, wf.Name)
		}
		return "", false, WorkflowInfo{}, fmt.Errorf(
			"unknown pipeline %q (no local file found, not a built-in workflow)\n  Available built-in workflows: %s\n  Run 'tracker workflows' to see details",
			name, strings.Join(names, ", "),
		)
	}
	return "", false, WorkflowInfo{}, fmt.Errorf("unknown pipeline %q: file not found", name)
}
