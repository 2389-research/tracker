// ABOUTME: Library-facing helpers for resolving pipeline sources and run checkpoints.
// ABOUTME: Mirrors the CLI's bare-name and run-ID resolution for library consumers.
package tracker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveSource resolves a pipeline name to the actual workflow source text.
// The resolution order matches the CLI's tracker.resolvePipelineSource:
//
//  1. If name contains "/" or ends in ".dip"/".dot", treat it as a filesystem
//     path and read it from disk.
//  2. If "<name>.dip" exists under workDir, read that.
//  3. If "<name>" exists under workDir, read that.
//  4. If name matches a built-in workflow, return the embedded source.
//  5. Otherwise return an error listing available built-ins.
//
// The returned WorkflowInfo is populated only for built-in workflows; it's
// the zero value for filesystem sources. workDir may be empty — in that case
// the current working directory is used for relative lookups.
func ResolveSource(name, workDir string) (source string, info WorkflowInfo, err error) {
	if name == "" {
		return "", WorkflowInfo{}, fmt.Errorf("pipeline name cannot be empty")
	}

	if isExplicitFilePath(name) {
		data, rerr := os.ReadFile(name)
		if rerr != nil {
			return "", WorkflowInfo{}, fmt.Errorf("read pipeline file %q: %w", name, rerr)
		}
		return string(data), WorkflowInfo{}, nil
	}

	baseDir := workDir
	if baseDir == "" {
		if cwd, cerr := os.Getwd(); cerr == nil {
			baseDir = cwd
		}
	}

	dipPath := filepath.Join(baseDir, name+".dip")
	if _, statErr := os.Stat(dipPath); statErr == nil {
		data, rerr := os.ReadFile(dipPath)
		if rerr != nil {
			return "", WorkflowInfo{}, fmt.Errorf("read %q: %w", dipPath, rerr)
		}
		return string(data), WorkflowInfo{}, nil
	}

	barePath := filepath.Join(baseDir, name)
	if _, statErr := os.Stat(barePath); statErr == nil {
		data, rerr := os.ReadFile(barePath)
		if rerr != nil {
			return "", WorkflowInfo{}, fmt.Errorf("read %q: %w", barePath, rerr)
		}
		return string(data), WorkflowInfo{}, nil
	}

	if wfInfo, ok := LookupWorkflow(name); ok {
		data, _, oerr := OpenWorkflow(name)
		if oerr != nil {
			return "", wfInfo, oerr
		}
		return string(data), wfInfo, nil
	}

	return "", WorkflowInfo{}, buildPipelineNotFoundError(name)
}

// isExplicitFilePath returns true if name looks like a filesystem path rather
// than a bare workflow name.
func isExplicitFilePath(name string) bool {
	return strings.Contains(name, "/") || strings.HasSuffix(name, ".dip") || strings.HasSuffix(name, ".dot")
}

// buildPipelineNotFoundError returns a diagnostic listing all available
// built-in workflows when a name can't be resolved.
func buildPipelineNotFoundError(name string) error {
	available := Workflows()
	if len(available) == 0 {
		return fmt.Errorf("unknown pipeline %q: file not found", name)
	}
	names := make([]string, 0, len(available))
	for _, wf := range available {
		names = append(names, wf.Name)
	}
	return fmt.Errorf(
		"unknown pipeline %q (no local file found, not a built-in workflow). Available built-in workflows: %s",
		name, strings.Join(names, ", "),
	)
}

// ResolveCheckpoint finds the checkpoint file path for a given run ID under
// the working directory's .tracker/runs/<runID>/checkpoint.json layout. The
// runID argument may be a unique prefix of a real run ID.
//
// Returns the absolute path to checkpoint.json, or an error if the run is
// not found, the prefix is ambiguous, or the checkpoint file is missing.
//
// This is the library equivalent of the CLI's `tracker -r <runID>` flag.
// Library consumers can set Config.ResumeRunID to have NewEngine resolve
// the checkpoint automatically.
func ResolveCheckpoint(workDir, runID string) (string, error) {
	if runID == "" {
		return "", fmt.Errorf("run ID cannot be empty")
	}
	runsDir := filepath.Join(workDir, ".tracker", "runs")
	resolved, err := resolveRunIDToDir(runsDir, runID)
	if err != nil {
		return "", err
	}
	cpPath := filepath.Join(runsDir, resolved, "checkpoint.json")
	if _, err := os.Stat(cpPath); err != nil {
		return "", fmt.Errorf("checkpoint not found for run %s: %w", resolved, err)
	}
	return cpPath, nil
}

// resolveRunIDToDir finds the unique run directory matching a run ID prefix.
func resolveRunIDToDir(runsDir, runID string) (string, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", fmt.Errorf("cannot read runs directory %q: %w", runsDir, err)
	}

	var matches []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), runID) {
			matches = append(matches, e.Name())
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no run found matching %q in %s", runID, runsDir)
	case 1:
		return matches[0], nil
	default:
		for _, m := range matches {
			if m == runID {
				return m, nil
			}
		}
		return "", fmt.Errorf("ambiguous run ID %q matches %d runs: %s", runID, len(matches), strings.Join(matches, ", "))
	}
}
