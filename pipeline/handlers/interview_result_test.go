// ABOUTME: Tests for InterviewResult types, serialization, and markdown summary.
// ABOUTME: Verifies round-trip JSON serialization and human-readable output format.
package handlers

import (
	"strings"
	"testing"
)

func TestSerializeInterviewResult(t *testing.T) {
	r := InterviewResult{
		Questions: []InterviewAnswer{
			{
				ID:          "q1",
				Text:        "Auth model?",
				Options:     []string{"OAuth", "API key"},
				Answer:      "OAuth",
				Elaboration: "Google SSO preferred",
			},
			{
				ID:     "q2",
				Text:   "Describe integrations",
				Answer: "Salesforce nightly sync",
			},
		},
		Incomplete: false,
		Canceled:   false,
	}

	s := SerializeInterviewResult(r)
	if s == "" {
		t.Fatal("expected non-empty serialized string")
	}

	got, err := DeserializeInterviewResult(s)
	if err != nil {
		t.Fatalf("round-trip deserialize: %v", err)
	}

	if len(got.Questions) != 2 {
		t.Fatalf("expected 2 questions, got %d", len(got.Questions))
	}

	q1 := got.Questions[0]
	if q1.ID != "q1" {
		t.Errorf("q1.ID: got %q want %q", q1.ID, "q1")
	}
	if q1.Text != "Auth model?" {
		t.Errorf("q1.Text: got %q want %q", q1.Text, "Auth model?")
	}
	if len(q1.Options) != 2 || q1.Options[0] != "OAuth" || q1.Options[1] != "API key" {
		t.Errorf("q1.Options: got %v", q1.Options)
	}
	if q1.Answer != "OAuth" {
		t.Errorf("q1.Answer: got %q want %q", q1.Answer, "OAuth")
	}
	if q1.Elaboration != "Google SSO preferred" {
		t.Errorf("q1.Elaboration: got %q want %q", q1.Elaboration, "Google SSO preferred")
	}

	q2 := got.Questions[1]
	if q2.ID != "q2" {
		t.Errorf("q2.ID: got %q want %q", q2.ID, "q2")
	}
	if q2.Answer != "Salesforce nightly sync" {
		t.Errorf("q2.Answer: got %q want %q", q2.Answer, "Salesforce nightly sync")
	}
	if q2.Elaboration != "" {
		t.Errorf("q2.Elaboration: expected empty, got %q", q2.Elaboration)
	}

	if got.Incomplete != false {
		t.Error("Incomplete should be false")
	}
	if got.Canceled != false {
		t.Error("Canceled should be false")
	}
}

func TestSerializeInterviewResult_Flags(t *testing.T) {
	r := InterviewResult{
		Questions:  []InterviewAnswer{},
		Incomplete: true,
		Canceled:   true,
	}

	s := SerializeInterviewResult(r)
	got, err := DeserializeInterviewResult(s)
	if err != nil {
		t.Fatalf("round-trip deserialize: %v", err)
	}
	if !got.Incomplete {
		t.Error("Incomplete should be true")
	}
	if !got.Canceled {
		t.Error("Canceled should be true")
	}
}

func TestDeserializeInterviewResult_Invalid(t *testing.T) {
	_, err := DeserializeInterviewResult("not valid json {{{")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestDeserializeInterviewResult_Empty(t *testing.T) {
	_, err := DeserializeInterviewResult("")
	if err == nil {
		t.Fatal("expected error for empty string, got nil")
	}
}

func TestBuildMarkdownSummary(t *testing.T) {
	r := InterviewResult{
		Questions: []InterviewAnswer{
			{
				ID:          "q1",
				Text:        "Auth model?",
				Answer:      "OAuth",
				Elaboration: "Google SSO",
			},
			{
				ID:     "q2",
				Text:   "Describe integrations",
				Answer: "Salesforce nightly sync",
			},
		},
	}

	out := BuildMarkdownSummary(r)

	// Must have section header
	if !strings.Contains(out, "## Interview Answers") {
		t.Error("missing '## Interview Answers' header")
	}

	// Q1 with elaboration: "OAuth — Google SSO"
	if !strings.Contains(out, "**Q1: Auth model?**") {
		t.Errorf("missing Q1 header, got:\n%s", out)
	}
	if !strings.Contains(out, "OAuth — Google SSO") {
		t.Errorf("missing 'OAuth — Google SSO' for Q1, got:\n%s", out)
	}

	// Q2 without elaboration: just the answer
	if !strings.Contains(out, "**Q2: Describe integrations**") {
		t.Errorf("missing Q2 header, got:\n%s", out)
	}
	if !strings.Contains(out, "Salesforce nightly sync") {
		t.Errorf("missing Q2 answer, got:\n%s", out)
	}

	// Footer count
	if !strings.Contains(out, "2 questions answered") {
		t.Errorf("missing footer count, got:\n%s", out)
	}
}

func TestBuildMarkdownSummary_Skipped(t *testing.T) {
	r := InterviewResult{
		Questions: []InterviewAnswer{
			{
				ID:     "q1",
				Text:   "Auth model?",
				Answer: "",
			},
		},
	}

	out := BuildMarkdownSummary(r)

	if !strings.Contains(out, "(skipped)") {
		t.Errorf("expected '(skipped)' for empty answer, got:\n%s", out)
	}
}

func TestBuildMarkdownSummary_Canceled(t *testing.T) {
	r := InterviewResult{
		Questions: []InterviewAnswer{
			{
				ID:     "q1",
				Text:   "Auth model?",
				Answer: "OAuth",
			},
		},
		Canceled: true,
	}

	out := BuildMarkdownSummary(r)

	if !strings.Contains(out, "*Interview was canceled.*") {
		t.Errorf("expected canceled notice, got:\n%s", out)
	}
}

func TestBuildMarkdownSummary_Empty(t *testing.T) {
	r := InterviewResult{
		Questions: []InterviewAnswer{},
	}

	out := BuildMarkdownSummary(r)

	// Should still have the header and a reasonable footer
	if !strings.Contains(out, "## Interview Answers") {
		t.Errorf("missing header for empty result, got:\n%s", out)
	}
	// Zero count footer
	if !strings.Contains(out, "0 questions answered") {
		t.Errorf("expected '0 questions answered', got:\n%s", out)
	}
}

func TestBuildMarkdownSummary_Separator(t *testing.T) {
	r := InterviewResult{
		Questions: []InterviewAnswer{
			{ID: "q1", Text: "First?", Answer: "Yes"},
			{ID: "q2", Text: "Second?", Answer: "No"},
		},
	}

	out := BuildMarkdownSummary(r)

	// The separator "---" should appear before the footer
	if !strings.Contains(out, "---") {
		t.Errorf("missing '---' separator, got:\n%s", out)
	}
}
