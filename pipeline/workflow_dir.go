// ABOUTME: Helper to detect nodes that reference ${graph.workflow_dir}, so a
// ABOUTME: packed .dipx run (which has no source directory) can fail loud (#430).
package pipeline

import (
	"sort"
	"strings"
)

// NodesReferencingWorkflowDir returns the sorted IDs of nodes whose attributes
// reference graph.workflow_dir (e.g. `${graph.workflow_dir}/scripts/x.sh`).
//
// graph.workflow_dir is seeded to the source .dip's parent directory on
// source-tree loads, but a packed .dipx bundle is content-addressed and has no
// stable source directory, so the value is absent and expands to "". This
// helper lets the loader fail loud when a packed run would otherwise degrade a
// tool body like `. "${graph.workflow_dir}/scripts/x.sh"` to `. "/scripts/x.sh"`
// and abort mysteriously under `set -eu`.
func NodesReferencingWorkflowDir(g *Graph) []string {
	if g == nil {
		return nil
	}
	var ids []string
	for id, n := range g.Nodes {
		for _, v := range n.Attrs {
			if strings.Contains(v, "graph.workflow_dir") {
				ids = append(ids, id)
				break
			}
		}
	}
	sort.Strings(ids)
	return ids
}
