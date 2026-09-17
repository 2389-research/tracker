// ABOUTME: `tracker init` — copies a built-in .dip to cwd together with its sidecar files
// ABOUTME: (prompts/<name>/, scripts/<name>/) and scaffolds a starter SPEC.md where needed.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"

	tracker "github.com/2389-research/tracker"
)

// sidecarFile is one embedded sidecar and where `tracker init` writes it. The
// destination is the embed path minus its "examples/" root, so the copied
// .dip's relative `prompt_file: prompts/<name>/X.md` resolves next to it
// exactly as it does inside examples/.
type sidecarFile struct {
	embedPath string // e.g. "examples/prompts/build_product/SpecLint.md"
	dest      string // e.g. "prompts/build_product/SpecLint.md" (slash-separated)
}

// embeddedSidecars lists the sidecar files shipped for a built-in workflow,
// sorted by destination. A built-in with no sidecar directories yields nil.
func embeddedSidecars(fsys fs.FS, name string) ([]sidecarFile, error) {
	var out []sidecarFile
	for _, kind := range []string{"prompts", "scripts"} {
		files, err := walkSidecarDir(fsys, kind, name)
		if err != nil {
			return nil, err
		}
		out = append(out, files...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].dest < out[j].dest })
	return out, nil
}

// walkSidecarDir collects the files under examples/<kind>/<name> in fsys; a
// missing directory is simply "no sidecars of this kind".
func walkSidecarDir(fsys fs.FS, kind, name string) ([]sidecarFile, error) {
	root := path.Join("examples", kind, name)
	if _, err := fs.Stat(fsys, root); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat embedded %s: %w", root, err)
	}
	var out []sidecarFile
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		out = append(out, sidecarFile{embedPath: p, dest: path.Join(kind, name, p[len(root)+1:])})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk embedded %s: %w", root, err)
	}
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
func workflowSidecars(name string) ([]sidecarFile, error) {
	return embeddedSidecars(tracker.EmbeddedWorkflowFS(), name)
}

func executeInit(cfg runConfig) error {
	if cfg.pipelineFile == "" {
		return printInitUsage()
	}

	info, ok := lookupBuiltinWorkflow(cfg.pipelineFile)
	if !ok {
		return buildUnknownWorkflowError(cfg.pipelineFile)
	}

	// A built-in's prompt_file / command_file directives point at
	// prompts/<name>/ and scripts/<name>/ next to the .dip, so those trees are
	// copied alongside it — otherwise the copied .dip cannot load from disk.
	sidecars, err := workflowSidecars(info.Name)
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
	fmt.Fprintf(os.Stderr, "Usage: tracker init <workflow_name>\n\nCopies <workflow_name>.dip to the current directory, plus any prompts/<workflow_name>/\nand scripts/<workflow_name>/ sidecar files it references. Never overwrites.\n\nAvailable workflows:\n")
	for _, wf := range workflows {
		fmt.Fprintf(os.Stderr, "  %s\n", wf.Name)
	}
	return fmt.Errorf("workflow name required")
}
