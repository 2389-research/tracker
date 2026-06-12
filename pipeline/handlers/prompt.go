// ABOUTME: Shared prompt resolution logic for pipeline handlers that invoke LLMs.
// ABOUTME: Extracts, expands variables, applies fidelity compaction, and injects pipeline context.
package handlers

import (
	"fmt"
	"strconv"

	"github.com/2389-research/tracker/pipeline"
)

// ResolvePrompt extracts the prompt from node attributes, expands variables,
// applies fidelity-based context compaction or full context injection, and
// returns the final prompt string ready for LLM consumption.
func ResolvePrompt(node *pipeline.Node, pctx *pipeline.PipelineContext,
	graphAttrs map[string]string, artifactDir string) (string, error) {

	prompt := node.Attrs["prompt"]
	if prompt == "" {
		return "", fmt.Errorf("node %q missing required attribute 'prompt'", node.ID)
	}

	params := pipeline.ExtractParamsFromGraphAttrs(graphAttrs)
	prompt, err := pipeline.ExpandVariables(prompt, pctx, params, graphAttrs, false)
	if err != nil {
		return "", fmt.Errorf("node %q variable expansion failed: %w", node.ID, err)
	}

	prompt = pipeline.ExpandPromptVariables(prompt, pctx)

	fidelity := pipeline.ResolveFidelity(node, graphAttrs)
	if fidelity != pipeline.FidelityFull {
		compacted := pipeline.CompactContextWithPinnedKeys(
			pctx,
			nil,
			fidelity,
			artifactDir,
			"",
			pipeline.ParseDeclaredKeys(node.Attrs["reads"]),
		)
		prompt = prependContextSummary(prompt, compacted, fidelity)
	} else {
		capBytes, err := resolveInjectionCap(node)
		if err != nil {
			return "", err
		}
		prompt = pipeline.InjectPipelineContext(prompt, pctx, capBytes)
	}

	return prompt, nil
}

// resolveInjectionCap reads the optional injection_cap node attr (#352): the
// byte budget for the injected "Previous Node Output" section. 0/absent means
// pipeline.DefaultInjectedResponseCap; negative disables capping. Strict-parse
// raw attr read (not in AgentNodeConfig): a malformed value must fail the node
// loudly, not silently fall back to the default cap.
func resolveInjectionCap(node *pipeline.Node) (int, error) {
	raw := node.Attrs["injection_cap"]
	if raw == "" {
		return 0, nil
	}
	capBytes, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("node %q has malformed injection_cap %q: %w", node.ID, raw, err)
	}
	return capBytes, nil
}
