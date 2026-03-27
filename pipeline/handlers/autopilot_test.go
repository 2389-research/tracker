// ABOUTME: Tests for the autopilot interviewer — persona parsing, decision parsing, choice matching.
// ABOUTME: Uses mock responses rather than live LLM calls.
package handlers

import (
	"strings"
	"testing"
)

func TestParsePersonaValid(t *testing.T) {
	tests := []struct {
		input string
		want  Persona
	}{
		{"lax", PersonaLax},
		{"LAX", PersonaLax},
		{"mid", PersonaMid},
		{"", PersonaMid}, // default
		{"hard", PersonaHard},
		{"mentor", PersonaMentor},
		{" mid ", PersonaMid},
	}
	for _, tc := range tests {
		got, err := ParsePersona(tc.input)
		if err != nil {
			t.Errorf("ParsePersona(%q) error: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParsePersona(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParsePersonaInvalid(t *testing.T) {
	_, err := ParsePersona("aggressive")
	if err == nil {
		t.Fatal("expected error for unknown persona")
	}
	if !strings.Contains(err.Error(), "unknown autopilot persona") {
		t.Errorf("expected 'unknown autopilot persona' error, got: %v", err)
	}
}

func TestParseDecisionCleanJSON(t *testing.T) {
	input := `{"choice": "approve", "reasoning": "Plan looks solid"}`
	d, err := parseDecision(input)
	if err != nil {
		t.Fatalf("parseDecision error: %v", err)
	}
	if d.Choice != "approve" {
		t.Errorf("choice = %q, want %q", d.Choice, "approve")
	}
	if d.Reasoning != "Plan looks solid" {
		t.Errorf("reasoning = %q, want %q", d.Reasoning, "Plan looks solid")
	}
}

func TestParseDecisionWithMarkdownFences(t *testing.T) {
	input := "```json\n{\"choice\": \"mark done\", \"reasoning\": \"Tests pass\"}\n```"
	d, err := parseDecision(input)
	if err != nil {
		t.Fatalf("parseDecision error: %v", err)
	}
	if d.Choice != "mark done" {
		t.Errorf("choice = %q, want %q", d.Choice, "mark done")
	}
}

func TestParseDecisionWithSurroundingText(t *testing.T) {
	input := "Based on my analysis, here is my decision:\n{\"choice\": \"retry\", \"reasoning\": \"Not ready\"}\nThat's my call."
	d, err := parseDecision(input)
	if err != nil {
		t.Fatalf("parseDecision error: %v", err)
	}
	if d.Choice != "retry" {
		t.Errorf("choice = %q, want %q", d.Choice, "retry")
	}
}

func TestParseDecisionEmptyChoice(t *testing.T) {
	input := `{"choice": "", "reasoning": "dunno"}`
	_, err := parseDecision(input)
	if err == nil {
		t.Fatal("expected error for empty choice")
	}
}

func TestParseDecisionNoJSON(t *testing.T) {
	input := "I think we should approve this plan."
	_, err := parseDecision(input)
	if err == nil {
		t.Fatal("expected error for non-JSON response")
	}
}

func TestMatchChoiceExact(t *testing.T) {
	options := []string{"approve", "adjust", "reject"}
	if got := matchChoice("approve", options); got != "approve" {
		t.Errorf("matchChoice exact = %q, want %q", got, "approve")
	}
}

func TestMatchChoiceCaseInsensitive(t *testing.T) {
	options := []string{"mark done", "retry", "accept", "abandon"}
	if got := matchChoice("Mark Done", options); got != "mark done" {
		t.Errorf("matchChoice case = %q, want %q", got, "mark done")
	}
}

func TestMatchChoiceSubstring(t *testing.T) {
	options := []string{"approve", "adjust", "reject"}
	// LLM might respond with "approve the plan"
	if got := matchChoice("approve the plan", options); got != "approve" {
		t.Errorf("matchChoice substring = %q, want %q", got, "approve")
	}
}

func TestMatchChoiceNoMatch(t *testing.T) {
	options := []string{"approve", "adjust", "reject"}
	if got := matchChoice("something else entirely", options); got != "" {
		t.Errorf("matchChoice no match = %q, want empty", got)
	}
}

func TestBuildUserPromptWithOptions(t *testing.T) {
	prompt := buildUserPrompt("Review the plan", []string{"approve", "reject"}, "approve")
	if !strings.Contains(prompt, "Review the plan") {
		t.Error("expected gate prompt in user prompt")
	}
	if !strings.Contains(prompt, `"approve"`) {
		t.Error("expected options listed")
	}
	if !strings.Contains(prompt, "default option") {
		t.Error("expected default marker")
	}
	if !strings.Contains(prompt, "JSON") {
		t.Error("expected JSON instruction")
	}
}

func TestBuildUserPromptFreeform(t *testing.T) {
	prompt := buildUserPrompt("What should we build?", nil, "")
	if !strings.Contains(prompt, "freeform") {
		t.Error("expected freeform instruction")
	}
}

func TestAllPersonasHavePrompts(t *testing.T) {
	for _, name := range ValidPersonas() {
		persona, _ := ParsePersona(name)
		if _, ok := personaPrompts[persona]; !ok {
			t.Errorf("persona %q has no system prompt", name)
		}
	}
}

func TestFallbackWithDefault(t *testing.T) {
	ai := &AutopilotInterviewer{}
	got := ai.fallback([]string{"a", "b"}, "b")
	if got != "b" {
		t.Errorf("fallback with default = %q, want %q", got, "b")
	}
}

func TestFallbackWithoutDefault(t *testing.T) {
	ai := &AutopilotInterviewer{}
	got := ai.fallback([]string{"a", "b"}, "")
	if got != "a" {
		t.Errorf("fallback without default = %q, want %q", got, "a")
	}
}

func TestFallbackEmpty(t *testing.T) {
	ai := &AutopilotInterviewer{}
	got := ai.fallback(nil, "")
	if got != "" {
		t.Errorf("fallback empty = %q, want empty", got)
	}
}
