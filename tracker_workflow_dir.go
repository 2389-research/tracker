// ABOUTME: Resolves ${graph.workflow_dir} for the library path: seeds it for disk
// ABOUTME: loads and materializes the embed FS tree for built-ins at engine construction.
package tracker

import (
	"fmt"
	"io/fs"
	"path"

	"github.com/2389-research/tracker/pipeline"
)

// markBuiltinWorkflow tags a graph loaded from the embedded catalog with the
// built-in's bare name so engine construction can materialize its sidecar
// tree once a workdir is known (materializeBuiltinWorkflowDir). Nothing is
// written at load time — validate / simulate / DescribeInputs never touch
// disk.
func markBuiltinWorkflow(graph *pipeline.Graph, info WorkflowInfo) {
	if graph == nil || info.Name == "" {
		return
	}
	if graph.Attrs == nil {
		graph.Attrs = make(map[string]string)
	}
	graph.Attrs[pipeline.WorkflowBuiltinAttr] = info.Name
}

// builtinWorkflowSource returns the fs.FS and root directory holding built-in
// name. A package variable so tests can substitute a synthetic tree for the
// embed FS.
var builtinWorkflowSource = func(name string) (fs.FS, string, error) {
	info, ok := LookupWorkflow(name)
	if !ok {
		return nil, "", fmt.Errorf("no built-in workflow named %q", name)
	}
	return embeddedWorkflows, path.Dir(info.File), nil
}

// materializeBuiltinWorkflowDir gives an embedded built-in a real
// ${graph.workflow_dir}: the graph was marked at load (WorkflowBuiltinAttr)
// and now that the workdir is known its embedded tree — the .dip, prompts/,
// scripts/ and every directive sidecar — is copied to
// <workDir>/.tracker/workflow/<name>/ and WorkflowDirAttr set to that path.
// Runs on every engine construction (fresh run or resume) so the copy always
// matches the running binary; an author-declared workflow_dir wins, as for
// disk loads. Not a built-in: no-op.
//
// Scope: embedded built-ins ONLY. Their sidecars are go:embed'ded, i.e. the
// binary's own verified content, so materializing them has none of the
// supply-chain concern that keeps a packed .dipx fail-loud (#467, #430): a
// SHA-verified bundle must not source shell from an unverified sibling
// directory, and that behavior is deliberately unchanged here.
func materializeBuiltinWorkflowDir(graph *pipeline.Graph, workDir string) error {
	name := graph.Attrs[pipeline.WorkflowBuiltinAttr]
	if name == "" {
		return nil
	}
	fsys, root, err := builtinWorkflowSource(name)
	if err != nil {
		return err
	}
	return pipeline.MaterializeBuiltinWorkflowDir(graph, fsys, root, workDir)
}
