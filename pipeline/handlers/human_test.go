// ABOUTME: Tests for the human gate handler and interviewer implementations.
// ABOUTME: Validates AutoApproveInterviewer and human handler choice presentation.
package handlers

import (
	"bytes"
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
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected success even on cancel, got %q", outcome.Status)
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
