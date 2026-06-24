// ABOUTME: #407 guard for the build_product dogfood cascade (run 634a2527ff56):
// ABOUTME: the milestone-escalation gate must surface the live verify result.
package pipeline

import (
	"strings"
	"testing"
)

// nodePrompt returns the resolved prompt text for an agent/human node.
func nodePrompt(t *testing.T, g *Graph, id string) string {
	t.Helper()
	n, ok := g.Nodes[id]
	if !ok {
		t.Fatalf("%s node missing from build_product graph", id)
	}
	return n.Attrs["prompt"]
}

// TestBuildProductEscalateMilestoneSurfacesVerifyState pins #407 AC1: the
// milestone-escalation gate must show the live verify result. In run
// 634a2527ff56 the operator was offered `abandon` on a green, passing tree with
// no indication the milestone verify currently PASSES, and discarded a finished
// build. The prompt must headline the live verify state.
func TestBuildProductEscalateMilestoneSurfacesVerifyState(t *testing.T) {
	p := nodePrompt(t, loadBuildProduct(t), "EscalateMilestone")
	if !strings.Contains(strings.ToLower(p), "verify currently") {
		t.Error("EscalateMilestone prompt does not surface the live verify result — a green tree can be abandoned without the operator being told it passes (issue #407 AC1)")
	}
	// The heading alone is hollow: the ${ctx.tool_stdout} interpolation is the
	// mechanism that actually injects the live verify output under it (this is a
	// prompt, where LLM-origin ctx.* keys are allowed). Without it the operator
	// sees an empty "Verify currently" section — exactly the regression #407
	// exists to prevent.
	if !strings.Contains(p, "${ctx.tool_stdout}") {
		t.Error("EscalateMilestone prompt has the \"Verify currently\" heading but no ${ctx.tool_stdout} interpolation — the live verify state is never actually surfaced (issue #407 AC1)")
	}
	// abandon must stay demoted to a FAIL-gated last resort so a green tree is
	// not offered as a casual default choice.
	if !strings.Contains(p, "LAST RESORT") {
		t.Error("EscalateMilestone prompt no longer demotes `abandon` to a LAST RESORT — a green tree can be abandoned as a first-class option (issue #407)")
	}
}
