// ABOUTME: Tests for actorOf — opportunistic Actor() interface assertion.
// ABOUTME: Verifies the fallback to ActorUnknown when an interviewer doesn't implement Actor().
package handlers

import (
	"fmt"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// stubInterviewerWithActor satisfies the Interviewer interface and exposes
// an Actor() method so actorOf's interface assertion succeeds.
type stubInterviewerWithActor struct {
	actor pipeline.Actor
}

func (s *stubInterviewerWithActor) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (s *stubInterviewerWithActor) Actor() pipeline.Actor { return s.actor }

// stubInterviewerWithoutActor satisfies the Interviewer interface but does NOT
// implement Actor(). actorOf should fall back to ActorUnknown.
type stubInterviewerWithoutActor struct{}

func (s *stubInterviewerWithoutActor) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func TestActorOf_KnownInterviewer(t *testing.T) {
	cases := []struct {
		name  string
		actor pipeline.Actor
	}{
		{"human", pipeline.ActorHuman},
		{"autopilot", pipeline.ActorAutopilot},
		{"webhook", pipeline.ActorWebhook},
		{"unknown", pipeline.ActorUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var iv Interviewer = &stubInterviewerWithActor{actor: tc.actor}
			got := actorOf(iv)
			if got != tc.actor {
				t.Errorf("actorOf returned %q, want %q", got, tc.actor)
			}
		})
	}
}

func TestActorOf_UnknownInterviewer(t *testing.T) {
	// An interviewer that doesn't implement Actor() falls back to ActorUnknown.
	var iv Interviewer = &stubInterviewerWithoutActor{}
	got := actorOf(iv)
	if got != pipeline.ActorUnknown {
		t.Errorf("actorOf for no-Actor() interviewer = %q, want %q",
			got, pipeline.ActorUnknown)
	}
}

func TestActorOf_NilInterviewer(t *testing.T) {
	// Nil interviewer → ActorUnknown (defensive; should not happen in practice).
	got := actorOf(nil)
	if got != pipeline.ActorUnknown {
		t.Errorf("actorOf(nil) = %q, want %q", got, pipeline.ActorUnknown)
	}
}

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
