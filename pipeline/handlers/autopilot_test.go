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

func TestMatchChoiceLongestWins(t *testing.T) {
	// "abandon" should match "abandon", not "a" — longest match wins
	options := []string{"a", "abandon", "accept"}
	if got := matchChoice("abandon", options); got != "abandon" {
		t.Errorf("matchChoice longest = %q, want %q", got, "abandon")
	}
}

func TestMatchChoiceAmbiguousSubstring(t *testing.T) {
	// When one option is a prefix of another, longest-match should win
	// even when the LLM response contains only the longer option.
	options := []string{"retry", "retry with escalation"}
	if got := matchChoice("retry with escalation", options); got != "retry with escalation" {
		t.Errorf("matchChoice ambiguous = %q, want %q", got, "retry with escalation")
	}
	// Short partial should match the short option
	if got := matchChoice("retry", options); got != "retry" {
		t.Errorf("matchChoice short = %q, want %q", got, "retry")
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

func TestBuildInterviewPrompt(t *testing.T) {
	questions := []Question{
		{Index: 1, Text: "Auth model?", Options: []string{"API key", "OAuth"}},
		{Index: 2, Text: "Describe integrations"},
	}
	prompt := buildInterviewPrompt(questions)

	if !strings.Contains(prompt, "Auth model?") {
		t.Error("expected question text 'Auth model?' in prompt")
	}
	if !strings.Contains(prompt, "API key") {
		t.Error("expected option 'API key' in prompt")
	}
	if !strings.Contains(prompt, "OAuth") {
		t.Error("expected option 'OAuth' in prompt")
	}
	if !strings.Contains(prompt, "Describe integrations") {
		t.Error("expected question text 'Describe integrations' in prompt")
	}
	if !strings.Contains(prompt, "JSON") {
		t.Error("expected JSON format instruction in prompt")
	}
	if !strings.Contains(prompt, "answers") {
		t.Error("expected 'answers' key in JSON format instruction")
	}
}

func TestBuildInterviewPromptYesNo(t *testing.T) {
	questions := []Question{
		{Index: 1, Text: "Do you want retries?", IsYesNo: true},
	}
	prompt := buildInterviewPrompt(questions)
	if !strings.Contains(prompt, "yes, no") {
		t.Error("expected yes/no options for IsYesNo question")
	}
}

func TestParseInterviewResponseValidJSON(t *testing.T) {
	questions := []Question{
		{Index: 1, Text: "Auth model?", Options: []string{"API key", "OAuth"}},
		{Index: 2, Text: "Describe integrations"},
	}
	jsonStr := `{"answers": [{"id": "q1", "answer": "OAuth"}, {"id": "q2", "answer": "Salesforce nightly sync"}]}`
	result, err := parseInterviewResponse(jsonStr, questions)
	if err != nil {
		t.Fatalf("parseInterviewResponse error: %v", err)
	}
	if len(result.Questions) != 2 {
		t.Fatalf("expected 2 answers, got %d", len(result.Questions))
	}
	if result.Questions[0].Answer != "OAuth" {
		t.Errorf("q1 answer = %q, want %q", result.Questions[0].Answer, "OAuth")
	}
	if result.Questions[0].ID != "q1" {
		t.Errorf("q1 ID = %q, want %q", result.Questions[0].ID, "q1")
	}
	if result.Questions[0].Text != "Auth model?" {
		t.Errorf("q1 text = %q, want %q", result.Questions[0].Text, "Auth model?")
	}
	if result.Questions[1].Answer != "Salesforce nightly sync" {
		t.Errorf("q2 answer = %q, want %q", result.Questions[1].Answer, "Salesforce nightly sync")
	}
}

func TestParseInterviewResponseMarkdownFences(t *testing.T) {
	questions := []Question{
		{Index: 1, Text: "Auth model?", Options: []string{"API key", "OAuth"}},
		{Index: 2, Text: "Describe integrations"},
	}
	jsonStr := `{"answers": [{"id": "q1", "answer": "OAuth"}, {"id": "q2", "answer": "Salesforce"}]}`
	wrapped := "```json\n" + jsonStr + "\n```"
	result, err := parseInterviewResponse(wrapped, questions)
	if err != nil {
		t.Fatalf("parseInterviewResponse with markdown fences error: %v", err)
	}
	if len(result.Questions) != 2 {
		t.Fatalf("expected 2 answers, got %d", len(result.Questions))
	}
	if result.Questions[0].Answer != "OAuth" {
		t.Errorf("q1 answer = %q, want %q", result.Questions[0].Answer, "OAuth")
	}
}

func TestParseInterviewResponseWithElaboration(t *testing.T) {
	questions := []Question{
		{Index: 1, Text: "Auth model?", Options: []string{"API key", "OAuth"}},
	}
	jsonStr := `{"answers": [{"id": "q1", "answer": "OAuth", "elaboration": "Google SSO preferred"}]}`
	result, err := parseInterviewResponse(jsonStr, questions)
	if err != nil {
		t.Fatalf("parseInterviewResponse error: %v", err)
	}
	if result.Questions[0].Elaboration != "Google SSO preferred" {
		t.Errorf("elaboration = %q, want %q", result.Questions[0].Elaboration, "Google SSO preferred")
	}
}

func TestParseInterviewResponseNoJSON(t *testing.T) {
	questions := []Question{
		{Index: 1, Text: "Auth model?"},
	}
	_, err := parseInterviewResponse("No JSON here at all", questions)
	if err == nil {
		t.Fatal("expected error for non-JSON response")
	}
}

func TestParseInterviewResponseEmptyAnswers(t *testing.T) {
	questions := []Question{
		{Index: 1, Text: "Auth model?"},
	}
	_, err := parseInterviewResponse(`{"answers": []}`, questions)
	if err == nil {
		t.Fatal("expected error for empty answers array")
	}
}

func TestParseInterviewResponseUnknownIDs(t *testing.T) {
	questions := []Question{
		{Index: 1, Text: "Auth model?"},
	}
	// All answers have unknown IDs — should error since no matches
	_, err := parseInterviewResponse(`{"answers": [{"id": "x99", "answer": "something"}]}`, questions)
	if err == nil {
		t.Fatal("expected error when no answer IDs match questions")
	}
}
