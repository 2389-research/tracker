// ABOUTME: Tests for the optional GateAware side-interface (#509 callback side).
// ABOUTME: Pins BeginGate identity and gate_id correlation with the gate_opened event.
package handlers

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// recordingGateAware is an Interviewer that also implements GateAware, recording
// every GateInfo it is handed so a test can assert what BeginGate received.
type recordingGateAware struct {
	AutoApproveInterviewer
	infos []GateInfo
}

func (r *recordingGateAware) BeginGate(info GateInfo) { r.infos = append(r.infos, info) }

// newChoiceGateGraph builds a single hexagon gate with two labeled edges.
func newChoiceGateGraph() *pipeline.Graph {
	graph := pipeline.NewGraph("gate-aware")
	graph.AddNode(&pipeline.Node{ID: "gate", Shape: "hexagon", Label: "Approve the plan?"})
	graph.AddNode(&pipeline.Node{ID: "ship", Shape: "box"})
	graph.AddNode(&pipeline.Node{ID: "revise", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "ship", Label: "approve"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "revise", Label: "revise"})
	return graph
}

// TestHumanHandler_GateAwareReceivesCorrelatedID proves BeginGate is invoked with
// the gate identity and that its GateID EQUALS the gate_opened event's GateID for
// the same gate — the whole point of the callback side (requirement 1).
func TestHumanHandler_GateAwareReceivesCorrelatedID(t *testing.T) {
	graph := newChoiceGateGraph()
	emitter, got := collectGateEvents()
	rec := &recordingGateAware{}
	h := NewHumanHandler(rec, graph, WithHumanPipelineEmitter(emitter))

	pctx := pipeline.NewPipelineContext()
	pctx.SetInternal(pipeline.InternalKeyRunID, "run-123")

	if _, err := h.Execute(context.Background(), graph.Nodes["gate"], pctx); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(rec.infos) != 1 {
		t.Fatalf("BeginGate called %d times, want exactly 1", len(rec.infos))
	}
	info := rec.infos[0]
	if info.NodeID != "gate" {
		t.Errorf("GateInfo.NodeID = %q, want gate", info.NodeID)
	}
	if info.Mode != "choice" {
		t.Errorf("GateInfo.Mode = %q, want choice", info.Mode)
	}
	if info.Label != "Approve the plan?" {
		t.Errorf("GateInfo.Label = %q, want the node label", info.Label)
	}
	if info.RunID != "run-123" {
		t.Errorf("GateInfo.RunID = %q, want run-123", info.RunID)
	}

	opened := gateEventsOfType(*got, pipeline.EventGateOpened)
	if len(opened) != 1 || opened[0].Gate == nil {
		t.Fatalf("got %d gate_opened events, want 1 with a payload", len(opened))
	}
	if info.GateID == "" {
		t.Fatal("GateInfo.GateID is empty")
	}
	if info.GateID != opened[0].Gate.GateID {
		t.Fatalf("GateInfo.GateID = %q, gate_opened GateID = %q; the two MUST correlate", info.GateID, opened[0].Gate.GateID)
	}
}

// TestHumanHandler_GateAwareFiresWithoutEmitter proves BeginGate fires even when
// no pipeline event emitter is wired — a transport implementing GateAware must
// work with or without an NDJSON sink (requirement 2).
func TestHumanHandler_GateAwareFiresWithoutEmitter(t *testing.T) {
	graph := newChoiceGateGraph()
	rec := &recordingGateAware{}
	h := NewHumanHandler(rec, graph) // no WithHumanPipelineEmitter

	if _, err := h.Execute(context.Background(), graph.Nodes["gate"], pipeline.NewPipelineContext()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rec.infos) != 1 {
		t.Fatalf("BeginGate called %d times without an emitter, want exactly 1", len(rec.infos))
	}
	if rec.infos[0].GateID == "" {
		t.Error("GateInfo.GateID is empty without an emitter — the id must still be minted")
	}
}

// TestHumanHandler_PlainInterviewerUnaffected proves a plain Interviewer that does
// NOT implement GateAware runs exactly as before — purely additive, assertion-gated
// (requirement 3).
func TestHumanHandler_PlainInterviewerUnaffected(t *testing.T) {
	graph := newChoiceGateGraph()
	h := NewHumanHandler(&AutoApproveInterviewer{}, graph)

	outcome, err := h.Execute(context.Background(), graph.Nodes["gate"], pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcome.PreferredLabel != "approve" {
		t.Fatalf("PreferredLabel = %q, want approve", outcome.PreferredLabel)
	}
}
