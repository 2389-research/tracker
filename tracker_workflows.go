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

// The four built-in .dip files plus the sidecar directories their *_file
// directives (prompt_file / command_file) reference. An embedded built-in can
// only use sidecars whose directory is listed here: go:embed of a directory
// takes every non-dot, non-underscore file beneath it, and the embedded loader
// resolves directives inside this FS (pipeline.ResolveFileDirectivesFS), never
// from disk. Extending another built-in with sidecars means adding its
// examples/prompts/<name> and examples/scripts/<name> dirs to this list.
//
//go:embed examples/ask_and_execute.dip
//go:embed examples/build_product.dip
//go:embed examples/build_product_with_superspec.dip
//go:embed examples/deep_review.dip
//go:embed examples/prompts/ask_and_execute examples/scripts/ask_and_execute
//go:embed examples/prompts/build_product examples/scripts/build_product
//go:embed examples/prompts/build_product_with_superspec examples/scripts/build_product_with_superspec
var embeddedWorkflows embed.FS

// EmbeddedWorkflowFS returns the read-only filesystem holding the built-in
// workflows and their sidecar files, rooted so that WorkflowInfo.File (e.g.
// "examples/build_product.dip") is a valid path within it. Pair it with
// pipeline.LoadDippinWorkflowFS to load a built-in with its prompt_file /
// command_file directives resolved from the binary rather than from disk.
func EmbeddedWorkflowFS() fs.FS {
	return embeddedWorkflows
}

// WorkflowInfo describes a built-in workflow embedded in the tracker binary.
type WorkflowInfo struct {
	Name        string   // bare name used for lookup, e.g. "build_product"
	File        string   // path within the embedded FS, e.g. "examples/build_product.dip"
	DisplayName string   // workflow declaration name, e.g. "BuildProduct"
	Goal        string   // parsed from the goal: field at the top of the .dip file
	Requires    []string // parsed from the `requires:` field (v0.29.0); nil if not declared
	// Path is set only by ResolveSource for a filesystem source: the resolved
	// on-disk path the source was read from (Name is empty in that case).
	Path string

	size int // byte length of the embedded source; precheck for embeddedWorkflowForSource
}

// Ref returns the SourceRef that anchors this workflow's source for Run /
// Simulate / ValidateSource / DescribeInputs: the built-in name for an
// embedded workflow, the on-disk path for a filesystem source.
func (info WorkflowInfo) Ref() SourceRef {
	return SourceRef{Path: info.Path, Builtin: info.Name}
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
			info := catalogEntry(entry.Name())
			catalog = append(catalog, info)
			catalogMap[info.Name] = info
		}
		sort.Slice(catalog, func(i, j int) bool {
			return catalog[i].Name < catalog[j].Name
		})
	})
}

// catalogEntry builds the WorkflowInfo for one embedded examples/<file>.dip.
func catalogEntry(fileName string) WorkflowInfo {
	file := "examples/" + fileName
	displayName, goal, requires := parseWorkflowHeader(file)
	size := 0
	if st, serr := fs.Stat(embeddedWorkflows, file); serr == nil {
		size = int(st.Size())
	}
	return WorkflowInfo{
		Name:        strings.TrimSuffix(fileName, ".dip"),
		File:        file,
		DisplayName: displayName,
		Goal:        goal,
		Requires:    requires,
		size:        size,
	}
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

// embeddedWorkflowForSource reports whether source is byte-identical to a
// built-in workflow and, if so, which one. The library's source-string entry
// points (Run / Simulate / ValidateSource / DescribeInputs) receive only text,
// so this is how a source handed back by ResolveSource / OpenWorkflow is
// recognised as embedded and gets its *_file sidecars resolved from the
// embed FS instead of the process cwd.
func embeddedWorkflowForSource(source string) (WorkflowInfo, bool) {
	loadWorkflowCatalog()
	for _, info := range catalog {
		if info.size != len(source) {
			continue
		}
		data, err := fs.ReadFile(embeddedWorkflows, info.File)
		if err == nil && string(data) == source {
			return cloneWorkflowInfo(info), true
		}
	}
	return WorkflowInfo{}, false
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
