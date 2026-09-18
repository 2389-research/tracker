// ABOUTME: Behavioral guard on the REAL build_product.dip: the post-build EscalateVerification gate
// ABOUTME: resolves to "abandon" under the deterministic auto-approve interviewer, never "accept".
package handlers

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// TestEscalateVerification_UnattendedResolvesToAbandon pins that a
// non-interactive --auto-approve run (AutoApproveFreeformInterviewer) on the
// real EscalateVerification node picks "abandon" — the .dip `default:` (#646)
// — so a red or unverified build is never shipped by a gate default. The
// sibling EscalateReview gate keeps its documented "accept" default for the
// one case it still serves (a green build whose re-review budget ran out).
func TestEscalateVerification_UnattendedResolvesToAbandon(t *testing.T) {
	g := loadBuildProductGraph(t)
	h := NewHumanHandler(&AutoApproveFreeformInterviewer{}, g)
	for id, want := range map[string]string{"EscalateVerification": "abandon", "EscalateReview": "accept"} {
		node := g.Nodes[id]
		if node == nil {
			t.Fatalf("%s node missing from build_product.dip", id)
		}
		out, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
		if err != nil {
			t.Fatalf("%s Execute: %v", id, err)
		}
		if out.PreferredLabel != want {
			t.Errorf("%s unattended PreferredLabel = %q, want %q", id, out.PreferredLabel, want)
		}
	}
}
