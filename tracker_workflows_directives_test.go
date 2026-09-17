// ABOUTME: Guard test — every *_file directive in an embedded built-in must
// ABOUTME: resolve inside the embed FS, since no sibling files exist on disk at runtime.
package tracker

import (
	"path"
	"testing"

	"github.com/2389-research/dippin-lang/ir"
	"github.com/2389-research/dippin-lang/parser"
	"github.com/2389-research/tracker/pipeline"
)

// TestEmbeddedWorkflows_DirectivesResolveFromEmbedFS keeps embedded built-ins
// loadable outside the repo. A built-in may use command_file / prompt_file /
// system_prompt_file / prompt_include, but only for files that are part of the
// embed FS (tracker_workflows.go's go:embed list) — the embedded loader never
// touches disk, so a directive pointing at an un-embedded sidecar would make
// `tracker run <name>` fail everywhere except the repo checkout. The check is
// the real resolver over the real FS: every directive must load, and every
// directive-bearing node must end up with non-empty content.
func TestEmbeddedWorkflows_DirectivesResolveFromEmbedFS(t *testing.T) {
	workflows := Workflows()
	if len(workflows) == 0 {
		t.Fatal("no embedded workflows found")
	}
	for _, info := range workflows {
		data, _, err := OpenWorkflow(info.Name)
		if err != nil {
			t.Fatalf("OpenWorkflow(%q): %v", info.Name, err)
		}
		wf, err := parser.NewParser(string(data), info.File).Parse()
		if err != nil {
			t.Fatalf("parse embedded workflow %q: %v", info.Name, err)
		}
		if err := pipeline.ResolveFileDirectivesFS(wf, embeddedWorkflows, path.Dir(info.File)); err != nil {
			t.Errorf("workflow %q: a *_file directive does not resolve inside the embed FS — add its sidecar dir to the go:embed list in tracker_workflows.go: %v", info.Name, err)
			continue
		}
		for _, n := range wf.Nodes {
			switch cfg := n.Config.(type) {
			case ir.ToolConfig:
				if cfg.CommandFile != "" && cfg.Command == "" {
					t.Errorf("workflow %q node %q: command_file %q resolved to empty content", info.Name, n.ID, cfg.CommandFile)
				}
			case ir.AgentConfig:
				if cfg.PromptFile != "" && cfg.Prompt == "" {
					t.Errorf("workflow %q node %q: prompt_file %q resolved to empty content", info.Name, n.ID, cfg.PromptFile)
				}
				if cfg.SystemPromptFile != "" && cfg.SystemPrompt == "" {
					t.Errorf("workflow %q node %q: system_prompt_file %q resolved to empty content", info.Name, n.ID, cfg.SystemPromptFile)
				}
			}
		}
	}
}

// TestEmbeddedBuildProductUsesSidecars pins that build_product actually ships
// in the sidecar layout (#398) and that the superspec variant shares its
// SpecLint prompt file — the by-construction half of the SpecLint parity guard.
func TestEmbeddedBuildProductUsesSidecars(t *testing.T) {
	directive := func(name, node string) (promptFile, commandFile string) {
		data, _, err := OpenWorkflow(name)
		if err != nil {
			t.Fatal(err)
		}
		wf, err := parser.NewParser(string(data), name+".dip").Parse()
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range wf.Nodes {
			if n.ID != node {
				continue
			}
			switch cfg := n.Config.(type) {
			case ir.AgentConfig:
				return cfg.PromptFile, ""
			case ir.ToolConfig:
				return "", cfg.CommandFile
			}
		}
		t.Fatalf("%s: node %q not found", name, node)
		return "", ""
	}
	if pf, _ := directive("build_product", "SpecLint"); pf != "prompts/build_product/SpecLint.md" {
		t.Errorf("build_product SpecLint prompt_file = %q", pf)
	}
	if pf, _ := directive("build_product_with_superspec", "SpecLint"); pf != "prompts/build_product/SpecLint.md" {
		t.Errorf("superspec SpecLint must share build_product's sidecar, got %q", pf)
	}
	if _, cf := directive("build_product", "Setup"); cf != "scripts/build_product/Setup.sh" {
		t.Errorf("build_product Setup command_file = %q", cf)
	}
}
