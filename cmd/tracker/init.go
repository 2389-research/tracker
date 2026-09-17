// ABOUTME: `tracker init` — copies a built-in .dip to cwd together with its sidecar files
// ABOUTME: (prompts/<name>/, scripts/<name>/) and scaffolds a starter SPEC.md where needed.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/2389-research/dippin-lang/parser"
	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline"
)

// sidecarFile is one embedded sidecar and where `tracker init` writes it. The
// destination is the directive path exactly as the .dip spells it (relative
// to the .dip's own directory), so the copied .dip's `prompt_file:` /
// `command_file:` lines resolve next to it exactly as they do inside
// examples/; embedPath is that same path inside the embed FS.
type sidecarFile struct {
	embedPath string // e.g. "examples/prompts/build_product/SpecLint.md"
	dest      string // e.g. "prompts/build_product/SpecLint.md" (slash-separated)
}

// embeddedSidecars lists the sidecar files a built-in workflow actually
// references — every *_file directive path in its parsed IR (tool
// command_file; agent prompt_file / system_prompt_file / prompt_include; the
// defaults-block prompt_prefix_file / prompt_suffix_file /
// system_prompt_file) — deduplicated and sorted by destination. The set is
// derived from the directives rather than from a prompts/<name>/ naming
// convention because a built-in may share a sidecar with another (superspec's
// SpecLint loads prompts/build_product/SpecLint.md). A built-in with no
// directives yields nil.
func embeddedSidecars(fsys fs.FS, info WorkflowInfo) ([]sidecarFile, error) {
	data, err := fs.ReadFile(fsys, info.File)
	if err != nil {
		return nil, fmt.Errorf("read embedded %s: %w", info.File, err)
	}
	wf, err := parser.NewParser(string(data), info.File).Parse()
	if err != nil {
		return nil, fmt.Errorf("parse embedded %s: %w", info.File, err)
	}
	baseDir := path.Dir(info.File)
	seen := map[string]bool{}
	var out []sidecarFile
	for _, p := range pipeline.WorkflowDirectivePaths(wf) {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, sidecarFile{embedPath: path.Join(baseDir, p), dest: p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].dest < out[j].dest })
	return out, nil
}

// writeSidecars copies every sidecar to its destination under cwd, creating
// directories as needed. Callers must have already refused any destination
// that exists (see executeInit) so this never overwrites a user's edits.
func writeSidecars(fsys fs.FS, files []sidecarFile) ([]string, error) {
	created := make([]string, 0, len(files))
	for _, f := range files {
		data, err := fs.ReadFile(fsys, f.embedPath)
		if err != nil {
			return created, fmt.Errorf("read embedded %s: %w", f.embedPath, err)
		}
		dest := filepath.FromSlash(f.dest)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return created, fmt.Errorf("create %s: %w", filepath.Dir(dest), err)
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return created, fmt.Errorf("write %s: %w", dest, err)
		}
		created = append(created, dest)
	}
	return created, nil
}

// initOutputs returns every path `tracker init <name>` would create — the
// .dip plus its sidecars — so the overwrite check can refuse up front, before
// anything is written.
func initOutputs(name string, sidecars []sidecarFile) []string {
	outs := []string{name + ".dip"}
	for _, f := range sidecars {
		outs = append(outs, filepath.FromSlash(f.dest))
	}
	return outs
}

// firstExisting returns the first path in paths that already exists on disk.
func firstExisting(paths []string) string {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// workflowSidecars is the production lookup over the binary's embed FS.
func workflowSidecars(info WorkflowInfo) ([]sidecarFile, error) {
	return embeddedSidecars(tracker.EmbeddedWorkflowFS(), info)
}

func executeInit(cfg runConfig) error {
	if cfg.pipelineFile == "" {
		return printInitUsage()
	}

	info, ok := lookupBuiltinWorkflow(cfg.pipelineFile)
	if !ok {
		return buildUnknownWorkflowError(cfg.pipelineFile)
	}

	// A built-in's *_file directives reference sidecar files relative to the
	// .dip (usually prompts/<name>/ and scripts/<name>/, but superspec shares
	// build_product's SpecLint.md), so exactly the referenced files are copied
	// alongside it — otherwise the copied .dip cannot load from disk.
	sidecars, err := workflowSidecars(info)
	if err != nil {
		return err
	}
	if existing := firstExisting(initOutputs(info.Name, sidecars)); existing != "" {
		return fmt.Errorf("%s already exists — remove it first or edit it directly", existing)
	}
	if err := writeInitFiles(info, sidecars); err != nil {
		return err
	}

	// Scaffold a starter SPEC.md for workflows that require one, so the newcomer
	// path (init → edit → run) succeeds instead of hard-exiting on a missing spec
	// (#456). Never overwrites an existing spec.
	fileExists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	writeFile := func(p string, b []byte) error { return os.WriteFile(p, b, 0o644) }
	spec, err := scaffoldStarterSpec(info.Name, fileExists, writeFile)
	if err != nil {
		return fmt.Errorf("write starter %s: %w", starterSpecFile, err)
	}
	if spec != "" {
		fmt.Printf("Created %s — a starter spec; edit it to describe what you want built.\n", spec)
	}

	fmt.Printf("Next: edit the files above, then run: tracker %s\n", info.Name)
	return nil
}

// writeInitFiles writes the .dip and its sidecars to cwd, printing each path
// as it is created. The overwrite check has already run.
func writeInitFiles(info WorkflowInfo, sidecars []sidecarFile) error {
	data, _, err := tracker.OpenWorkflow(info.Name)
	if err != nil {
		return fmt.Errorf("read embedded workflow: %w", err)
	}
	outFile := info.Name + ".dip"
	if err := os.WriteFile(outFile, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outFile, err)
	}
	fmt.Printf("Created %s\n", outFile)
	created, err := writeSidecars(tracker.EmbeddedWorkflowFS(), sidecars)
	for _, c := range created {
		fmt.Printf("Created %s\n", c)
	}
	return err
}

// printInitUsage prints the usage and lists available workflows, then returns an error.
func printInitUsage() error {
	workflows := listBuiltinWorkflows()
	fmt.Fprintf(os.Stderr, "Usage: tracker init <workflow_name>\n\nCopies <workflow_name>.dip to the current directory, plus every prompt_file /\ncommand_file sidecar it references (e.g. prompts/<name>/, scripts/<name>/). Never overwrites.\n\nAvailable workflows:\n")
	for _, wf := range workflows {
		fmt.Fprintf(os.Stderr, "  %s\n", wf.Name)
	}
	return fmt.Errorf("workflow name required")
}
