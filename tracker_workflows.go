// ABOUTME: Built-in workflow catalog — embedded .dip files and name resolution.
// ABOUTME: Library consumers can list, read, and resolve workflows without shelling to the CLI.
package tracker

import (
	"bufio"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed workflows/*.dip
var embeddedWorkflows embed.FS

// WorkflowInfo describes a built-in workflow embedded in the tracker binary.
type WorkflowInfo struct {
	Name        string // bare name used for lookup, e.g. "build_product"
	File        string // path within the embedded FS, e.g. "workflows/build_product.dip"
	DisplayName string // workflow declaration name, e.g. "BuildProduct"
	Goal        string // parsed from the goal: field at the top of the .dip file
}

var (
	catalogOnce sync.Once
	catalog     []WorkflowInfo
	catalogMap  map[string]WorkflowInfo
)

func loadWorkflowCatalog() {
	catalogOnce.Do(func() {
		catalogMap = make(map[string]WorkflowInfo)

		entries, err := fs.ReadDir(embeddedWorkflows, "workflows")
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".dip" {
				continue
			}
			file := "workflows/" + entry.Name()
			name := strings.TrimSuffix(entry.Name(), ".dip")
			displayName, goal := parseWorkflowHeader(file)

			info := WorkflowInfo{
				Name:        name,
				File:        file,
				DisplayName: displayName,
				Goal:        goal,
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
// extracts the workflow declaration name and goal field. Empty strings if the
// fields aren't present.
func parseWorkflowHeader(file string) (displayName, goal string) {
	f, err := embeddedWorkflows.Open(file)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "workflow ") {
			displayName = strings.TrimSpace(strings.TrimPrefix(trimmed, "workflow "))
			continue
		}
		if strings.HasPrefix(trimmed, "goal:") {
			goal = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "goal:")), `"`)
			break
		}
		if strings.HasPrefix(trimmed, "start:") {
			break
		}
	}
	return displayName, goal
}

// Workflows returns the list of workflows embedded in the tracker binary,
// sorted by name. Library consumers can use this to show users the available
// built-ins without shelling out to `tracker workflows`.
func Workflows() []WorkflowInfo {
	loadWorkflowCatalog()
	out := make([]WorkflowInfo, len(catalog))
	copy(out, catalog)
	return out
}

// LookupWorkflow returns the WorkflowInfo for a built-in workflow by bare name,
// or (zero, false) if no built-in matches.
func LookupWorkflow(name string) (WorkflowInfo, bool) {
	loadWorkflowCatalog()
	info, ok := catalogMap[name]
	return info, ok
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
