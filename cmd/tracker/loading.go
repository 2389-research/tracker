// ABOUTME: Pipeline file loading — reads .dip or .dot files and converts to Graph.
// ABOUTME: Auto-detects format from extension; resolves and loads subgraph references recursively.
package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/2389-research/dippin-lang/ir"
	"github.com/2389-research/dippin-lang/parser"
	"github.com/2389-research/dippin-lang/validator"
	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline"
)

// detectPipelineFormat returns "dip" or "dot" based on file extension.
func detectPipelineFormat(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".dip":
		return "dip"
	case ".dot":
		return "dot"
	default:
		return "dip" // default to .dip format for unknown extensions
	}
}

// loadPipeline reads and parses a pipeline file, auto-detecting format from
// extension unless formatOverride is set. Emits a deprecation warning to stderr
// when the resolved format is "dot".
func loadPipeline(filename, formatOverride string) (*pipeline.Graph, error) {
	format := formatOverride
	if format == "" {
		format = detectPipelineFormat(filename)
	}

	if format == "dot" {
		emitDOTDeprecationWarning(os.Stderr)
	}

	fileBytes, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read pipeline file: %w", err)
	}

	var graph *pipeline.Graph
	switch format {
	case "dip":
		graph, err = loadDippinPipeline(string(fileBytes), filename)
	case "dot":
		graph, err = pipeline.ParseDOT(string(fileBytes))
	default:
		return nil, fmt.Errorf("unknown pipeline format: %q (valid: dip, dot)", format)
	}
	if err != nil {
		return nil, err
	}
	// Seed ${graph.workflow_dir} for the raw on-disk load (#332). Only this
	// path seeds it: a .dipx bundle is content-addressed with no stable dir
	// (guardPackedWorkflowDir fails loud, #430), and an embedded built-in is
	// instead marked by loadEmbeddedPipeline and materialized into the workdir
	// at engine construction.
	pipeline.SeedWorkflowDir(graph, filename)
	return graph, nil
}

// emitDOTDeprecationWarning prints a one-line warning that DOT is deprecated.
func emitDOTDeprecationWarning(w io.Writer) {
	fmt.Fprintln(w, "WARNING: DOT format is deprecated. Migrate pipelines to .dip format.")
}

// guardPackedWorkflowDir fails a packed (.dipx) run that references
// ${graph.workflow_dir} but has no seeded value. workflow_dir is the SOURCE
// .dip's directory (seeded by pipeline.SeedWorkflowDir); a content-addressed bundle has
// no stable source dir, so the value is absent and expands to "" — degrading a
// tool body like `. "${graph.workflow_dir}/scripts/x.sh"` to `. "/scripts/x.sh"`,
// which aborts mysteriously under `set -eu`. Fail loud, before any node runs,
// with actionable guidance instead of that silent degrade (#430). Only the run
// path calls this; validate/simulate stay non-fatal.
func guardPackedWorkflowDir(graph *pipeline.Graph, packed bool) error {
	if !packed || graph.Attrs["workflow_dir"] != "" {
		return nil
	}
	refs := pipeline.NodesReferencingWorkflowDir(graph)
	if len(refs) == 0 {
		return nil
	}
	return fmt.Errorf(
		"this workflow references ${graph.workflow_dir} (node(s): %s) but it is unavailable in a packed .dipx "+
			"run — workflow_dir resolves the SOURCE .dip's directory, which is not part of a content-addressed "+
			"bundle. Run the workflow from its source .dip directory (where workflow_dir is seeded), or remove the "+
			"${graph.workflow_dir} reference. See https://github.com/2389-research/tracker/issues/430",
		strings.Join(refs, ", "),
	)
}

// loadSubgraphs scans the graph for subgraph nodes and loads their referenced
// .dip files. Refs are resolved relative to the parent pipeline file's directory.
// Returns a map of ref → *Graph suitable for handlers.WithSubgraphs().
// Recursively loads nested subgraph refs.
func loadSubgraphs(graph *pipeline.Graph, parentFile string) (map[string]*pipeline.Graph, error) {
	parentDir := filepath.Dir(parentFile)
	subgraphs := make(map[string]*pipeline.Graph)
	return subgraphs, loadSubgraphsRecursive(graph, parentDir, subgraphs, make(map[string]bool))
}

func loadSubgraphsRecursive(graph *pipeline.Graph, baseDir string, subgraphs map[string]*pipeline.Graph, visited map[string]bool) error {
	for _, node := range graph.Nodes {
		if err := loadSubgraphNode(node, baseDir, subgraphs, visited); err != nil {
			return err
		}
	}
	return nil
}

// loadSubgraphNode loads the subgraph referenced by a single node (if any) and recurses.
func loadSubgraphNode(node *pipeline.Node, baseDir string, subgraphs map[string]*pipeline.Graph, visited map[string]bool) error {
	ref := node.Attrs["subgraph_ref"]
	if ref == "" || subgraphs[ref] != nil {
		return nil
	}

	resolved, err := resolveSubgraphPath(ref, baseDir)
	if err != nil {
		return fmt.Errorf("subgraph ref %q from node %q: %w", ref, node.ID, err)
	}

	absResolved, err := filepath.Abs(resolved)
	if err != nil {
		absResolved = resolved
	}
	if visited[absResolved] {
		return fmt.Errorf("circular subgraph reference detected: %q resolves to %q which is already being loaded (cycle)", ref, absResolved)
	}
	visited[absResolved] = true
	defer delete(visited, absResolved)

	subGraph, err := loadPipeline(resolved, "")
	if err != nil {
		return fmt.Errorf("load subgraph %q (node %q): %w", ref, node.ID, err)
	}
	subgraphs[ref] = subGraph

	return loadSubgraphsRecursive(subGraph, filepath.Dir(resolved), subgraphs, visited)
}

// validateSubgraphRefs checks that every subgraph node in the graph has a
// corresponding entry in the loaded subgraphs map. Catches missing refs early
// instead of failing at execution time.
func validateSubgraphRefs(graph *pipeline.Graph, subgraphs map[string]*pipeline.Graph) error {
	for _, node := range graph.Nodes {
		if node.Handler != "subgraph" {
			continue
		}
		ref := node.Attrs["subgraph_ref"]
		if ref == "" {
			return fmt.Errorf("subgraph node %q has no subgraph_ref attribute", node.ID)
		}
		if subgraphs[ref] == nil {
			return fmt.Errorf("subgraph node %q references %q but it was not loaded", node.ID, ref)
		}
	}
	return nil
}

// resolveSubgraphPath finds the file for a subgraph ref. Tries (in order):
// 1. Relative to parent dir (ref as-is)
// 2. Relative to parent dir with .dip extension appended
// 3. Ref as-is from cwd
// 4. Ref with .dip extension from cwd
func resolveSubgraphPath(ref, baseDir string) (string, error) {
	candidates := []string{
		filepath.Join(baseDir, ref),
		filepath.Join(baseDir, ref+".dip"),
		ref,
		ref + ".dip",
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("file not found (tried: %s, %s, %s, %s)",
		candidates[0], candidates[1], candidates[2], candidates[3])
}

// loadEmbeddedPipeline reads a .dip file from the embedded workflows FS
// and parses it through the standard dippin pipeline loader, resolving its
// prompt_file / command_file sidecars from the same embed FS (a built-in's
// sidecars ship inside the binary, not on disk).
//
// The graph is marked with pipeline.WorkflowBuiltinAttr (the bare name) so
// the engine can materialize the built-in's tree into the workdir and give it
// a ${graph.workflow_dir}; nothing is written at load time, so validate /
// simulate on a bare name leave no .tracker/ behind.
func loadEmbeddedPipeline(info WorkflowInfo) (*pipeline.Graph, error) {
	data, _, err := tracker.OpenWorkflow(info.Name)
	if err != nil {
		return nil, fmt.Errorf("read embedded workflow %s: %w", info.Name, err)
	}
	graph, err := loadDippinPipelineFS(string(data), info.File, tracker.EmbeddedWorkflowFS())
	if err != nil {
		return nil, err
	}
	graph.Attrs[pipeline.WorkflowBuiltinAttr] = info.Name
	return graph, nil
}

// loadDippinPipeline parses an on-disk .dip file using dippin-lang parser,
// runs Dippin's built-in validator and linter, then converts to Tracker's
// Graph representation. Validation errors are fatal; lint warnings are
// printed to stderr but do not block execution. File directives resolve from
// disk relative to the file's directory.
func loadDippinPipeline(source, filename string) (*pipeline.Graph, error) {
	return loadDippinPipelineFS(source, filename, nil)
}

// loadDippinPipelineFS is loadDippinPipeline with the directive source made
// explicit: nil fsys resolves *_file directives from disk (dippin's own
// resolver); a non-nil fsys resolves them inside that FS relative to
// path.Dir(filename) — the embedded built-ins.
func loadDippinPipelineFS(source, filename string, fsys fs.FS) (*pipeline.Graph, error) {
	// Record the IR for run capture. dippin expands subgraphs at compile time,
	// so the expanded graph is the only form that explains the run's events —
	// the authored source alone does not. Parse failures are ignored here and
	// left to LoadDippinWorkflow below, which owns error reporting; capture
	// simply records nothing in that case.
	if workflow, perr := parser.NewParser(source, filename).Parse(); perr == nil {
		// Mirrors LoadDippinWorkflow: parser entry points do not resolve file
		// directives, and tracker is a CLI entry point.
		if rerr := resolveDirectives(workflow, filename, fsys); rerr == nil {
			recordExecutedSpec(filename, source, workflow)
		}
	}

	var (
		graph *pipeline.Graph
		diags []validator.Diagnostic
		err   error
	)
	if fsys != nil {
		graph, diags, err = pipeline.LoadDippinWorkflowFS(source, filename, fsys)
	} else {
		graph, diags, err = pipeline.LoadDippinWorkflow(source, filename)
	}
	// Log validation errors and lint warnings before returning so users
	// see the specific diagnostics even on fatal failures.
	printLoadDiagnostics(diags)
	if err != nil {
		return nil, err
	}
	return graph, nil
}

// printLoadDiagnostics writes every dippin diagnostic (errors, warnings and
// hints) to stderr. Hints used to be dropped here because DIP125's PATH probe
// mis-read `${graph.x}` placeholders (dippin-lang#305, fixed v0.75.0) and then
// flagged the `:` builtin and every shell function the shipped pipelines load
// via `. "$LIB/x.sh"` (dippin-lang#315, fixed v0.76.0) — 25 bogus hints
// across the three built-ins. With both fixed the built-ins load DIP125-clean
// (pinned by TestLoadEmbeddedBuiltins_NoDIP125Hints), so hints print again;
// No shipped built-in currently emits a hint — build_product's former DIP165
// (FinalCommit's `writable_paths_mode: prefer`) is gone since #656 made
// FinalCommit a tool node; the test tolerates a DIP165 hint but requires none.
func printLoadDiagnostics(diags []validator.Diagnostic) {
	for _, d := range diags {
		fmt.Fprintln(os.Stderr, d.String())
	}
}

// resolveDirectives resolves *_file directives on a parsed workflow from disk
// (nil fsys) or from fsys, anchored at the file's directory either way.
func resolveDirectives(workflow *ir.Workflow, filename string, fsys fs.FS) error {
	if fsys != nil {
		return pipeline.ResolveFileDirectivesFS(workflow, fsys, path.Dir(filepath.ToSlash(filename)))
	}
	return parser.ResolveFileDirectives(workflow, filepath.Dir(filename))
}

// loadDipxPipeline reads a .dipx bundle, verifies hashes, and converts the
// entry + every transitively-referenced workflow to tracker Graphs. Lint
// diagnostics from the bundled IR are printed to stderr here so the library
// (pipeline.LoadDipxBundle) stays free of os.Stderr side effects; this
// mirrors what loadDippinPipeline does for the .dip path.
func loadDipxPipeline(filename string) (*pipeline.Graph, map[string]*pipeline.Graph, pipeline.BundleInfo, error) {
	graph, subgraphs, info, diags, err := pipeline.LoadDipxBundle(context.Background(), filename)
	printLoadDiagnostics(diags)
	return graph, subgraphs, info, err
}

// loadPipelineAndBundle is the loader entry point that handles both .dip
// (filesystem + recursive subgraph walker) and .dipx (sealed bundle, pre-
// resolved subgraphs). Always returns the subgraphs map and BundleInfo;
// .dip callers see an empty BundleInfo and a subgraph map populated from
// disk, while .dipx callers see a populated BundleInfo and subgraphs
// pre-resolved by dipx.
func loadPipelineAndBundle(filename, formatOverride string) (*pipeline.Graph, map[string]*pipeline.Graph, pipeline.BundleInfo, error) {
	if strings.EqualFold(filepath.Ext(filename), ".dipx") {
		return loadDipxPipeline(filename)
	}
	graph, err := loadPipeline(filename, formatOverride)
	if err != nil {
		return nil, nil, pipeline.BundleInfo{}, err
	}
	subgraphs, err := loadSubgraphs(graph, filename)
	if err != nil {
		return nil, nil, pipeline.BundleInfo{}, err
	}
	return graph, subgraphs, pipeline.BundleInfo{}, nil
}
