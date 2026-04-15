// ABOUTME: CLI-side aliases for the built-in workflow catalog.
// ABOUTME: Thin wrappers over the top-level tracker package so CLI and library share one source.
package main

import (
	tracker "github.com/2389-research/tracker"
)

// WorkflowInfo is a local alias for tracker.WorkflowInfo so existing CLI call
// sites that passed WorkflowInfo around don't need to import the library type.
type WorkflowInfo = tracker.WorkflowInfo

// lookupBuiltinWorkflow returns the WorkflowInfo for a bare workflow name, or
// false if the name is not a known built-in.
func lookupBuiltinWorkflow(name string) (WorkflowInfo, bool) {
	return tracker.LookupWorkflow(name)
}

// listBuiltinWorkflows returns all embedded workflows sorted by name.
func listBuiltinWorkflows() []WorkflowInfo {
	return tracker.Workflows()
}
