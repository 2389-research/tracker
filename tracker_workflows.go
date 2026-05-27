// ABOUTME: Built-in workflow catalog — embedded .dip files and name resolution.
// ABOUTME: Library consumers can list, read, and resolve workflows without shelling to the CLI.
package tracker

import (
	"bufio"
	"bytes"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed examples/ask_and_execute.dip
//go:embed examples/build_product.dip
//go:embed examples/build_product_with_superspec.dip
//go:embed examples/deep_review.dip
var embeddedWorkflows embed.FS

// WorkflowInfo describes a built-in workflow embedded in the tracker binary.
type WorkflowInfo struct {
	Name        string   // bare name used for lookup, e.g. "build_product"
	File        string   // path within the embedded FS, e.g. "examples/build_product.dip"
	DisplayName string   // workflow declaration name, e.g. "BuildProduct"
	Goal        string   // parsed from the goal: field at the top of the .dip file
	Requires    []string // parsed from the `requires:` field (v0.29.0); nil if not declared
}

var (
	catalogOnce sync.Once
	catalog     []WorkflowInfo
	catalogMap  map[string]WorkflowInfo
)

func loadWorkflowCatalog() {
	catalogOnce.Do(func() {
		catalogMap = make(map[string]WorkflowInfo)

		// Embedded FS should never fail; return empty catalog on error.
		entries, err := fs.ReadDir(embeddedWorkflows, "examples")
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".dip" {
				continue
			}
			file := "examples/" + entry.Name()
			name := strings.TrimSuffix(entry.Name(), ".dip")
			displayName, goal, requires := parseWorkflowHeader(file)

			info := WorkflowInfo{
				Name:        name,
				File:        file,
				DisplayName: displayName,
				Goal:        goal,
				Requires:    requires,
			}
			catalog = append(catalog, info)
			catalogMap[name] = info
		}
		sort.Slice(catalog, func(i, j int) bool {
			return catalog[i].Name < catalog[j].Name
		})
	})
}

// parseWorkflowHeader reads the first few lines of an embedded .dip file and
// extracts the workflow declaration name, goal field, and requires: list.
// Empty values if the fields aren't present. Scan stops at `start:`.
func parseWorkflowHeader(file string) (displayName, goal string, requires []string) {
	f, err := embeddedWorkflows.Open(file)
	if err != nil {
		return "", "", nil
	}
	defer f.Close()
	return parseWorkflowHeaderReader(f)
}

// parseWorkflowHeaderForTest exposes the parser to tests for fixture-based
// assertions without needing to bake test workflows into the embedded FS.
func parseWorkflowHeaderForTest(content []byte) (displayName, goal string, requires []string) {
	return parseWorkflowHeaderReader(bytes.NewReader(content))
}

// parseWorkflowHeaderReader scans the header section of a .dip source. It
// captures `workflow X`, `goal: ...`, and `requires: a, b, c` lines, then
// stops at the first `start:` line — the rest of the file is irrelevant.
func parseWorkflowHeaderReader(r io.Reader) (displayName, goal string, requires []string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "workflow ") {
			displayName = strings.TrimSpace(strings.TrimPrefix(trimmed, "workflow "))
			continue
		}
		if strings.HasPrefix(trimmed, "goal:") {
			goal = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "goal:")), `"`)
			continue
		}
		if strings.HasPrefix(trimmed, "requires:") {
			raw := strings.TrimSpace(strings.TrimPrefix(trimmed, "requires:"))
			seen := map[string]struct{}{}
			for _, part := range strings.Split(raw, ",") {
				s := strings.TrimSpace(part)
				if s == "" {
					continue
				}
				if _, dup := seen[s]; dup {
					continue
				}
				seen[s] = struct{}{}
				requires = append(requires, s)
			}
			continue
		}
		if strings.HasPrefix(trimmed, "start:") {
			break
		}
	}
	_ = scanner.Err() // best-effort on embedded files
	return displayName, goal, requires
}

// cloneWorkflowInfo deep-copies a WorkflowInfo so callers can't mutate the
// cached catalog through the returned value's slice field. The struct's
// scalar fields are value-copied for free by the assignment; only Requires
// needs explicit handling because it's a slice. Defensive: pre-v0.29.0
// WorkflowInfo had no slice fields and a shallow copy was safe.
func cloneWorkflowInfo(info WorkflowInfo) WorkflowInfo {
	if len(info.Requires) > 0 {
		reqCopy := make([]string, len(info.Requires))
		copy(reqCopy, info.Requires)
		info.Requires = reqCopy
	}
	return info
}

// Workflows returns the list of workflows embedded in the tracker binary,
// sorted by name. Library consumers can use this to show users the available
// built-ins without shelling out to `tracker workflows`. Returned values
// share no mutable state with the cached catalog.
func Workflows() []WorkflowInfo {
	loadWorkflowCatalog()
	out := make([]WorkflowInfo, len(catalog))
	for i, info := range catalog {
		out[i] = cloneWorkflowInfo(info)
	}
	return out
}

// LookupWorkflow returns the WorkflowInfo for a built-in workflow by bare name,
// or (zero, false) if no built-in matches. The returned value shares no
// mutable state with the cached catalog.
func LookupWorkflow(name string) (WorkflowInfo, bool) {
	loadWorkflowCatalog()
	info, ok := catalogMap[name]
	if !ok {
		return WorkflowInfo{}, false
	}
	return cloneWorkflowInfo(info), true
}

// OpenWorkflow returns the raw source bytes of a built-in workflow by bare
// name. This is the same content that `tracker init <name>` would copy to disk.
// Returns an error if the name is not a known built-in.
func OpenWorkflow(name string) ([]byte, WorkflowInfo, error) {
	info, ok := LookupWorkflow(name)
	if !ok {
		return nil, WorkflowInfo{}, fmt.Errorf("no built-in workflow named %q", name)
	}
	data, err := fs.ReadFile(embeddedWorkflows, info.File)
	if err != nil {
		return nil, info, fmt.Errorf("read embedded workflow %q: %w", name, err)
	}
	return data, info, nil
}
