// ABOUTME: Pin test for issues #418b/#419 — model + reasoning-effort tiering on
// ABOUTME: the review panel and cheaply-verified agent nodes in build_product.dip.
package pipeline

import (
	"strings"
	"testing"
)

func TestBuildProductReviewPanelTiering(t *testing.T) {
	g := loadBuildProduct(t)
	cases := []struct{ id, model, effort, provider string }{
		// #418b: the lanes run mid-tier; tracker-runner convergence pins the
		// OpenAI lane to gpt-5.6-sol at medium effort (with a max_turns cap,
		// #353/#598) and the adversarial lane to gemini-2.5-flash.
		{"ReviewClaude", "claude-sonnet-5", "high", "anthropic"},
		{"ReviewCodex", "gpt-5.6-sol", "medium", "openai"},
		{"ReviewGemini", "gemini-2.5-flash", "high", "gemini"},
	}
	for _, c := range cases {
		n := g.Nodes[c.id]
		if n == nil {
			t.Fatalf("%s node missing", c.id)
		}
		if got := n.Attrs["llm_model"]; got != c.model {
			t.Errorf("%s llm_model = %q, want %q (#418b)", c.id, got, c.model)
		}
		if got := n.Attrs["reasoning_effort"]; got != c.effort {
			t.Errorf("%s reasoning_effort = %q, want %q (#418b)", c.id, got, c.effort)
		}
		if got := n.Attrs["llm_provider"]; got != c.provider {
			t.Errorf("%s llm_provider = %q, want %q (#418b)", c.id, got, c.provider)
		}
	}
	if got := g.Nodes["ReviewCodex"].Attrs["max_turns"]; got != "40" {
		t.Errorf("ReviewCodex max_turns = %q, want 40 (tracker-runner #353/#598 runaway cap)", got)
	}
}

func TestBuildProductSynthesisAndFinalSpecCheckStayFrontier(t *testing.T) {
	g := loadBuildProduct(t)
	for _, id := range []string{"SynthesizeReviews", "FinalSpecCheck"} {
		n := g.Nodes[id]
		if n == nil {
			t.Fatalf("%s node missing", id)
		}
		if got := n.Attrs["llm_model"]; got != "claude-opus-5" {
			t.Errorf("%s llm_model = %q, want claude-opus-5 (frontier, #419)", id, got)
		}
		if got := n.Attrs["reasoning_effort"]; got != "high" {
			t.Errorf("%s reasoning_effort = %q, want high (#419)", id, got)
		}
	}
}

func TestBuildProductCheaplyVerifiedNodesAreMidTier(t *testing.T) {
	g := loadBuildProduct(t)
	// #419: SpecLint (gated by its STATUS contract) and ReadSpec (gated by
	// ApprovePlan) drop to mid-tier model AND medium effort.
	for _, id := range []string{"SpecLint", "ReadSpec"} {
		n := g.Nodes[id]
		if n == nil {
			t.Fatalf("%s node missing", id)
		}
		if got := n.Attrs["llm_model"]; got != "claude-sonnet-5" {
			t.Errorf("%s llm_model = %q, want claude-sonnet-5 (#419)", id, got)
		}
		if got := n.Attrs["reasoning_effort"]; got != "medium" {
			t.Errorf("%s reasoning_effort = %q, want medium (#419)", id, got)
		}
	}
}

func TestBuildProductComputeReviewDiffNodeExists(t *testing.T) {
	g := loadBuildProduct(t)
	n := g.Nodes["ComputeReviewDiff"]
	if n == nil {
		t.Fatal("ComputeReviewDiff node missing (#418a)")
	}
	cmd := n.Attrs["tool_command"]
	if cmd == "" {
		t.Fatal("ComputeReviewDiff has no tool_command (#418a)")
	}
	for _, want := range []string{"run-base-sha", "OUT=.ai/build/review-diff.md"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("ComputeReviewDiff tool_command missing %q (#418a)", want)
		}
	}
	for _, id := range []string{"ReviewClaude", "ReviewCodex", "ReviewGemini"} {
		rn := g.Nodes[id]
		if rn == nil {
			t.Errorf("reviewer node %q missing (#418a)", id)
			continue
		}
		if p := rn.Attrs["prompt"]; !strings.Contains(p, "review-diff.md") {
			t.Errorf("%s prompt does not point at review-diff.md (#418a)", id)
		}
	}
}
