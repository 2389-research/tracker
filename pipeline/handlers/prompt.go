// ABOUTME: Shared prompt resolution logic for pipeline handlers that invoke LLMs.
// ABOUTME: Extracts, expands variables, applies fidelity compaction, and injects pipeline context.
package handlers

import (
	"fmt"
	"strconv"
	"unicode/utf8"

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

	// Apply last_response_truncate before any variable expansion so that
	// ${ctx.last_response} references in the prompt template are also capped
	// (chain-attack mitigation, issue #56 / dippin-lang v0.40.0).
	restore, err := applyLastResponseTruncate(node, pctx)
	if err != nil {
		return "", err
	}
	if restore != nil {
		defer restore()
	}

	params := pipeline.ExtractParamsFromGraphAttrs(graphAttrs)
	prompt, err = pipeline.ExpandVariables(prompt, pctx, params, graphAttrs, false)
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

// applyLastResponseTruncate caps pctx's last_response to the node's
// last_response_truncate limit before prompt building. Returns a restore
// function (non-nil only when truncation actually changed the value) so the
// caller can defer-restore the original. Returns an error for invalid attrs.
func applyLastResponseTruncate(node *pipeline.Node, pctx *pipeline.PipelineContext) (func(), error) {
	truncChars, err := resolveLastResponseTruncate(node)
	if err != nil || truncChars == 0 {
		return nil, err
	}
	resp, ok := pctx.Get(pipeline.ContextKeyLastResponse)
	if !ok {
		return nil, nil
	}
	// Walk forward exactly truncChars runes to find the byte cut point.
	// This avoids allocating a full []rune for large responses.
	byteIdx, count := 0, 0
	for byteIdx < len(resp) && count < truncChars {
		_, size := utf8.DecodeRuneInString(resp[byteIdx:])
		byteIdx += size
		count++
	}
	if count < truncChars {
		return nil, nil // fewer runes than the limit — nothing to truncate
	}
	// Use MergeWithoutDirty so the temporary truncation is not attributed to
	// this node if ResolvePrompt returns an error and the engine calls
	// ScopeToNode on the error path.
	pctx.MergeWithoutDirty(map[string]string{pipeline.ContextKeyLastResponse: resp[:byteIdx]})
	return func() { pctx.MergeWithoutDirty(map[string]string{pipeline.ContextKeyLastResponse: resp}) }, nil
}

// resolveLastResponseTruncate reads the optional last_response_truncate node
// attr: a Unicode character cap on the ctx.last_response value. 0/absent means
// no truncation. Negative values and non-integers are rejected loudly.
func resolveLastResponseTruncate(node *pipeline.Node) (int, error) {
	raw := node.Attrs["last_response_truncate"]
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("node %q has malformed last_response_truncate %q: %w", node.ID, raw, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("node %q has invalid last_response_truncate %d: must be >= 0", node.ID, n)
	}
	return n, nil
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
