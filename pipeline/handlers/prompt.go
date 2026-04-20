// ABOUTME: Shared prompt resolution logic for pipeline handlers that invoke LLMs.
// ABOUTME: Extracts, expands variables, applies fidelity compaction, and injects pipeline context.
package handlers

import (
	"fmt"

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
		compacted := pipeline.CompactContext(pctx, nil, fidelity, artifactDir, "")
		prompt = prependContextSummary(prompt, compacted, fidelity)
	} else {
		prompt = pipeline.InjectPipelineContext(prompt, pctx)
	}

	return prompt, nil
}
