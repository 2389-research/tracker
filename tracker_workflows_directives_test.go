// ABOUTME: Guard test — embedded built-in workflows must not use file
// ABOUTME: directives (command_file etc.); sibling files don't ship in the binary.
package tracker

import (
	"testing"

	"github.com/2389-research/dippin-lang/ir"
	"github.com/2389-research/dippin-lang/parser"
)

// TestEmbeddedWorkflows_NoFileDirectives keeps embedded built-ins free of
// command_file / prompt_file / system_prompt_file directives. Embedded
// workflows have no sibling files at runtime, so a directive would make the
// built-in unloadable (LoadDippinWorkflow resolves directives relative to the
// .dip's directory — the same delivery gap as embedded subgraph refs, see the
// design notes in PR #334). If a built-in needs file content, inline it.
func TestEmbeddedWorkflows_NoFileDirectives(t *testing.T) {
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
		for _, n := range wf.Nodes {
			switch cfg := n.Config.(type) {
			case ir.ToolConfig:
				if cfg.CommandFile != "" {
					t.Errorf("workflow %q node %q uses command_file: %q — embedded built-ins must inline content", info.Name, n.ID, cfg.CommandFile)
				}
			case ir.AgentConfig:
				if cfg.PromptFile != "" {
					t.Errorf("workflow %q node %q uses prompt_file: %q — embedded built-ins must inline content", info.Name, n.ID, cfg.PromptFile)
				}
				if cfg.SystemPromptFile != "" {
					t.Errorf("workflow %q node %q uses system_prompt_file: %q — embedded built-ins must inline content", info.Name, n.ID, cfg.SystemPromptFile)
				}
			}
		}
	}
}
