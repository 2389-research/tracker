// ABOUTME: Tests for gate lifecycle events emitted by the human gate handler (#509).
// ABOUTME: Pins node_id/label/prompt/choices on gate_opened and gate_id correlation on gate_resolved.
package handlers

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// collectGateEvents returns a pipeline emitter that appends every event to the
// returned slice pointer.
func collectGateEvents() (pipeline.PipelineEventHandler, *[]pipeline.PipelineEvent) {
	var got []pipeline.PipelineEvent
	h := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		got = append(got, evt)
	})
	return h, &got
}

// gateEventsOfType filters collected events by type.
func gateEventsOfType(events []pipeline.PipelineEvent, t pipeline.PipelineEventType) []pipeline.PipelineEvent {
	var out []pipeline.PipelineEvent
	for _, e := range events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

func TestHumanHandler_EmitsGateEventsForChoiceMode(t *testing.T) {
	graph := pipeline.NewGraph("gates")
	graph.AddNode(&pipeline.Node{ID: "gate", Shape: "hexagon", Label: "Approve the plan?"})
	graph.AddNode(&pipeline.Node{ID: "ship", Shape: "box"})
	graph.AddNode(&pipeline.Node{ID: "revise", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "ship", Label: "approve"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "revise", Label: "revise"})

	emitter, got := collectGateEvents()
	h := NewHumanHandler(&AutoApproveInterviewer{}, graph, WithHumanPipelineEmitter(emitter))

	outcome, err := h.Execute(context.Background(), graph.Nodes["gate"], pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcome.PreferredLabel != "approve" {
		t.Fatalf("PreferredLabel = %q, want approve", outcome.PreferredLabel)
	}

	opened := gateEventsOfType(*got, pipeline.EventGateOpened)
	resolved := gateEventsOfType(*got, pipeline.EventGateResolved)
	if len(opened) != 1 || len(resolved) != 1 {
		t.Fatalf("got %d gate_opened / %d gate_resolved, want 1 / 1", len(opened), len(resolved))
	}

	o := opened[0]
	if o.NodeID != "gate" {
		t.Errorf("gate_opened NodeID = %q, want gate", o.NodeID)
	}
	if o.Gate == nil {
		t.Fatal("gate_opened has nil Gate payload")
	}
	if o.Gate.GateID == "" {
		t.Error("gate_opened GateID is empty")
	}
	if o.Gate.Mode != "choice" {
		t.Errorf("gate_opened Mode = %q, want choice", o.Gate.Mode)
	}
	if o.Gate.Label != "Approve the plan?" {
		t.Errorf("gate_opened Label = %q, want the node label", o.Gate.Label)
	}
	if o.Gate.Prompt == "" {
		t.Error("gate_opened Prompt is empty — a stream consumer cannot render the question")
	}
	if len(o.Gate.Choices) != 2 || o.Gate.Choices[0] != "approve" || o.Gate.Choices[1] != "revise" {
		t.Errorf("gate_opened Choices = %v, want [approve revise]", o.Gate.Choices)
	}

	r := resolved[0]
	if r.NodeID != "gate" {
		t.Errorf("gate_resolved NodeID = %q, want gate", r.NodeID)
	}
	if r.Gate == nil {
		t.Fatal("gate_resolved has nil Gate payload")
	}
	if r.Gate.GateID != o.Gate.GateID {
		t.Errorf("gate_resolved GateID = %q, want %q (must correlate with gate_opened)", r.Gate.GateID, o.Gate.GateID)
	}
	if r.Gate.Response != "approve" {
		t.Errorf("gate_resolved Response = %q, want approve", r.Gate.Response)
	}
	if r.Gate.Outcome != string(pipeline.OutcomeSuccess) {
		t.Errorf("gate_resolved Outcome = %q, want success", r.Gate.Outcome)
	}
	if r.Gate.Actor != pipeline.ActorAutopilot {
		t.Errorf("gate_resolved Actor = %q, want autopilot", r.Gate.Actor)
	}
}

func TestHumanHandler_EmitsGateEventsForFreeformMode(t *testing.T) {
	graph := pipeline.NewGraph("gates")
	graph.AddNode(&pipeline.Node{
		ID:    "ask",
		Shape: "hexagon",
		Label: "What should we build?",
		Attrs: map[string]string{"mode": "freeform"},
	})

	emitter, got := collectGateEvents()
	h := NewHumanHandler(&AutoApproveFreeformInterviewer{}, graph, WithHumanPipelineEmitter(emitter))

	if _, err := h.Execute(context.Background(), graph.Nodes["ask"], pipeline.NewPipelineContext()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	opened := gateEventsOfType(*got, pipeline.EventGateOpened)
	resolved := gateEventsOfType(*got, pipeline.EventGateResolved)
	if len(opened) != 1 || len(resolved) != 1 {
		t.Fatalf("got %d gate_opened / %d gate_resolved, want 1 / 1", len(opened), len(resolved))
	}
	if opened[0].Gate.Mode != "freeform" {
		t.Errorf("gate_opened Mode = %q, want freeform", opened[0].Gate.Mode)
	}
	if len(opened[0].Gate.Choices) != 0 {
		t.Errorf("gate_opened Choices = %v, want none for an unlabeled freeform gate", opened[0].Gate.Choices)
	}
	if resolved[0].Gate.Response != "auto-approved" {
		t.Errorf("gate_resolved Response = %q, want auto-approved", resolved[0].Gate.Response)
	}
}

// TestHumanHandler_GateIDsAreUniquePerOpen is the property the issue turns on:
// with several gates in one run, each open/resolve pair carries its own ID so
// "which question got which answer" is reconstructable from the stream alone.
func TestHumanHandler_GateIDsAreUniquePerOpen(t *testing.T) {
	graph := pipeline.NewGraph("gates")
	graph.AddNode(&pipeline.Node{ID: "g1", Shape: "hexagon", Label: "one", Attrs: map[string]string{"mode": "freeform"}})
	graph.AddNode(&pipeline.Node{ID: "g2", Shape: "hexagon", Label: "two", Attrs: map[string]string{"mode": "freeform"}})

	emitter, got := collectGateEvents()
	h := NewHumanHandler(&AutoApproveFreeformInterviewer{}, graph, WithHumanPipelineEmitter(emitter))

	for _, id := range []string{"g1", "g2"} {
		if _, err := h.Execute(context.Background(), graph.Nodes[id], pipeline.NewPipelineContext()); err != nil {
			t.Fatalf("Execute(%s): %v", id, err)
		}
	}

	opened := gateEventsOfType(*got, pipeline.EventGateOpened)
	if len(opened) != 2 {
		t.Fatalf("got %d gate_opened, want 2", len(opened))
	}
	if opened[0].Gate.GateID == opened[1].Gate.GateID {
		t.Errorf("both gates share GateID %q — resolutions cannot be correlated", opened[0].Gate.GateID)
	}

	// Every resolved event must match exactly one opened event's ID, on the
	// same node.
	byID := map[string]string{}
	for _, o := range opened {
		byID[o.Gate.GateID] = o.NodeID
	}
	for _, r := range gateEventsOfType(*got, pipeline.EventGateResolved) {
		node, ok := byID[r.Gate.GateID]
		if !ok {
			t.Errorf("gate_resolved GateID %q has no matching gate_opened", r.Gate.GateID)
			continue
		}
		if node != r.NodeID {
			t.Errorf("gate %q: opened on node %q but resolved on node %q", r.Gate.GateID, node, r.NodeID)
		}
	}
}

// TestHumanHandler_NoEmitterIsSafe pins that gate events are opt-in: a handler
// built without an emitter (every existing caller) behaves exactly as before.
func TestHumanHandler_NoEmitterIsSafe(t *testing.T) {
	graph := pipeline.NewGraph("gates")
	graph.AddNode(&pipeline.Node{ID: "gate", Shape: "hexagon", Label: "ok?", Attrs: map[string]string{"mode": "freeform"}})

	h := NewHumanHandler(&AutoApproveFreeformInterviewer{}, graph)
	outcome, err := h.Execute(context.Background(), graph.Nodes["gate"], pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("Status = %q, want success", outcome.Status)
	}
}

// TestHumanHandler_GateResolvedOnFailure pins that a failed gate still emits a
// resolution — a consumer must never be left with a gate stuck open. yes_no
// mode's "No" is the cheapest deterministic failure path.
func TestHumanHandler_GateResolvedOnFailure(t *testing.T) {
	graph := pipeline.NewGraph("gates")
	graph.AddNode(&pipeline.Node{ID: "gate", Shape: "hexagon", Label: "ship it?", Attrs: map[string]string{"mode": "yes_no"}})

	emitter, got := collectGateEvents()
	h := NewHumanHandler(&QueueInterviewer{Answers: []string{"No"}}, graph, WithHumanPipelineEmitter(emitter))

	outcome, err := h.Execute(context.Background(), graph.Nodes["gate"], pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Fatalf("Status = %q, want fail", outcome.Status)
	}

	resolved := gateEventsOfType(*got, pipeline.EventGateResolved)
	if len(resolved) != 1 {
		t.Fatalf("got %d gate_resolved, want 1", len(resolved))
	}
	if resolved[0].Gate.Outcome != string(pipeline.OutcomeFail) {
		t.Errorf("gate_resolved Outcome = %q, want fail", resolved[0].Gate.Outcome)
	}
	if resolved[0].Gate.Response != "No" {
		t.Errorf("gate_resolved Response = %q, want No", resolved[0].Gate.Response)
	}
}

// TestHumanHandler_GateResolvedOnInterviewerError pins that an interviewer
// error (not just a fail outcome) also closes the gate on the stream.
func TestHumanHandler_GateResolvedOnInterviewerError(t *testing.T) {
	graph := pipeline.NewGraph("gates")
	graph.AddNode(&pipeline.Node{ID: "gate", Shape: "hexagon", Label: "pick", Attrs: map[string]string{"mode": "freeform"}})

	emitter, got := collectGateEvents()
	// A bare Interviewer does not implement FreeformInterviewer, so freeform
	// mode errors out inside dispatchHumanMode.
	h := NewHumanHandler(&QueueInterviewer{}, graph, WithHumanPipelineEmitter(emitter))

	if _, err := h.Execute(context.Background(), graph.Nodes["gate"], pipeline.NewPipelineContext()); err == nil {
		t.Fatal("expected an error for freeform mode without a FreeformInterviewer")
	}

	resolved := gateEventsOfType(*got, pipeline.EventGateResolved)
	if len(resolved) != 1 {
		t.Fatalf("got %d gate_resolved, want 1", len(resolved))
	}
	if resolved[0].Gate.Error == "" {
		t.Error("gate_resolved Error is empty on an interviewer failure")
	}
}
