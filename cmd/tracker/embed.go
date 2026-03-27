// ABOUTME: Embeds built-in workflow .dip files and provides catalog lookup.
// ABOUTME: Lazy-parses embedded files to extract workflow name and goal for listing.
package main

import (
	"bufio"
	"embed"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed workflows/*.dip
var embeddedWorkflows embed.FS

// WorkflowInfo describes a built-in embedded workflow.
type WorkflowInfo struct {
	Name        string // bare name, e.g. "build_product"
	File        string // path within embed.FS, e.g. "workflows/build_product.dip"
	DisplayName string // workflow declaration name, e.g. "BuildProduct"
	Goal        string // parsed from goal: field
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

// parseWorkflowHeader reads the first few lines of an embedded .dip file
// to extract the workflow name and goal field.
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
			displayName = strings.TrimPrefix(trimmed, "workflow ")
			displayName = strings.TrimSpace(displayName)
			continue
		}
		if strings.HasPrefix(trimmed, "goal:") {
			goal = strings.TrimPrefix(trimmed, "goal:")
			goal = strings.TrimSpace(goal)
			goal = strings.Trim(goal, `"`)
			break
		}
		// Stop scanning after start: line (past the header).
		if strings.HasPrefix(trimmed, "start:") {
			break
		}
	}
	return displayName, goal
}

// lookupBuiltinWorkflow returns the WorkflowInfo for a bare name, or false.
func lookupBuiltinWorkflow(name string) (WorkflowInfo, bool) {
	loadWorkflowCatalog()
	info, ok := catalogMap[name]
	return info, ok
}

// listBuiltinWorkflows returns all embedded workflows sorted by name.
func listBuiltinWorkflows() []WorkflowInfo {
	loadWorkflowCatalog()
	return catalog
}
