// ABOUTME: Behavioral guard for #318 hazard 1 — an unattended (auto-approve /
// ABOUTME: webhook) operator-decision gate must resolve to the SAFE default
// ABOUTME: ("stop"), never silently "continue" past unverified work.
package handlers

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// loadBuildProductGraph loads the shipped build_product.dip into a Graph from the
// handlers package (examples/ is two levels up from pipeline/handlers).
func loadBuildProductGraph(t *testing.T) *pipeline.Graph {
	t.Helper()
	path := filepath.Join("..", "..", "examples", "build_product.dip")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	g, _, err := pipeline.LoadDippinWorkflow(string(source), "build_product.dip")
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v", err)
	}
	return g
}

// TestOperatorDecision_UnattendedResolvesToStop pins that a non-interactive
// auto-approve run (AutoApproveFreeformInterviewer) on the real OperatorDecision
// node resolves to "stop", routing to the escalation gate rather than advancing.
//
// NOTE on the mechanism: in freeform mode HumanHandler reads node.Attrs["default"]
// (NOT default_choice, where dippin's `default:` keyword lands), so for a
// dippin-authored gate that bare attr is empty and the interviewer falls back to
// the FIRST labeled edge. The safety here therefore comes from EDGE ORDERING
// (stop is listed first), not from a freeform `default` attr being honored. The
// --webhook path is a separate interviewer not exercised here; this test pins the
// deterministic auto-approve path end-to-end.
func TestOperatorDecision_UnattendedResolvesToStop(t *testing.T) {
	g := loadBuildProductGraph(t)
	node := g.Nodes["OperatorDecision"]
	if node == nil {
		t.Fatal("OperatorDecision node missing from build_product.dip (issue #318)")
	}

	h := NewHumanHandler(&AutoApproveFreeformInterviewer{}, g)
	out, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.PreferredLabel != "stop" {
		t.Errorf("unattended PreferredLabel = %q, want \"stop\" — auto-approve must take the safe default (issue #318 hazard 1)", out.PreferredLabel)
	}
	if out.PreferredLabel == "continue" {
		t.Fatal("unattended run resolved to \"continue\" — the exact unsafe outcome #318 forbids")
	}
}

// TestOperatorDecision_DroppedDefaultStillSafe pins the defense-in-depth ordering:
// if the `default` attr were ever dropped, the freeform interviewer falls back to
// the FIRST edge label. That fallback must be a non-advancing option, never
// "continue" — so the edge order (stop/abandon before continue) fails safe.
func TestOperatorDecision_DroppedDefaultStillSafe(t *testing.T) {
	g := loadBuildProductGraph(t)
	if g.Nodes["OperatorDecision"] == nil {
		t.Fatal("OperatorDecision node missing from build_product.dip (issue #318)")
	}

	var labels []string
	for _, e := range g.OutgoingEdges("OperatorDecision") {
		if e.Label != "" {
			labels = append(labels, e.Label)
		}
	}
	if len(labels) == 0 {
		t.Fatal("OperatorDecision has no labeled edges (issue #318)")
	}

	// Empty default → AskFreeformWithLabels returns labels[0].
	fallback, err := (&AutoApproveFreeformInterviewer{}).AskFreeformWithLabels("", labels, "")
	if err != nil {
		t.Fatalf("AskFreeformWithLabels: %v", err)
	}
	if fallback == "continue" {
		t.Errorf("dropped-default fallback (labels[0]) = %q — must not be the advancing option; reorder so stop/abandon precede continue (issue #318 hazard 1)", fallback)
	}
}
