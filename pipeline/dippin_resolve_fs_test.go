// ABOUTME: Drift guard for ResolveFileDirectivesFS against dippin's disk resolver.
// ABOUTME: Parity over the real build_product example plus a synthetic cascade workflow, and path rejection.
package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/2389-research/dippin-lang/ir"
	"github.com/2389-research/dippin-lang/parser"
)

// resolvedBodies returns every node's resolved Prompt / SystemPrompt / Command
// keyed by "<nodeID>.<field>" so two resolutions can be compared byte-for-byte.
func resolvedBodies(t *testing.T, w *ir.Workflow) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, n := range w.Nodes {
		switch c := n.Config.(type) {
		case ir.AgentConfig:
			out[n.ID+".prompt"] = c.Prompt
			out[n.ID+".system_prompt"] = c.SystemPrompt
		case ir.ToolConfig:
			out[n.ID+".command"] = c.Command
		}
	}
	return out
}

// assertFSParityWithDisk parses source twice: once through dippin's disk
// resolver anchored at dir, once through ResolveFileDirectivesFS over
// os.DirFS(dir) with baseDir ".", and requires identical resolved bodies.
func assertFSParityWithDisk(t *testing.T, source, filename, dir string) map[string]string {
	t.Helper()
	disk, err := parser.NewParser(source, filename).Parse()
	if err != nil {
		t.Fatalf("parse (disk): %v", err)
	}
	if err := parser.ResolveFileDirectives(disk, dir); err != nil {
		t.Fatalf("dippin ResolveFileDirectives: %v", err)
	}
	viaFS, err := parser.NewParser(source, filename).Parse()
	if err != nil {
		t.Fatalf("parse (fs): %v", err)
	}
	if err := ResolveFileDirectivesFS(viaFS, os.DirFS(dir), "."); err != nil {
		t.Fatalf("ResolveFileDirectivesFS: %v", err)
	}
	want, got := resolvedBodies(t, disk), resolvedBodies(t, viaFS)
	if len(want) != len(got) {
		t.Fatalf("body count mismatch: disk=%d fs=%d", len(want), len(got))
	}
	for k, w := range want {
		if g := got[k]; g != w {
			t.Errorf("%s differs between disk and fs resolution:\n--- disk ---\n%s\n--- fs ---\n%s", k, w, g)
		}
	}
	return got
}

func TestResolveFileDirectivesFS_ParityWithDisk_BuildProduct(t *testing.T) {
	file := filepath.Join("..", "examples", "build_product.dip")
	source, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	bodies := assertFSParityWithDisk(t, string(source), file, filepath.Dir(file))
	if p := bodies["SpecLint.prompt"]; !strings.Contains(p, "SPEC COHERENCE PREFLIGHT") {
		t.Errorf("SpecLint prompt did not resolve: %q", p)
	}
}

// Synthetic workflow covering every cascade branch: defaults prefix/suffix
// files, the shared system_prompt_file fallback (#72), prompt_include (#175),
// `prompt_prefix: none` / `prompt_suffix: none` opt-outs, a node with its own
// system_prompt_file (wins over the default), a command_file tool, and a
// body-less passthrough agent that must stay body-less (#248).
const cascadeWorkflow = `workflow Cascade
  goal: "cascade parity"
  start: Start
  exit: Done

  defaults
    prompt_prefix_file: frag/prefix.md
    prompt_suffix_file: frag/suffix.md
    system_prompt_file: frag/system.md

  agent Start
    label: Start

  agent Done
    label: Done

  agent Plain
    prompt_file: prompts/plain.md

  agent WithInclude
    prompt_file: prompts/plain.md
    prompt_include: frag/include.md

  agent NoPrefix
    prompt_prefix: none
    prompt_file: prompts/plain.md

  agent NoSuffix
    prompt_suffix: none
    prompt_file: prompts/plain.md

  agent OwnSystem
    system_prompt_file: prompts/own_system.md
    prompt: "inline body"

  agent IncludeOnly
    prompt_include: frag/include.md

  tool Script
    command_file: scripts/run.sh

  edges
    Start -> Plain
    Plain -> WithInclude
    WithInclude -> NoPrefix
    NoPrefix -> NoSuffix
    NoSuffix -> OwnSystem
    OwnSystem -> IncludeOnly
    IncludeOnly -> Script
    Script -> Done
`

func writeCascadeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"frag/prefix.md":        "PREFIX LINE\n",
		"frag/suffix.md":        "SUFFIX LINE  \n\n",
		"frag/system.md":        "shared system prompt\n",
		"frag/include.md":       "included fragment\n",
		"prompts/plain.md":      "plain body\nsecond line\n",
		"prompts/own_system.md": "own system prompt",
		"scripts/run.sh":        "set -eu\nprintf 'ok'\n",
	}
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestResolveFileDirectivesFS_ParityWithDisk_Cascade(t *testing.T) {
	dir := writeCascadeFixture(t)
	bodies := assertFSParityWithDisk(t, cascadeWorkflow, filepath.Join(dir, "cascade.dip"), dir)

	// Spot-check the branches actually fired (not just that both sides agree).
	checks := map[string]func(string) bool{
		"Plain.prompt":            func(s string) bool { return strings.HasPrefix(s, "PREFIX LINE") && strings.HasSuffix(s, "SUFFIX LINE") },
		"WithInclude.prompt":      func(s string) bool { return strings.Contains(s, "included fragment") },
		"NoPrefix.prompt":         func(s string) bool { return strings.HasPrefix(s, "plain body") && strings.HasSuffix(s, "SUFFIX LINE") },
		"NoSuffix.prompt":         func(s string) bool { return strings.HasPrefix(s, "PREFIX LINE") && strings.HasSuffix(s, "second line") },
		"Plain.system_prompt":     func(s string) bool { return s == "shared system prompt\n" },
		"OwnSystem.system_prompt": func(s string) bool { return s == "own system prompt" },
		"OwnSystem.prompt":        func(s string) bool { return strings.Contains(s, "inline body") },
		"IncludeOnly.prompt":      func(s string) bool { return strings.Contains(s, "included fragment") },
		"Start.prompt":            func(s string) bool { return s == "" },
		"Done.prompt":             func(s string) bool { return s == "" },
		"Script.command":          func(s string) bool { return s == "set -eu\nprintf 'ok'\n" },
	}
	for key, ok := range checks {
		if !ok(bodies[key]) {
			t.Errorf("%s: unexpected resolved value %q", key, bodies[key])
		}
	}
}

func TestResolveFileDirectivesFS_RejectsEscapes(t *testing.T) {
	fsys := fstest.MapFS{
		"examples/prompts/x.md": {Data: []byte("x")},
		"secret.md":             {Data: []byte("secret")},
	}
	cases := map[string]string{
		"parent escape": "../secret.md",
		"nested parent": "prompts/../../secret.md",
		"absolute":      "/etc/passwd",
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			w := &ir.Workflow{Nodes: []*ir.Node{{ID: "A", Config: ir.AgentConfig{PromptFile: p}}}}
			err := ResolveFileDirectivesFS(w, fsys, "examples")
			if err == nil {
				t.Fatalf("expected rejection for %q", p)
			}
			if !strings.Contains(err.Error(), `node "A" prompt_file`) {
				t.Errorf("error should name the node and directive: %v", err)
			}
			if got := w.Nodes[0].Config.(ir.AgentConfig).Prompt; got != "" {
				t.Errorf("prompt must stay empty on rejection, got %q", got)
			}
		})
	}
}

func TestResolveFileDirectivesFS_MissingFile(t *testing.T) {
	fsys := fstest.MapFS{}
	w := &ir.Workflow{Nodes: []*ir.Node{{ID: "T", Config: ir.ToolConfig{CommandFile: "scripts/none.sh"}}}}
	err := ResolveFileDirectivesFS(w, fsys, "examples")
	if err == nil || !strings.Contains(err.Error(), `node "T" command_file: file "scripts/none.sh" not found`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadDippinWorkflowFS_AnchorsAtFileDir(t *testing.T) {
	fsys := fstest.MapFS{
		"examples/prompts/demo/Ask.md": {Data: []byte("ask something")},
	}
	src := "workflow Demo\n  goal: \"g\"\n  start: Start\n  exit: Done\n\n  agent Start\n    label: Start\n\n  agent Done\n    label: Done\n\n  agent Ask\n    prompt_file: prompts/demo/Ask.md\n\n  edges\n    Start -> Ask\n    Ask -> Done\n"
	graph, _, err := LoadDippinWorkflowFS(src, "examples/demo.dip", fsys)
	if err != nil {
		t.Fatalf("LoadDippinWorkflowFS: %v", err)
	}
	if got := graph.Nodes["Ask"].Attrs["prompt"]; got != "ask something" {
		t.Errorf("prompt = %q", got)
	}
}
