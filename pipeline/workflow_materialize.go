// ABOUTME: Materializes an embedded built-in workflow's sidecar tree into the run's
// ABOUTME: workdir so ${graph.workflow_dir} resolves for a workflow with no on-disk dir.
package pipeline

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/2389-research/dippin-lang/ir"
	"github.com/2389-research/dippin-lang/parser"
)

const (
	// WorkflowDirAttr is the graph attr behind ${graph.workflow_dir}: the
	// directory against which a workflow's sibling files resolve. Contract:
	// `${graph.workflow_dir}/<relpath>` resolves a workflow-relative file; the
	// concrete path is implementation-defined (the source .dip's directory for a
	// disk load, a per-run materialized copy for an embedded built-in) and must
	// not be relied on by workflow authors.
	WorkflowDirAttr = "workflow_dir"

	// WorkflowBuiltinAttr marks a graph loaded from tracker's embedded built-in
	// catalog with the built-in's bare name (e.g. "build_product"). Set at load
	// time (no workdir exists yet); consumed at engine construction, where
	// MaterializeBuiltinWorkflowDir turns it into a WorkflowDirAttr value.
	WorkflowBuiltinAttr = "workflow_builtin"

	// workflowMaterializeDir is where an embedded built-in's tree is copied,
	// relative to the workdir. It is under .tracker/ so the artifact repo's
	// local git exclude (#351/#553) keeps it out of commits and bundles.
	workflowMaterializeDir = ".tracker/workflow"
)

// SeedWorkflowDir sets WorkflowDirAttr to the absolute parent directory of the
// pipeline file, so authors can reference sibling files via
// ${graph.workflow_dir} in prompts and tool_commands (#332). The seeded value
// is a literal path — variable expansion is single-pass, so it is never
// re-scanned. No-op if the attr is already present (an author-declared value
// wins — including an explicit empty one, which ParseDOT can carry from a
// .dot graph attr) or if the path can't be made absolute.
//
// Only real on-disk loads seed this. A packed .dipx bundle is content-
// addressed and has no stable source dir, so it deliberately gets no value
// and the CLI fails loud when it references the attr (#430, #467). An
// embedded built-in has no directory either, but its sidecars are the
// binary's own verified content, so it is materialized into the workdir at
// engine construction instead — see MaterializeBuiltinWorkflowDir.
func SeedWorkflowDir(g *Graph, filename string) {
	if _, ok := g.Attrs[WorkflowDirAttr]; ok {
		return
	}
	abs, err := filepath.Abs(filename)
	if err != nil {
		return
	}
	g.Attrs[WorkflowDirAttr] = filepath.Dir(abs)
}

// MaterializeBuiltinWorkflowDir resolves ${graph.workflow_dir} for a graph
// loaded from an embedded built-in (WorkflowBuiltinAttr set): it copies the
// built-in's tree out of fsys (rooted at root, e.g. "examples") into
// <workDir>/.tracker/workflow/<name>/ and sets WorkflowDirAttr to that
// absolute path. A graph without WorkflowBuiltinAttr, or with an
// author-declared WorkflowDirAttr (presence wins, as in SeedWorkflowDir), is
// left untouched and nothing is written.
//
// The copy is refreshed on every call (every engine construction, including
// a checkpoint resume): the content comes from the running binary and must
// never be stale. A user who wants to customize a built-in runs
// `tracker init <name>` and gets an ordinary disk load instead.
func MaterializeBuiltinWorkflowDir(g *Graph, fsys fs.FS, root, workDir string) error {
	name := g.Attrs[WorkflowBuiltinAttr]
	if name == "" {
		return nil
	}
	if _, declared := g.Attrs[WorkflowDirAttr]; declared {
		return nil
	}
	dir, err := MaterializeWorkflow(fsys, root, name, workDir)
	if err != nil {
		return fmt.Errorf("materialize built-in workflow %q: %w", name, err)
	}
	g.Attrs[WorkflowDirAttr] = dir
	return nil
}

// MaterializeWorkflow copies built-in workflow name out of fsys into
// <workDir>/.tracker/workflow/<name>/ and returns that directory's absolute
// path. root is the directory inside fsys that holds <name>.dip (the anchor
// its *_file directives resolve against); the copy preserves the layout
// relative to root, so `${graph.workflow_dir}/<name>.dip` is the workflow and
// `${graph.workflow_dir}/scripts/<name>/lib/x.sh` is a sidecar.
//
// What is copied: <name>.dip; everything under root/prompts/<name>/ and
// root/scripts/<name>/ (so helpers that no directive names — sourced lib/ scripts —
// are reachable, which is the whole point); and every *_file directive path
// the .dip declares, wherever it lives under root (a built-in may share a
// sidecar in another built-in's directory). Other built-ins' files are not
// copied.
//
// The tree is replaced, never merged: files are written into a fresh staging
// directory next to the destination, then swapped in with a rename after the
// previous copy is removed, so a stale leftover from an older binary cannot
// survive. Files are 0644 (scripts are sourced or `sh`'d by tool commands,
// never exec'd) and directories 0755. Every destination component under
// workDir (.tracker, .tracker/workflow, .tracker/workflow/<name>) is refused
// if it is a symlink, and each file is opened O_NOFOLLOW|O_EXCL, mirroring
// StageInputFile and the activity-log snapshot guards.
//
// The `writable_paths` Landlock jail bounds only WRITES (it grants read-only
// access to the whole filesystem), so a jailed tool node can still source
// from the materialized directory even when it lies outside its writable
// globs.
func MaterializeWorkflow(fsys fs.FS, root, name, workDir string) (string, error) {
	if err := validateWorkflowName(name); err != nil {
		return "", err
	}
	files, err := collectWorkflowFiles(fsys, root, name)
	if err != nil {
		return "", err
	}
	base, dest, err := prepareMaterializeDest(workDir, name)
	if err != nil {
		return "", err
	}
	if err := installWorkflowTree(fsys, root, base, dest, name, files); err != nil {
		return "", err
	}
	return filepath.Abs(dest)
}

// prepareMaterializeDest refuses a symlink at any destination component under
// workDir, then ensures <workDir>/.tracker/workflow exists. Returns that base
// and the final <base>/<name> destination.
func prepareMaterializeDest(workDir, name string) (base, dest string, err error) {
	base = filepath.Join(workDir, filepath.FromSlash(workflowMaterializeDir))
	dest = filepath.Join(base, name)
	for _, p := range []string{filepath.Join(workDir, ".tracker"), base, dest} {
		if err := refuseIfSymlink(p); err != nil {
			return "", "", err
		}
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", "", err
	}
	return base, dest, nil
}

// installWorkflowTree writes files into a fresh staging dir under base, then
// swaps it into dest. The staging dir is removed on any failure.
func installWorkflowTree(fsys fs.FS, root, base, dest, name string, files []string) error {
	staging, err := os.MkdirTemp(base, "."+name+".tmp-")
	if err != nil {
		return err
	}
	if err := writeWorkflowTree(fsys, root, staging, files); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	if err := swapWorkflowTree(staging, dest); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	return nil
}

// validateWorkflowName rejects anything that is not a single safe path
// segment, so the destination can never escape .tracker/workflow/.
func validateWorkflowName(name string) error {
	if name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("invalid workflow name %q (must be a single path segment)", name)
	}
	return nil
}

// collectWorkflowFiles returns the sorted root-relative paths that make up
// built-in name: the .dip, its prompts/<name> and scripts/<name> subtrees, and
// every directive-referenced sidecar.
func collectWorkflowFiles(fsys fs.FS, root, name string) ([]string, error) {
	dip := name + ".dip"
	data, err := fs.ReadFile(fsys, path.Join(root, dip))
	if err != nil {
		return nil, fmt.Errorf("read built-in %s: %w", dip, err)
	}
	set := map[string]bool{dip: true}
	for _, sub := range []string{path.Join("prompts", name), path.Join("scripts", name)} {
		if err := walkWorkflowSubtree(fsys, root, sub, set); err != nil {
			return nil, err
		}
	}
	if err := addDirectivePaths(string(data), dip, set); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// addDirectivePaths parses the .dip source and adds every *_file directive
// path to set. A path that is not a clean root-relative path (absolute, `..`
// escape, or empty) is refused rather than copied.
func addDirectivePaths(source, dip string, set map[string]bool) error {
	wf, err := parser.NewParser(source, dip).Parse()
	if err != nil {
		return fmt.Errorf("parse built-in %s: %w", dip, err)
	}
	for _, p := range WorkflowDirectivePaths(wf) {
		clean := path.Clean(p)
		if !fs.ValidPath(clean) || clean == "." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("built-in %s: directive path %q escapes the workflow root", dip, p)
		}
		set[clean] = true
	}
	return nil
}

// walkWorkflowSubtree adds every regular file under root/sub to set (as
// root-relative paths). A missing subtree is not an error — a built-in
// without prompts or scripts is fine.
func walkWorkflowSubtree(fsys fs.FS, root, sub string, set map[string]bool) error {
	full := path.Join(root, sub)
	if _, err := fs.Stat(fsys, full); err != nil {
		return nil
	}
	return fs.WalkDir(fsys, full, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			set[strings.TrimPrefix(p, root+"/")] = true
		}
		return nil
	})
}

// writeWorkflowTree copies each root-relative file from fsys into staging,
// creating 0755 directories and 0644 files. staging is a fresh private
// directory, but every file is still opened O_EXCL|O_NOFOLLOW so nothing can
// be redirected through it.
func writeWorkflowTree(fsys fs.FS, root, staging string, files []string) error {
	for _, rel := range files {
		data, err := fs.ReadFile(fsys, path.Join(root, rel))
		if err != nil {
			return fmt.Errorf("read built-in file %s: %w", rel, err)
		}
		dst := filepath.Join(staging, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := writeExclusiveFile(dst, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
	}
	return nil
}

// writeExclusiveFile creates path (which must not exist) with the given mode,
// force-tightened after creation because O_CREATE's perm is subject to umask.
func writeExclusiveFile(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|snapshotNoFollow, perm)
	if err != nil {
		return err
	}
	_ = f.Chmod(perm)
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// swapWorkflowTree replaces dest with staging. dest was Lstat-checked as a
// non-symlink by the caller, so RemoveAll is bounded to the previous copy.
func swapWorkflowTree(staging, dest string) error {
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("remove previous materialization %s: %w", dest, err)
	}
	if err := os.Rename(staging, dest); err != nil {
		return fmt.Errorf("install materialization %s: %w", dest, err)
	}
	return nil
}

// WorkflowDirectivePaths returns every *_file directive path declared in a
// parsed (unresolved) workflow — tool command_file; agent prompt_file /
// system_prompt_file / prompt_include; the defaults-block prompt_prefix_file /
// prompt_suffix_file / system_prompt_file — in declaration order, empty
// entries skipped, duplicates kept. Shared by `tracker init` (which copies
// exactly these files next to the .dip) and MaterializeWorkflow.
func WorkflowDirectivePaths(wf *ir.Workflow) []string {
	var paths []string
	add := func(ps ...string) {
		for _, p := range ps {
			if p != "" {
				paths = append(paths, p)
			}
		}
	}
	add(wf.Defaults.PromptPrefixFile, wf.Defaults.PromptSuffixFile, wf.Defaults.SystemPromptFile)
	for _, n := range wf.Nodes {
		switch cfg := n.Config.(type) {
		case ir.ToolConfig:
			add(cfg.CommandFile)
		case ir.AgentConfig:
			add(cfg.PromptFile, cfg.SystemPromptFile, cfg.PromptInclude)
		}
	}
	return paths
}
