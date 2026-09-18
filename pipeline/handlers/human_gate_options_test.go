// ABOUTME: Tests for the structured gate options carried on gate_opened and GateInfo (#631).
// ABOUTME: Pins that options come from edges + default:, never from prompt prose.
package handlers

import (
	"context"
	"reflect"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// approvePlanGraph mirrors build_product's ApprovePlan: a freeform gate with a
// declared default, a restart ("adjust") edge, and a rejection edge.
func approvePlanGraph() *pipeline.Graph {
	g := pipeline.NewGraph("plan")
	g.AddNode(&pipeline.Node{ID: "ApprovePlan", Shape: "hexagon", Label: "Approve the plan?",
		Attrs: map[string]string{"mode": "freeform", "default_choice": "approve", "prompt": "**approve** (default): ship it"}})
	g.AddNode(&pipeline.Node{ID: "PickNext", Shape: "box"})
	g.AddNode(&pipeline.Node{ID: "Decompose", Shape: "box"})
	g.AddNode(&pipeline.Node{ID: "Done", Shape: "box"})
	g.AddEdge(&pipeline.Edge{From: "ApprovePlan", To: "PickNext", Label: "approve"})
	g.AddEdge(&pipeline.Edge{From: "ApprovePlan", To: "Decompose", Label: "adjust", Attrs: map[string]string{"restart": "true"}})
	g.AddEdge(&pipeline.Edge{From: "ApprovePlan", To: "Done", Label: "reject"})
	return g
}

func openedGate(t *testing.T, g *pipeline.Graph, nodeID string, iv Interviewer) *pipeline.GateDetail {
	t.Helper()
	emitter, got := collectGateEvents()
	h := NewHumanHandler(iv, g, WithHumanPipelineEmitter(emitter))
	if _, err := h.Execute(context.Background(), g.Nodes[nodeID], pipeline.NewPipelineContext()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	opened := gateEventsOfType(*got, pipeline.EventGateOpened)
	if len(opened) != 1 || opened[0].Gate == nil {
		t.Fatalf("got %d gate_opened events, want 1 with a payload", len(opened))
	}
	return opened[0].Gate
}

func TestGateOpened_OptionsDerivedFromEdgesAndDefault(t *testing.T) {
	gate := openedGate(t, approvePlanGraph(), "ApprovePlan", &AutoApproveFreeformInterviewer{})

	if gate.Default != "approve" {
		t.Errorf("Default = %q, want approve", gate.Default)
	}
	want := []pipeline.GateOption{
		{Label: "approve", Target: "PickNext", Default: true, Meaning: pipeline.GateMeaningApprove},
		{Label: "adjust", Target: "Decompose", Restart: true},
		{Label: "reject", Target: "Done", Meaning: pipeline.GateMeaningReject},
	}
	if !reflect.DeepEqual(gate.Options, want) {
		t.Errorf("Options =\n%+v\nwant\n%+v", gate.Options, want)
	}
	// The legacy flat Choices stay in lockstep with Options.
	if got := []string{"approve", "adjust", "reject"}; !reflect.DeepEqual(gate.Choices, got) {
		t.Errorf("Choices = %v, want %v", gate.Choices, got)
	}
}

func TestGateOpened_OverrideEdgeMeansApprove_AbandonMeansReject(t *testing.T) {
	g := pipeline.NewGraph("review")
	g.AddNode(&pipeline.Node{ID: "EscalateReview", Shape: "hexagon", Label: "Reviewers object",
		Attrs: map[string]string{"mode": "freeform", "default_choice": "accept"}})
	g.AddNode(&pipeline.Node{ID: "FinalCommit", Shape: "box"})
	g.AddNode(&pipeline.Node{ID: "AbortRun", Shape: "box"})
	g.AddEdge(&pipeline.Edge{From: "EscalateReview", To: "FinalCommit", Label: "accept", Override: true})
	g.AddEdge(&pipeline.Edge{From: "EscalateReview", To: "AbortRun", Label: "abandon"})

	gate := openedGate(t, g, "EscalateReview", &AutoApproveFreeformInterviewer{})

	want := []pipeline.GateOption{
		{Label: "accept", Target: "FinalCommit", Default: true, Override: true, Meaning: pipeline.GateMeaningApprove},
		{Label: "abandon", Target: "AbortRun", Meaning: pipeline.GateMeaningReject},
	}
	if !reflect.DeepEqual(gate.Options, want) {
		t.Errorf("Options =\n%+v\nwant\n%+v", gate.Options, want)
	}
}

func TestGateOpened_YesNoOptions(t *testing.T) {
	g := pipeline.NewGraph("yn")
	g.AddNode(&pipeline.Node{ID: "Confirm", Shape: "hexagon", Label: "Proceed?", Attrs: map[string]string{"mode": "yes_no"}})
	g.AddNode(&pipeline.Node{ID: "Go", Shape: "box"})
	g.AddNode(&pipeline.Node{ID: "Stop", Shape: "box"})
	g.AddEdge(&pipeline.Edge{From: "Confirm", To: "Go", Condition: "outcome=success"})
	g.AddEdge(&pipeline.Edge{From: "Confirm", To: "Stop", Condition: "outcome=fail"})

	gate := openedGate(t, g, "Confirm", &AutoApproveInterviewer{})

	want := []pipeline.GateOption{
		{Label: "Yes", Default: true, Meaning: pipeline.GateMeaningApprove},
		{Label: "No", Meaning: pipeline.GateMeaningReject},
	}
	if !reflect.DeepEqual(gate.Options, want) {
		t.Errorf("Options =\n%+v\nwant\n%+v", gate.Options, want)
	}
	if gate.Default != "Yes" {
		t.Errorf("Default = %q, want Yes", gate.Default)
	}
}

func TestGateOpened_UnlabeledFreeformHasNoOptions(t *testing.T) {
	g := pipeline.NewGraph("ff")
	g.AddNode(&pipeline.Node{ID: "Ask", Shape: "hexagon", Label: "Anything?", Attrs: map[string]string{"mode": "freeform"}})
	g.AddNode(&pipeline.Node{ID: "Next", Shape: "box"})
	g.AddEdge(&pipeline.Edge{From: "Ask", To: "Next"})

	gate := openedGate(t, g, "Ask", &AutoApproveFreeformInterviewer{})
	if len(gate.Options) != 0 || gate.Default != "" {
		t.Errorf("Options = %+v, Default = %q; want none", gate.Options, gate.Default)
	}
}

// gateAwareRecorder captures the GateInfo handed to BeginGate.
type gateAwareRecorder struct {
	AutoApproveFreeformInterviewer
	info GateInfo
}

func (r *gateAwareRecorder) BeginGate(info GateInfo) { r.info = info }

func TestGateInfo_CarriesTheSameOptionsAsGateOpened(t *testing.T) {
	rec := &gateAwareRecorder{}
	gate := openedGate(t, approvePlanGraph(), "ApprovePlan", rec)

	if rec.info.Default != gate.Default {
		t.Errorf("GateInfo.Default = %q, gate_opened Default = %q", rec.info.Default, gate.Default)
	}
	if !reflect.DeepEqual(rec.info.Options, gate.Options) {
		t.Errorf("GateInfo.Options =\n%+v\ngate_opened Options =\n%+v", rec.info.Options, gate.Options)
	}
}
