// ABOUTME: Tests for the human gate handler and interviewer implementations.
// ABOUTME: Validates AutoApproveInterviewer and human handler choice presentation.
package handlers

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func TestAutoApproveInterviewer(t *testing.T) {
	interviewer := &AutoApproveInterviewer{}
	choice, err := interviewer.Ask("Continue?", []string{"yes", "no"}, "yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if choice != "yes" {
		t.Errorf("expected 'yes', got %q", choice)
	}
}

func TestAutoApproveInterviewerNoDefault(t *testing.T) {
	interviewer := &AutoApproveInterviewer{}
	choice, err := interviewer.Ask("Pick one", []string{"alpha", "beta"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if choice != "alpha" {
		t.Errorf("expected 'alpha', got %q", choice)
	}
}

func TestAutoApproveInterviewerNoChoices(t *testing.T) {
	interviewer := &AutoApproveInterviewer{}
	_, err := interviewer.Ask("Pick one", []string{}, "")
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
}

func TestHumanHandlerName(t *testing.T) {
	h := NewHumanHandler(&AutoApproveInterviewer{}, nil)
	if h.Name() != "wait.human" {
		t.Errorf("expected 'wait.human', got %q", h.Name())
	}
}

func TestHumanHandlerWithAutoApprove(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{ID: "gate", Shape: "hexagon"})
	graph.AddNode(&pipeline.Node{ID: "approve", Shape: "box"})
	graph.AddNode(&pipeline.Node{ID: "reject", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "approve", Label: "approve"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "reject", Label: "reject"})

	h := NewHumanHandler(&AutoApproveInterviewer{}, graph)
	node := graph.Nodes["gate"]
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected 'success', got %q", outcome.Status)
	}
	if outcome.PreferredLabel != "approve" {
		t.Errorf("expected preferred label 'approve', got %q", outcome.PreferredLabel)
	}
}

func TestHumanHandlerWithDefaultChoice(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{ID: "gate", Shape: "hexagon", Attrs: map[string]string{"default_choice": "reject"}})
	graph.AddNode(&pipeline.Node{ID: "approve", Shape: "box"})
	graph.AddNode(&pipeline.Node{ID: "reject", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "approve", Label: "approve"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "reject", Label: "reject"})

	h := NewHumanHandler(&AutoApproveInterviewer{}, graph)
	node := graph.Nodes["gate"]
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.PreferredLabel != "reject" {
		t.Errorf("expected 'reject', got %q", outcome.PreferredLabel)
	}
}

func TestHumanHandlerNoOutgoingEdges(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{ID: "gate", Shape: "hexagon"})

	h := NewHumanHandler(&AutoApproveInterviewer{}, graph)
	node := graph.Nodes["gate"]
	pctx := pipeline.NewPipelineContext()

	_, err := h.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected error for no outgoing edges")
	}
}

type recordingInterviewer struct {
	promptReceived  string
	choicesReceived []string
	response        string
}

func (r *recordingInterviewer) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	r.promptReceived = prompt
	r.choicesReceived = choices
	return r.response, nil
}

func TestHumanHandlerPassesLabelAsPrompt(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{ID: "gate", Shape: "hexagon", Label: "Review the code changes"})
	graph.AddNode(&pipeline.Node{ID: "yes", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "yes", Label: "looks good"})

	recorder := &recordingInterviewer{response: "looks good"}
	h := NewHumanHandler(recorder, graph)
	node := graph.Nodes["gate"]
	pctx := pipeline.NewPipelineContext()

	_, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recorder.promptReceived != "Review the code changes" {
		t.Errorf("expected prompt 'Review the code changes', got %q", recorder.promptReceived)
	}
	if len(recorder.choicesReceived) != 1 || recorder.choicesReceived[0] != "looks good" {
		t.Errorf("expected choices [looks good], got %v", recorder.choicesReceived)
	}
}

func TestManagerLoopHandlerIsRegistered(t *testing.T) {
	graph := pipeline.NewGraph("test")
	registry := NewDefaultRegistry(graph)
	if !registry.Has("stack.manager_loop") {
		t.Fatal("expected stack.manager_loop to be registered")
	}
}
