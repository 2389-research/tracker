// ABOUTME: Tests for the human gate handler and interviewer implementations.
// ABOUTME: Validates AutoApproveInterviewer and human handler choice presentation.
package handlers

import (
	"context"
	"strings"
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

// --- Freeform mode tests ---

type recordingFreeformInterviewer struct {
	recordingInterviewer
	freeformPromptReceived string
	freeformResponse       string
}

func (r *recordingFreeformInterviewer) AskFreeform(prompt string) (string, error) {
	r.freeformPromptReceived = prompt
	return r.freeformResponse, nil
}

func TestHumanHandlerFreeformMode(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Label: "What would you like to do?",
		Attrs: map[string]string{"mode": "freeform"},
	})
	graph.AddNode(&pipeline.Node{ID: "next", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "next"})

	recorder := &recordingFreeformInterviewer{freeformResponse: "build me a REST API"}
	h := NewHumanHandler(recorder, graph)
	node := graph.Nodes["gate"]
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected 'success', got %q", outcome.Status)
	}
	if recorder.freeformPromptReceived != "What would you like to do?" {
		t.Errorf("expected freeform prompt 'What would you like to do?', got %q", recorder.freeformPromptReceived)
	}
	// Freeform response stored in context updates
	resp, ok := outcome.ContextUpdates["human_response"]
	if !ok {
		t.Fatal("expected human_response in context updates")
	}
	if resp != "build me a REST API" {
		t.Errorf("expected 'build me a REST API', got %q", resp)
	}
}

func TestHumanHandlerFreeformIncludesLastResponse(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Label: "Brainstorm with Human",
		Attrs: map[string]string{"mode": "freeform"},
	})
	graph.AddNode(&pipeline.Node{ID: "next", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "next"})

	recorder := &recordingFreeformInterviewer{freeformResponse: "let's use Redis"}
	h := NewHumanHandler(recorder, graph)
	node := graph.Nodes["gate"]
	pctx := pipeline.NewPipelineContext()
	// Simulate previous LLM node setting last_response
	pctx.Set(pipeline.ContextKeyLastResponse, "What caching strategy should we use?")

	_, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The prompt shown to the human should include the previous node's output
	if !strings.Contains(recorder.freeformPromptReceived, "What caching strategy should we use?") {
		t.Errorf("expected last_response in freeform prompt, got %q", recorder.freeformPromptReceived)
	}
	// The static label should still be present
	if !strings.Contains(recorder.freeformPromptReceived, "Brainstorm with Human") {
		t.Errorf("expected node label in freeform prompt, got %q", recorder.freeformPromptReceived)
	}
}

func TestHumanHandlerChoiceIncludesLastResponse(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Label: "Review the proposal",
	})
	graph.AddNode(&pipeline.Node{ID: "approve", Shape: "box"})
	graph.AddNode(&pipeline.Node{ID: "reject", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "approve", Label: "approve"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "reject", Label: "reject"})

	recorder := &recordingInterviewer{response: "approve"}
	h := NewHumanHandler(recorder, graph)
	node := graph.Nodes["gate"]
	pctx := pipeline.NewPipelineContext()
	pctx.Set(pipeline.ContextKeyLastResponse, "Here is my proposal for the API design...")

	_, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(recorder.promptReceived, "Here is my proposal for the API design...") {
		t.Errorf("expected last_response in choice prompt, got %q", recorder.promptReceived)
	}
	if !strings.Contains(recorder.promptReceived, "Review the proposal") {
		t.Errorf("expected node label in choice prompt, got %q", recorder.promptReceived)
	}
}

func TestHumanHandlerFreeformFallsBackToChoiceMode(t *testing.T) {
	// When interviewer does NOT implement FreeformInterviewer,
	// freeform mode should error clearly.
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Label: "What do you want?",
		Attrs: map[string]string{"mode": "freeform"},
	})
	graph.AddNode(&pipeline.Node{ID: "next", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "next"})

	h := NewHumanHandler(&AutoApproveInterviewer{}, graph)
	node := graph.Nodes["gate"]
	pctx := pipeline.NewPipelineContext()

	_, err := h.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected error when interviewer does not support freeform")
	}
}

func TestAutoApproveInterviewerFreeform(t *testing.T) {
	interviewer := &AutoApproveFreeformInterviewer{}
	resp, err := interviewer.AskFreeform("What do you want?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "auto-approved" {
		t.Errorf("expected 'auto-approved', got %q", resp)
	}
}

func TestConsoleInterviewerFreeform(t *testing.T) {
	input := "build me a REST API\n"
	reader := strings.NewReader(input)
	var output strings.Builder

	interviewer := &ConsoleInterviewer{Reader: reader, Writer: &output}
	resp, err := interviewer.AskFreeform("What would you like to do?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "build me a REST API" {
		t.Errorf("expected 'build me a REST API', got %q", resp)
	}
	if !strings.Contains(output.String(), "What would you like to do?") {
		t.Errorf("expected prompt in output, got %q", output.String())
	}
}

func TestConsoleInterviewerFreeformEmptyInput(t *testing.T) {
	input := "\n"
	reader := strings.NewReader(input)
	var output strings.Builder

	interviewer := &ConsoleInterviewer{Reader: reader, Writer: &output}
	_, err := interviewer.AskFreeform("What would you like to do?")
	if err == nil {
		t.Fatal("expected error for empty freeform input")
	}
}

func TestManagerLoopHandlerIsRegistered(t *testing.T) {
	graph := pipeline.NewGraph("test")
	registry := NewDefaultRegistry(graph)
	if !registry.Has("stack.manager_loop") {
		t.Fatal("expected stack.manager_loop to be registered")
	}
}
