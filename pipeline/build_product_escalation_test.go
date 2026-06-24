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
}
