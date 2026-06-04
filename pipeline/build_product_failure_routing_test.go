// ABOUTME: Regression guard for issue #296 — every agent (codergen) node in the
// ABOUTME: shipped build_product.dip must route failure/turn-exhaustion, never dead-stop.
package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

// loadBuildProduct loads the embedded-on-disk examples/build_product.dip the
// same way the binary embeds it. Relative to the pipeline package dir.
func loadBuildProduct(t *testing.T) *Graph {
	t.Helper()
	path := filepath.Join("..", "examples", "build_product.dip")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	g, _, err := LoadDippinWorkflow(string(source), "build_product.dip")
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v", err)
	}
	return g
}

// TestBuildProductAgentNodesHaveFailureRouting pins the issue #296 invariant:
// no agent node in build_product.dip can silently halt the pipeline on an
// unhandled failure (e.g. max_turns exhaustion). This is the in-repo
// counterpart to dippin-lang#93's author-time lint.
//
// It mirrors the engine's strict-failure rule exactly (checkStrictFailure /
// findFallbackTarget): a failed node dead-stops only when it has NO conditional
// outgoing edge AND no node/graph-level fallback_target resolves. So every
// codergen node must satisfy hasAnyConditionalEdge(out) || a resolvable
// fallback_target. If a future edit reintroduces a single-unconditional-edge
// agent node with no fallback, this fails — the regression that caused the
// code-goblin run (7b6e08c9e2b2) to halt before review + FinalSpecCheck.
func TestBuildProductAgentNodesHaveFailureRouting(t *testing.T) {
	g := loadBuildProduct(t)

	checked := 0
	for _, n := range g.Nodes {
		// Only agent/codergen nodes run a session that can exhaust turns.
		// Passthrough start/exit carry no agent work.
		if n.Handler != "codergen" || n.ID == g.StartNode || n.ID == g.ExitNode {
			continue
		}
		checked++
		if hasAnyConditionalEdge(g.OutgoingEdges(n.ID)) || resolveFallback(g, n) != "" {
			continue
		}
		t.Errorf("agent node %q can dead-stop on failure: no conditional fail edge and no fallback_target (issue #296)", n.ID)
	}
	if checked == 0 {
		t.Fatal("no codergen nodes found — loader or fixture changed; invariant not actually exercised")
	}
}

// resolveFallback returns the node's effective fallback target using the same
// candidate order as Engine.findFallbackTarget, or "" if none resolves. The
// .dip `fallback_target:` keyword lands in node.Attrs["fallback_retry_target"]
// after the IR→Graph adapter (extractRetryAttrs), so a direct read of
// "fallback_target" would miss it — resolve the way the engine does.
func resolveFallback(g *Graph, n *Node) string {
	for _, fb := range []string{
		n.Attrs["fallback_target"],
		n.Attrs["fallback_retry_target"],
		g.Attrs["fallback_target"],
		g.Attrs["fallback_retry_target"],
	} {
		if fb != "" {
			if _, ok := g.Nodes[fb]; ok {
				return fb
			}
		}
	}
	return ""
}

// TestBuildProductIssue296FallbackTargets pins the specific escalation targets
// for the six nodes #296 hardened. The general invariant above proves "has a
// route"; this proves the route goes to the *correct* escalation gate, so a
// future edit can't satisfy the invariant by pointing failure somewhere unsafe
// (e.g. a Fix node or loop target).
func TestBuildProductIssue296FallbackTargets(t *testing.T) {
	g := loadBuildProduct(t)

	want := map[string]string{
		"Implement":        "EscalateMilestone",
		"ReviewClaude":     "EscalateReview",
		"ReviewCodex":      "EscalateReview",
		"ReviewGemini":     "EscalateReview",
		"ApplyReviewFixes": "EscalateReview",
		"FinalCommit":      "EscalateReview",
	}
	for id, target := range want {
		if _, ok := g.Nodes[id]; !ok {
			t.Errorf("node %q missing from build_product.dip", id)
			continue
		}
		if got := resolveFallback(g, g.Nodes[id]); got != target {
			t.Errorf("node %q: resolved fallback = %q, want %q (issue #296)", id, got, target)
		}
	}
}
