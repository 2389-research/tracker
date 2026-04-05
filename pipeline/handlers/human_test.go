// ABOUTME: Tests for the human gate handler and interviewer implementations.
// ABOUTME: Validates AutoApproveInterviewer and human handler choice presentation.
package handlers

import (
	"bytes"
	"context"
	"fmt"
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

func TestHumanHandler_FreeformWritesPerNodeResponse(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "mygate",
		Shape: "hexagon",
		Label: "Tell me something",
		Attrs: map[string]string{"mode": "freeform"},
	})
	graph.AddNode(&pipeline.Node{ID: "next", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "mygate", To: "next"})

	recorder := &recordingFreeformInterviewer{freeformResponse: "user input here"}
	h := NewHumanHandler(recorder, graph)
	node := graph.Nodes["mygate"]
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.ContextUpdates[pipeline.ContextKeyHumanResponse] != "user input here" {
		t.Errorf("human_response = %q, want %q", outcome.ContextUpdates[pipeline.ContextKeyHumanResponse], "user input here")
	}
	perNodeKey := pipeline.ContextKeyResponsePrefix + "mygate"
	if outcome.ContextUpdates[perNodeKey] != "user input here" {
		t.Errorf("%s = %q, want %q", perNodeKey, outcome.ContextUpdates[perNodeKey], "user input here")
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

func TestAutoApproveFreeformInterviewer_AskInterview(t *testing.T) {
	ai := &AutoApproveFreeformInterviewer{}
	questions := []Question{
		{Index: 1, Text: "Auth model?", Options: []string{"API key", "OAuth", "JWT"}},
		{Index: 2, Text: "Need real-time?", IsYesNo: true},
		{Index: 3, Text: "Describe integrations"},
	}
	result, err := ai.AskInterview(questions, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Questions) != 3 {
		t.Fatalf("expected 3 answers, got %d", len(result.Questions))
	}
	// First option selected for select question
	if result.Questions[0].Answer != "API key" {
		t.Errorf("expected 'API key', got %q", result.Questions[0].Answer)
	}
	// Yes for yes/no
	if result.Questions[1].Answer != "yes" {
		t.Errorf("expected 'yes', got %q", result.Questions[1].Answer)
	}
	// Auto-approved for open-ended
	if result.Questions[2].Answer != "auto-approved" {
		t.Errorf("expected 'auto-approved', got %q", result.Questions[2].Answer)
	}
	// Not canceled or incomplete
	if result.Canceled || result.Incomplete {
		t.Error("expected non-canceled, non-incomplete result")
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

func TestConsoleInterviewerAskOutputHasNoANSI(t *testing.T) {
	var out bytes.Buffer
	ci := &ConsoleInterviewer{
		Reader: strings.NewReader("approve\n"),
		Writer: &out,
	}
	_, err := ci.Ask("Pick one", []string{"approve", "reject"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\033") {
		t.Error("console output contains ANSI escape sequences")
	}
}

func TestConsoleInterviewerAskFreeformOutputHasNoANSI(t *testing.T) {
	var out bytes.Buffer
	ci := &ConsoleInterviewer{
		Reader: strings.NewReader("my response\n"),
		Writer: &out,
	}
	_, err := ci.AskFreeform("Tell me something")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\033") {
		t.Error("console output contains ANSI escape sequences")
	}
}

func TestManagerLoopHandlerIsRegistered(t *testing.T) {
	graph := pipeline.NewGraph("test")
	registry := NewDefaultRegistry(graph)
	if !registry.Has("stack.manager_loop") {
		t.Fatal("expected stack.manager_loop to be registered")
	}
}

// --- Interview mode tests ---

// mockInterviewInterviewer is a mock that implements InterviewInterviewer.
// It embeds AutoApproveFreeformInterviewer so the freeform fallback path works.
type mockInterviewInterviewer struct {
	AutoApproveFreeformInterviewer
	questionsReceived []Question
	previousReceived  *InterviewResult
	result            *InterviewResult
	err               error
}

func (m *mockInterviewInterviewer) AskInterview(qs []Question, prev *InterviewResult) (*InterviewResult, error) {
	m.questionsReceived = qs
	m.previousReceived = prev
	return m.result, m.err
}

func TestHumanHandler_InterviewMode_HappyPath(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Attrs: map[string]string{"mode": "interview"},
	})
	graph.AddNode(&pipeline.Node{ID: "next", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "next"})

	pctx := pipeline.NewPipelineContext()
	pctx.Set("interview_questions", "1. What auth model? (API key, OAuth)\n2. Describe integrations.")

	expected := &InterviewResult{
		Questions: []InterviewAnswer{
			{ID: "q1", Text: "What auth model?", Options: []string{"API key", "OAuth"}, Answer: "OAuth"},
			{ID: "q2", Text: "Describe integrations.", Answer: "Salesforce nightly sync"},
		},
	}
	mock := &mockInterviewInterviewer{result: expected}
	h := NewHumanHandler(mock, graph)
	node := graph.Nodes["gate"]

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected success, got %q", outcome.Status)
	}

	// interview_answers key should contain JSON
	jsonStr, ok := outcome.ContextUpdates["interview_answers"]
	if !ok || jsonStr == "" {
		t.Fatal("expected interview_answers in context updates")
	}
	got, err := DeserializeInterviewResult(jsonStr)
	if err != nil {
		t.Fatalf("failed to deserialize interview_answers: %v", err)
	}
	if len(got.Questions) != 2 {
		t.Errorf("expected 2 questions in result, got %d", len(got.Questions))
	}

	// human_response key should contain markdown summary
	summary, ok := outcome.ContextUpdates[pipeline.ContextKeyHumanResponse]
	if !ok || summary == "" {
		t.Fatal("expected human_response in context updates")
	}
	if !strings.Contains(summary, "## Interview Answers") {
		t.Errorf("expected markdown summary header, got %q", summary)
	}

	// Questions should have been passed to the mock
	if len(mock.questionsReceived) != 2 {
		t.Errorf("expected 2 questions passed to AskInterview, got %d", len(mock.questionsReceived))
	}
}

func TestHumanHandler_InterviewMode_ZeroQuestions(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Label: "Please clarify",
		Attrs: map[string]string{"mode": "interview"},
	})
	graph.AddNode(&pipeline.Node{ID: "next", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "next"})

	pctx := pipeline.NewPipelineContext()
	// Text that does not parse as any questions
	pctx.Set("interview_questions", "No further questions needed.")

	mock := &mockInterviewInterviewer{}
	h := NewHumanHandler(mock, graph)
	node := graph.Nodes["gate"]

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected success, got %q", outcome.Status)
	}
	// Falls back to freeform — human_response should be set (auto-approved)
	resp, ok := outcome.ContextUpdates[pipeline.ContextKeyHumanResponse]
	if !ok || resp == "" {
		t.Fatal("expected human_response in context updates from freeform fallback")
	}
	// Freeform fallback should also persist under interview_answers key.
	if _, ok := outcome.ContextUpdates["interview_answers"]; !ok {
		t.Error("expected freeform fallback to also set interview_answers key")
	}
	// AskInterview should NOT have been called
	if mock.questionsReceived != nil {
		t.Error("expected AskInterview not to be called on zero-questions fallback")
	}
}

func TestHumanHandler_InterviewMode_MissingQuestionsKey(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Attrs: map[string]string{"mode": "interview", "questions_key": "custom_key"},
	})
	graph.AddNode(&pipeline.Node{ID: "next", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "next"})

	pctx := pipeline.NewPipelineContext()
	// custom_key not set; last_response has valid questions
	pctx.Set(pipeline.ContextKeyLastResponse, "1. What scale? (low, high)\n2. Describe deployment.")

	expected := &InterviewResult{
		Questions: []InterviewAnswer{
			{ID: "q1", Text: "What scale?", Options: []string{"low", "high"}, Answer: "high"},
		},
	}
	mock := &mockInterviewInterviewer{result: expected}
	h := NewHumanHandler(mock, graph)
	node := graph.Nodes["gate"]

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected success, got %q", outcome.Status)
	}
	// Should have parsed questions from last_response and called AskInterview
	if len(mock.questionsReceived) == 0 {
		t.Error("expected questions to be parsed from last_response fallback")
	}
}

func TestHumanHandler_InterviewMode_RetryPreFill(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Attrs: map[string]string{"mode": "interview"},
	})
	graph.AddNode(&pipeline.Node{ID: "next", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "next"})

	pctx := pipeline.NewPipelineContext()
	pctx.Set("interview_questions", "1. What auth model? (API key, OAuth)")

	// Store previous answers
	prev := InterviewResult{
		Questions: []InterviewAnswer{
			{ID: "q1", Text: "What auth model?", Options: []string{"API key", "OAuth"}, Answer: "API key"},
		},
	}
	pctx.Set("interview_answers", SerializeInterviewResult(prev))

	final := &InterviewResult{
		Questions: []InterviewAnswer{
			{ID: "q1", Text: "What auth model?", Options: []string{"API key", "OAuth"}, Answer: "OAuth"},
		},
	}
	mock := &mockInterviewInterviewer{result: final}
	h := NewHumanHandler(mock, graph)
	node := graph.Nodes["gate"]

	_, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Previous answers should have been passed for pre-fill
	if mock.previousReceived == nil {
		t.Fatal("expected previousReceived to be non-nil")
	}
	if len(mock.previousReceived.Questions) != 1 || mock.previousReceived.Questions[0].Answer != "API key" {
		t.Errorf("unexpected previousReceived: %+v", mock.previousReceived)
	}
}

func TestHumanHandler_InterviewMode_NotInterviewInterviewer(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Attrs: map[string]string{"mode": "interview"},
	})
	graph.AddNode(&pipeline.Node{ID: "next", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "next"})

	pctx := pipeline.NewPipelineContext()
	pctx.Set("interview_questions", "1. What auth model?")

	// AutoApproveInterviewer does NOT implement InterviewInterviewer
	h := NewHumanHandler(&AutoApproveInterviewer{}, graph)
	node := graph.Nodes["gate"]

	_, err := h.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected error when interviewer does not support interviews")
	}
	if !strings.Contains(err.Error(), "does not support interviews") {
		t.Errorf("expected 'does not support interviews' in error, got %q", err.Error())
	}
}

func TestHumanHandler_InterviewMode_Canceled(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Attrs: map[string]string{"mode": "interview"},
	})
	graph.AddNode(&pipeline.Node{ID: "next", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "next"})

	pctx := pipeline.NewPipelineContext()
	pctx.Set("interview_questions", "1. What auth model? (API key, OAuth)\n2. Scale?")

	canceled := &InterviewResult{
		Questions: []InterviewAnswer{
			{ID: "q1", Text: "What auth model?", Answer: "API key"},
		},
		Canceled: true,
	}
	mock := &mockInterviewInterviewer{result: canceled}
	h := NewHumanHandler(mock, graph)
	node := graph.Nodes["gate"]

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error (canceled should not error): %v", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected fail on cancel, got %q", outcome.Status)
	}
	// Partial answers should still be stored
	jsonStr, ok := outcome.ContextUpdates["interview_answers"]
	if !ok || jsonStr == "" {
		t.Fatal("expected interview_answers to be stored even when canceled")
	}
	result, err := DeserializeInterviewResult(jsonStr)
	if err != nil {
		t.Fatalf("failed to deserialize: %v", err)
	}
	if !result.Canceled {
		t.Error("expected Canceled=true in stored result")
	}
}

func TestConsoleInterviewer_AskInterview(t *testing.T) {
	// Input: select "OAuth", yes/no "y", freeform "Salesforce sync"
	input := "OAuth\ny\nSalesforce sync\n"
	var output bytes.Buffer
	ci := &ConsoleInterviewer{Reader: strings.NewReader(input), Writer: &output}

	questions := []Question{
		{Index: 1, Text: "Auth model?", Options: []string{"API key", "OAuth", "JWT"}},
		{Index: 2, Text: "Need real-time?", IsYesNo: true},
		{Index: 3, Text: "Describe integrations"},
	}
	result, err := ci.AskInterview(questions, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Questions) != 3 {
		t.Fatalf("expected 3 answers, got %d", len(result.Questions))
	}
	if result.Questions[0].Answer != "OAuth" {
		t.Errorf("expected 'OAuth', got %q", result.Questions[0].Answer)
	}
	if result.Questions[1].Answer != "yes" {
		t.Errorf("expected 'yes', got %q", result.Questions[1].Answer)
	}
	if result.Questions[2].Answer != "Salesforce sync" {
		t.Errorf("expected 'Salesforce sync', got %q", result.Questions[2].Answer)
	}
}

func TestConsoleInterviewer_AskInterview_ByNumber(t *testing.T) {
	input := "2\n"
	var output bytes.Buffer
	ci := &ConsoleInterviewer{Reader: strings.NewReader(input), Writer: &output}

	questions := []Question{
		{Index: 1, Text: "Auth?", Options: []string{"API key", "OAuth", "JWT"}},
	}
	result, err := ci.AskInterview(questions, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Questions[0].Answer != "OAuth" {
		t.Errorf("expected 'OAuth', got %q", result.Questions[0].Answer)
	}
}

func TestConsoleInterviewer_AskInterview_Skip(t *testing.T) {
	input := "\n" // blank = skip
	var output bytes.Buffer
	ci := &ConsoleInterviewer{Reader: strings.NewReader(input), Writer: &output}

	questions := []Question{
		{Index: 1, Text: "Auth?", Options: []string{"API key", "OAuth"}},
	}
	result, err := ci.AskInterview(questions, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Questions[0].Answer != "" {
		t.Errorf("expected empty (skipped), got %q", result.Questions[0].Answer)
	}
}

func TestConsoleInterviewer_AskInterview_BlankPreservesPrevious(t *testing.T) {
	input := "\n\n" // blank for both questions — should preserve previous
	var output bytes.Buffer
	ci := &ConsoleInterviewer{Reader: strings.NewReader(input), Writer: &output}

	questions := []Question{
		{Index: 1, Text: "Auth?", Options: []string{"OAuth", "SAML"}},
		{Index: 2, Text: "Scale?", IsYesNo: true},
	}
	prev := &InterviewResult{
		Questions: []InterviewAnswer{
			{ID: "q1", Text: "Auth?", Answer: "OAuth"},
			{ID: "q2", Text: "Scale?", Answer: "yes"},
		},
	}
	result, err := ci.AskInterview(questions, prev)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Questions[0].Answer != "OAuth" {
		t.Errorf("expected previous 'OAuth' preserved, got %q", result.Questions[0].Answer)
	}
	if result.Questions[1].Answer != "yes" {
		t.Errorf("expected previous 'yes' preserved, got %q", result.Questions[1].Answer)
	}
}

func TestHumanHandler_InterviewMode_CustomKeys(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Attrs: map[string]string{
			"mode":          "interview",
			"questions_key": "my_qs",
			"answers_key":   "my_ans",
		},
	})
	graph.AddNode(&pipeline.Node{ID: "next", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "next"})

	pctx := pipeline.NewPipelineContext()
	pctx.Set("my_qs", "1. What deployment target? (k8s, VM)")

	expected := &InterviewResult{
		Questions: []InterviewAnswer{
			{ID: "q1", Text: "What deployment target?", Options: []string{"k8s", "VM"}, Answer: "k8s"},
		},
	}
	mock := &mockInterviewInterviewer{result: expected}
	h := NewHumanHandler(mock, graph)
	node := graph.Nodes["gate"]

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected success, got %q", outcome.Status)
	}

	// Answers must be written under "my_ans"
	_, ok := outcome.ContextUpdates["my_ans"]
	if !ok {
		t.Error("expected answers written to 'my_ans' key")
	}
	// Default key should NOT be set
	if _, ok := outcome.ContextUpdates["interview_answers"]; ok {
		t.Error("expected default 'interview_answers' key NOT to be set when custom answers_key is specified")
	}
}

func TestHumanHandler_InterviewMode_CanceledZeroAnswers(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Attrs: map[string]string{"mode": "interview"},
	})
	graph.AddNode(&pipeline.Node{ID: "next", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "next"})

	pctx := pipeline.NewPipelineContext()
	pctx.Set("interview_questions", "1. What auth model?\n2. Scale?")

	// User pressed Esc immediately — zero answers, canceled.
	canceled := &InterviewResult{
		Questions: []InterviewAnswer{
			{ID: "q1", Text: "What auth model?"},
			{ID: "q2", Text: "Scale?"},
		},
		Canceled: true,
	}
	mock := &mockInterviewInterviewer{result: canceled}
	h := NewHumanHandler(mock, graph)
	node := graph.Nodes["gate"]

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected fail on cancel, got %q", outcome.Status)
	}
	// Partial answers (even zero) should be stored
	jsonStr, ok := outcome.ContextUpdates["interview_answers"]
	if !ok || jsonStr == "" {
		t.Fatal("expected interview_answers to be stored even on immediate cancel")
	}
	result, err := DeserializeInterviewResult(jsonStr)
	if err != nil {
		t.Fatalf("failed to deserialize: %v", err)
	}
	if !result.Canceled {
		t.Error("expected Canceled=true")
	}
	// Markdown summary should note zero answers
	summary := outcome.ContextUpdates[pipeline.ContextKeyHumanResponse]
	if !strings.Contains(summary, "0 of 2 questions answered") {
		t.Errorf("expected '0 of 2 questions answered' in summary, got %q", summary)
	}
}

func TestHumanHandler_InterviewMode_AskInterviewError(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Attrs: map[string]string{"mode": "interview"},
	})
	graph.AddNode(&pipeline.Node{ID: "next", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "next"})

	pctx := pipeline.NewPipelineContext()
	pctx.Set("interview_questions", "1. What auth model?")

	mock := &mockInterviewInterviewer{err: fmt.Errorf("connection lost")}
	h := NewHumanHandler(mock, graph)
	node := graph.Nodes["gate"]

	_, err := h.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected error from AskInterview failure")
	}
	if !strings.Contains(err.Error(), "connection lost") {
		t.Errorf("expected 'connection lost' in error, got %q", err.Error())
	}
}

// blockingInterviewer blocks forever on all methods — used to test timeouts.
type blockingInterviewer struct{}

func (b *blockingInterviewer) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	select {} // block forever
}

func (b *blockingInterviewer) AskFreeform(prompt string) (string, error) {
	select {} // block forever
}

func (b *blockingInterviewer) AskFreeformWithLabels(prompt string, labels []string, defaultLabel string) (string, error) {
	select {} // block forever
}

func (b *blockingInterviewer) AskInterview(questions []Question, previousAnswers *InterviewResult) (*InterviewResult, error) {
	select {} // block forever
}

func TestHumanHandler_FreeformTimeout(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Label: "Give input",
		Attrs: map[string]string{
			"mode":           "freeform",
			"timeout":        "100ms",
			"timeout_action": "fail",
		},
	})
	graph.AddNode(&pipeline.Node{ID: "next", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "next"})

	h := NewHumanHandler(&blockingInterviewer{}, graph)
	node := graph.Nodes["gate"]
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected OutcomeFail on timeout with timeout_action=fail, got %q", outcome.Status)
	}
}

func TestHumanHandler_TimeoutUsesDefault(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Label: "Approve?",
		Attrs: map[string]string{
			"timeout":        "100ms",
			"default_choice": "approved",
		},
	})
	graph.AddNode(&pipeline.Node{ID: "approved", Shape: "box"})
	graph.AddNode(&pipeline.Node{ID: "rejected", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "approved", Label: "approved"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "rejected", Label: "rejected"})

	h := NewHumanHandler(&blockingInterviewer{}, graph)
	node := graph.Nodes["gate"]
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected OutcomeSuccess when default_choice is set, got %q", outcome.Status)
	}
	if outcome.PreferredLabel != "approved" {
		t.Errorf("expected PreferredLabel 'approved', got %q", outcome.PreferredLabel)
	}
	if outcome.ContextUpdates[pipeline.ContextKeyHumanResponse] != "approved" {
		t.Errorf("expected human_response 'approved', got %q", outcome.ContextUpdates[pipeline.ContextKeyHumanResponse])
	}
}

// --- Human gate correctness: all modes must route correctly ---

// yesNoInterviewer simulates a user picking a specific choice from the presented options.
type yesNoInterviewer struct {
	pick string // the choice to pick (e.g., "Yes" or "No")
}

func (y *yesNoInterviewer) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	for _, c := range choices {
		if c == y.pick {
			return c, nil
		}
	}
	return "", fmt.Errorf("choice %q not found in %v", y.pick, choices)
}

// TestYesNoMode_YesReturnsSuccess verifies that picking Yes in yes_no mode
// returns OutcomeSuccess so ctx.outcome = success conditions match.
func TestYesNoMode_YesReturnsSuccess(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Label: "Is it alive?",
		Attrs: map[string]string{"mode": "yes_no"},
	})
	graph.AddNode(&pipeline.Node{ID: "yes_path", Shape: "box"})
	graph.AddNode(&pipeline.Node{ID: "no_path", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "yes_path", Label: "[Y] Yes", Condition: "ctx.outcome = success"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "no_path", Label: "[N] No", Condition: "ctx.outcome = fail"})

	h := NewHumanHandler(&yesNoInterviewer{pick: "Yes"}, graph)
	node := graph.Nodes["gate"]
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected OutcomeSuccess for Yes, got %q", outcome.Status)
	}
}

// TestYesNoMode_NoReturnsFail verifies that picking No in yes_no mode
// returns OutcomeFail so ctx.outcome = fail conditions match.
func TestYesNoMode_NoReturnsFail(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Label: "Is it alive?",
		Attrs: map[string]string{"mode": "yes_no"},
	})
	graph.AddNode(&pipeline.Node{ID: "yes_path", Shape: "box"})
	graph.AddNode(&pipeline.Node{ID: "no_path", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "yes_path", Label: "[Y] Yes", Condition: "ctx.outcome = success"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "no_path", Label: "[N] No", Condition: "ctx.outcome = fail"})

	h := NewHumanHandler(&yesNoInterviewer{pick: "No"}, graph)
	node := graph.Nodes["gate"]
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected OutcomeFail for No, got %q", outcome.Status)
	}
}

// TestYesNoMode_PresentsYesNoChoices verifies that yes_no mode presents
// exactly ["Yes", "No"] as choices, not edge labels.
func TestYesNoMode_PresentsYesNoChoices(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.AddNode(&pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Label: "Approve?",
		Attrs: map[string]string{"mode": "yes_no"},
	})
	graph.AddNode(&pipeline.Node{ID: "a", Shape: "box"})
	graph.AddNode(&pipeline.Node{ID: "b", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "a", Label: "[Y] Ship it", Condition: "ctx.outcome = success"})
	graph.AddEdge(&pipeline.Edge{From: "gate", To: "b", Label: "[N] Reject", Condition: "ctx.outcome = fail"})

	recorder := &recordingInterviewer{response: "Yes"}
	h := NewHumanHandler(recorder, graph)
	node := graph.Nodes["gate"]
	pctx := pipeline.NewPipelineContext()

	_, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recorder.choicesReceived) != 2 {
		t.Fatalf("expected 2 choices, got %d: %v", len(recorder.choicesReceived), recorder.choicesReceived)
	}
	if recorder.choicesReceived[0] != "Yes" || recorder.choicesReceived[1] != "No" {
		t.Errorf("expected [Yes, No], got %v", recorder.choicesReceived)
	}
}

// TestAllGateModes_CorrectRouting is a comprehensive test verifying that every
// human gate mode produces the correct outcome for edge routing.
func TestAllGateModes_CorrectRouting(t *testing.T) {
	t.Run("choice mode: outcome is always success, routing by preferred label", func(t *testing.T) {
		graph := pipeline.NewGraph("test")
		graph.AddNode(&pipeline.Node{ID: "gate", Shape: "hexagon", Label: "Pick"})
		graph.AddNode(&pipeline.Node{ID: "a", Shape: "box"})
		graph.AddNode(&pipeline.Node{ID: "b", Shape: "box"})
		graph.AddEdge(&pipeline.Edge{From: "gate", To: "a", Label: "alpha"})
		graph.AddEdge(&pipeline.Edge{From: "gate", To: "b", Label: "beta"})

		for _, pick := range []string{"alpha", "beta"} {
			h := NewHumanHandler(&recordingInterviewer{response: pick}, graph)
			outcome, err := h.Execute(context.Background(), graph.Nodes["gate"], pipeline.NewPipelineContext())
			if err != nil {
				t.Fatalf("pick=%s: unexpected error: %v", pick, err)
			}
			if outcome.Status != pipeline.OutcomeSuccess {
				t.Errorf("pick=%s: choice mode should always return OutcomeSuccess, got %q", pick, outcome.Status)
			}
			if outcome.PreferredLabel != pick {
				t.Errorf("pick=%s: expected PreferredLabel %q, got %q", pick, pick, outcome.PreferredLabel)
			}
		}
	})

	t.Run("yes_no mode: Yes=success, No=fail", func(t *testing.T) {
		graph := pipeline.NewGraph("test")
		graph.AddNode(&pipeline.Node{
			ID: "gate", Shape: "hexagon", Label: "Ready?",
			Attrs: map[string]string{"mode": "yes_no"},
		})
		graph.AddNode(&pipeline.Node{ID: "yes_dest", Shape: "box"})
		graph.AddNode(&pipeline.Node{ID: "no_dest", Shape: "box"})
		graph.AddEdge(&pipeline.Edge{From: "gate", To: "yes_dest", Condition: "ctx.outcome = success"})
		graph.AddEdge(&pipeline.Edge{From: "gate", To: "no_dest", Condition: "ctx.outcome = fail"})

		// Yes → success
		h := NewHumanHandler(&yesNoInterviewer{pick: "Yes"}, graph)
		outcome, err := h.Execute(context.Background(), graph.Nodes["gate"], pipeline.NewPipelineContext())
		if err != nil {
			t.Fatalf("Yes: unexpected error: %v", err)
		}
		if outcome.Status != pipeline.OutcomeSuccess {
			t.Errorf("Yes: expected OutcomeSuccess, got %q", outcome.Status)
		}

		// No → fail
		h = NewHumanHandler(&yesNoInterviewer{pick: "No"}, graph)
		outcome, err = h.Execute(context.Background(), graph.Nodes["gate"], pipeline.NewPipelineContext())
		if err != nil {
			t.Fatalf("No: unexpected error: %v", err)
		}
		if outcome.Status != pipeline.OutcomeFail {
			t.Errorf("No: expected OutcomeFail, got %q", outcome.Status)
		}
	})

	t.Run("freeform mode: outcome is success with human_response set", func(t *testing.T) {
		graph := pipeline.NewGraph("test")
		graph.AddNode(&pipeline.Node{
			ID: "gate", Shape: "hexagon", Label: "Describe the bug",
			Attrs: map[string]string{"mode": "freeform"},
		})
		graph.AddNode(&pipeline.Node{ID: "next", Shape: "box"})
		graph.AddEdge(&pipeline.Edge{From: "gate", To: "next"})

		recorder := &recordingFreeformInterviewer{freeformResponse: "the login page crashes"}
		h := NewHumanHandler(recorder, graph)
		outcome, err := h.Execute(context.Background(), graph.Nodes["gate"], pipeline.NewPipelineContext())
		if err != nil {
			t.Fatalf("freeform: unexpected error: %v", err)
		}
		if outcome.Status != pipeline.OutcomeSuccess {
			t.Errorf("freeform: expected OutcomeSuccess, got %q", outcome.Status)
		}
		if outcome.ContextUpdates[pipeline.ContextKeyHumanResponse] != "the login page crashes" {
			t.Errorf("freeform: expected human_response to be set, got %q", outcome.ContextUpdates[pipeline.ContextKeyHumanResponse])
		}
	})

	t.Run("interview mode: success on completion, fail on cancel", func(t *testing.T) {
		graph := pipeline.NewGraph("test")
		graph.AddNode(&pipeline.Node{
			ID: "gate", Shape: "hexagon",
			Attrs: map[string]string{"mode": "interview"},
		})
		graph.AddNode(&pipeline.Node{ID: "next", Shape: "box"})
		graph.AddEdge(&pipeline.Edge{From: "gate", To: "next"})

		pctx := pipeline.NewPipelineContext()
		pctx.Set("interview_questions", "1. What language? (Go, Python)\n2. Framework?")

		// Completed interview → success
		mock := &mockInterviewInterviewer{
			result: &InterviewResult{
				Questions: []InterviewAnswer{{Answer: "Go"}, {Answer: "Gin"}},
			},
		}
		h := NewHumanHandler(mock, graph)
		outcome, err := h.Execute(context.Background(), graph.Nodes["gate"], pctx)
		if err != nil {
			t.Fatalf("interview complete: unexpected error: %v", err)
		}
		if outcome.Status != pipeline.OutcomeSuccess {
			t.Errorf("interview complete: expected OutcomeSuccess, got %q", outcome.Status)
		}

		// Canceled interview → fail
		mockCancel := &mockInterviewInterviewer{
			result: &InterviewResult{
				Canceled:  true,
				Questions: []InterviewAnswer{{Answer: "Go"}},
			},
		}
		h = NewHumanHandler(mockCancel, graph)
		outcome, err = h.Execute(context.Background(), graph.Nodes["gate"], pctx)
		if err != nil {
			t.Fatalf("interview cancel: unexpected error: %v", err)
		}
		if outcome.Status != pipeline.OutcomeFail {
			t.Errorf("interview cancel: expected OutcomeFail, got %q", outcome.Status)
		}
	})
}
