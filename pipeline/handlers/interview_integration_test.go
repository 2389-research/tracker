// ABOUTME: End-to-end integration tests for the interview mode handler flow.
// ABOUTME: Exercises the full path: context → ParseQuestions → AskInterview → context updates.
package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func TestInterview_Integration(t *testing.T) {
	// 1. Build a graph with an interview node
	graph := pipeline.NewGraph("test-interview")
	graph.AddNode(&pipeline.Node{
		ID:    "interview",
		Shape: "hexagon",
		Label: "Answer the interviewer's questions.",
		Attrs: map[string]string{
			"mode":          "interview",
			"questions_key": "interview_questions",
			"answers_key":   "interview_answers",
		},
	})
	graph.AddNode(&pipeline.Node{ID: "done", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "interview", To: "done"})

	// 2. Set questions in context (simulating upstream agent output)
	pctx := pipeline.NewPipelineContext()
	pctx.Set("interview_questions", `Here are my questions:

1. Who are the API consumers? (internal services, third-party devs, mobile app)
2. What operations go beyond CRUD? (search, bulk, export, webhooks)
3. What are the auth requirements? (public, API key, OAuth, per-tenant)
4. Are there real-time needs? (yes, no)
5. Describe any existing systems this must integrate with.
`)

	// 3. Use AutoApproveFreeformInterviewer (implements InterviewInterviewer)
	interviewer := &AutoApproveFreeformInterviewer{}
	handler := NewHumanHandler(interviewer, graph)

	// 4. Execute
	node := graph.Nodes["interview"]
	outcome, err := handler.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 5. Verify outcome
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected success, got %q", outcome.Status)
	}

	// 6. Verify answers_key in context updates
	answersJSON, ok := outcome.ContextUpdates["interview_answers"]
	if !ok || answersJSON == "" {
		t.Fatal("expected interview_answers in context updates")
	}

	// Parse and verify the JSON
	result, err := DeserializeInterviewResult(answersJSON)
	if err != nil {
		t.Fatalf("failed to deserialize answers: %v", err)
	}
	if len(result.Questions) != 5 {
		t.Fatalf("expected 5 answers, got %d", len(result.Questions))
	}
	// Auto-approve picks first option for select questions
	if result.Questions[0].Answer != "internal services" {
		t.Errorf("Q1: expected 'internal services', got %q", result.Questions[0].Answer)
	}
	// Yes/no: auto-approve picks first option "yes" (q.Options[0] since options are set)
	if result.Questions[3].Answer != "yes" {
		t.Errorf("Q4: expected 'yes', got %q", result.Questions[3].Answer)
	}
	// Open-ended: auto-approve returns "auto-approved"
	if result.Questions[4].Answer != "auto-approved" {
		t.Errorf("Q5: expected 'auto-approved', got %q", result.Questions[4].Answer)
	}
	if result.Canceled {
		t.Error("expected non-canceled result")
	}

	// 7. Verify human_response (markdown summary)
	summary, ok := outcome.ContextUpdates[pipeline.ContextKeyHumanResponse]
	if !ok || summary == "" {
		t.Fatal("expected human_response in context updates")
	}
	if !strings.Contains(summary, "Interview Answers") {
		t.Error("expected markdown summary to contain 'Interview Answers'")
	}
}

func TestInterview_Integration_ZeroQuestions(t *testing.T) {
	// Build a graph with an interview node
	graph := pipeline.NewGraph("test-interview-zero")
	graph.AddNode(&pipeline.Node{
		ID:    "interview",
		Shape: "hexagon",
		Label: "Please provide any additional context.",
		Attrs: map[string]string{
			"mode":          "interview",
			"questions_key": "interview_questions",
			"answers_key":   "interview_answers",
		},
	})
	graph.AddNode(&pipeline.Node{ID: "done", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "interview", To: "done"})

	// Context has content with no parseable questions
	pctx := pipeline.NewPipelineContext()
	pctx.Set("interview_questions", "No further questions needed.")

	// Use AutoApproveFreeformInterviewer (also implements FreeformInterviewer for fallback)
	interviewer := &AutoApproveFreeformInterviewer{}
	handler := NewHumanHandler(interviewer, graph)

	node := graph.Nodes["interview"]
	outcome, err := handler.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Falls back to freeform — outcome is still success
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected success, got %q", outcome.Status)
	}

	// human_response is set from freeform fallback
	response, ok := outcome.ContextUpdates[pipeline.ContextKeyHumanResponse]
	if !ok || response == "" {
		t.Fatal("expected human_response in context updates for freeform fallback")
	}
}

// retryCapturingInterviewer records the previous answers passed to AskInterview.
type retryCapturingInterviewer struct {
	AutoApproveFreeformInterviewer
	capturedPrev *InterviewResult
}

func (r *retryCapturingInterviewer) AskInterview(questions []Question, prev *InterviewResult) (*InterviewResult, error) {
	r.capturedPrev = prev
	return r.AutoApproveFreeformInterviewer.AskInterview(questions, prev)
}

func TestInterview_Integration_RetryPreFill(t *testing.T) {
	// Build a graph with an interview node
	graph := pipeline.NewGraph("test-interview-retry")
	graph.AddNode(&pipeline.Node{
		ID:    "interview",
		Shape: "hexagon",
		Label: "Answer the interviewer's questions.",
		Attrs: map[string]string{
			"mode":          "interview",
			"questions_key": "interview_questions",
			"answers_key":   "interview_answers",
		},
	})
	graph.AddNode(&pipeline.Node{ID: "done", Shape: "box"})
	graph.AddEdge(&pipeline.Edge{From: "interview", To: "done"})

	pctx := pipeline.NewPipelineContext()
	pctx.Set("interview_questions", `
1. What is your primary use case?
2. Are there performance constraints? (yes, no)
`)

	// Set previous answers in context at answers_key to simulate retry pre-fill
	previousResult := InterviewResult{
		Questions: []InterviewAnswer{
			{ID: "q1", Text: "What is your primary use case?", Answer: "data analytics"},
			{ID: "q2", Text: "Are there performance constraints?", Answer: "yes"},
		},
	}
	pctx.Set("interview_answers", SerializeInterviewResult(previousResult))

	mock := &retryCapturingInterviewer{}
	handler := NewHumanHandler(mock, graph)

	node := graph.Nodes["interview"]
	outcome, err := handler.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected success, got %q", outcome.Status)
	}

	// Verify that the previous answers were passed through to the interviewer
	if mock.capturedPrev == nil {
		t.Fatal("expected previous answers to be passed to interviewer, got nil")
	}
	if len(mock.capturedPrev.Questions) != 2 {
		t.Errorf("expected 2 previous answers, got %d", len(mock.capturedPrev.Questions))
	}
	if mock.capturedPrev.Questions[0].Answer != "data analytics" {
		t.Errorf("expected previous Q1 answer 'data analytics', got %q", mock.capturedPrev.Questions[0].Answer)
	}
}
