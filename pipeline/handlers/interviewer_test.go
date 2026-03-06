package handlers

import "testing"

func TestCallbackInterviewerReturnsCallbackResponse(t *testing.T) {
	interviewer := &CallbackInterviewer{
		AskFunc: func(prompt string, choices []string, defaultChoice string) (string, error) {
			return choices[1], nil
		},
	}

	choice, err := interviewer.Ask("Pick", []string{"approve", "reject"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if choice != "reject" {
		t.Fatalf("choice = %q, want reject", choice)
	}
}

func TestQueueInterviewerReturnsQueuedAnswers(t *testing.T) {
	interviewer := &QueueInterviewer{Answers: []string{"reject", "approve"}}

	first, err := interviewer.Ask("Pick", []string{"approve", "reject"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first != "reject" {
		t.Fatalf("first = %q, want reject", first)
	}

	second, err := interviewer.Ask("Pick", []string{"approve", "reject"}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if second != "approve" {
		t.Fatalf("second = %q, want approve", second)
	}
}
