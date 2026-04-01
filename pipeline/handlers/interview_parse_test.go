// ABOUTME: Tests for ParseQuestions — extracts structured questions from agent markdown output.
package handlers

import (
	"testing"
)

func TestParseQuestionsEmpty(t *testing.T) {
	questions := ParseQuestions("")
	if len(questions) != 0 {
		t.Fatalf("expected empty slice, got %d questions", len(questions))
	}
}

func TestParseQuestionsNoQuestions(t *testing.T) {
	md := `This is some introductory text.
It explains what the pipeline does.
There are no questions here.`
	questions := ParseQuestions(md)
	if len(questions) != 0 {
		t.Fatalf("expected empty slice, got %d questions", len(questions))
	}
}

func TestParseQuestionsNumberedWithOptions(t *testing.T) {
	md := `1. Who are the API consumers? (internal services, third-party devs, mobile app)`
	questions := ParseQuestions(md)
	if len(questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(questions))
	}
	q := questions[0]
	if q.Index != 1 {
		t.Errorf("Index = %d, want 1", q.Index)
	}
	if q.Text != "Who are the API consumers?" {
		t.Errorf("Text = %q, want %q", q.Text, "Who are the API consumers?")
	}
	if len(q.Options) != 3 {
		t.Errorf("Options len = %d, want 3; got %v", len(q.Options), q.Options)
	} else {
		if q.Options[0] != "internal services" {
			t.Errorf("Options[0] = %q, want %q", q.Options[0], "internal services")
		}
		if q.Options[1] != "third-party devs" {
			t.Errorf("Options[1] = %q, want %q", q.Options[1], "third-party devs")
		}
		if q.Options[2] != "mobile app" {
			t.Errorf("Options[2] = %q, want %q", q.Options[2], "mobile app")
		}
	}
}

func TestParseQuestionsNumberedWithoutOptions(t *testing.T) {
	md := `2. What operations go beyond CRUD?`
	questions := ParseQuestions(md)
	if len(questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(questions))
	}
	q := questions[0]
	if q.Index != 1 {
		t.Errorf("Index = %d, want 1", q.Index)
	}
	if q.Text != "What operations go beyond CRUD?" {
		t.Errorf("Text = %q, want %q", q.Text, "What operations go beyond CRUD?")
	}
	if len(q.Options) != 0 {
		t.Errorf("Options = %v, want empty", q.Options)
	}
}

func TestParseQuestionsImperativePrompt(t *testing.T) {
	md := `3. Describe any existing integrations.`
	questions := ParseQuestions(md)
	if len(questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(questions))
	}
	q := questions[0]
	if q.Text != "Describe any existing integrations." {
		t.Errorf("Text = %q, want %q", q.Text, "Describe any existing integrations.")
	}
}

func TestParseQuestionsBulletEndingInQuestionMark(t *testing.T) {
	md := `- What auth model?`
	questions := ParseQuestions(md)
	if len(questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(questions))
	}
	q := questions[0]
	if q.Text != "What auth model?" {
		t.Errorf("Text = %q, want %q", q.Text, "What auth model?")
	}
}

func TestParseQuestionsTrailingParentheticalWithDescriptions(t *testing.T) {
	md := `Scale? (low <1k/day, medium 1k-100k/day)`
	questions := ParseQuestions(md)
	if len(questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(questions))
	}
	q := questions[0]
	if q.Text != "Scale?" {
		t.Errorf("Text = %q, want %q", q.Text, "Scale?")
	}
	if len(q.Options) != 2 {
		t.Errorf("Options len = %d, want 2; got %v", len(q.Options), q.Options)
	} else {
		if q.Options[0] != "low <1k/day" {
			t.Errorf("Options[0] = %q, want %q", q.Options[0], "low <1k/day")
		}
		if q.Options[1] != "medium 1k-100k/day" {
			t.Errorf("Options[1] = %q, want %q", q.Options[1], "medium 1k-100k/day")
		}
	}
}

func TestParseQuestionsYesNoDetection(t *testing.T) {
	md := `Need real-time? (yes, no)`
	questions := ParseQuestions(md)
	if len(questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(questions))
	}
	q := questions[0]
	if !q.IsYesNo {
		t.Errorf("IsYesNo = false, want true")
	}
}

func TestParseQuestionsYesNoDetectionCaseInsensitive(t *testing.T) {
	md := `- Ready to proceed? (Yes, No)`
	questions := ParseQuestions(md)
	if len(questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(questions))
	}
	if !questions[0].IsYesNo {
		t.Errorf("IsYesNo = false, want true for Yes/No options")
	}
}

func TestParseQuestionsNotYesNo(t *testing.T) {
	md := `1. Choose a tier? (free, pro, enterprise)`
	questions := ParseQuestions(md)
	if len(questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(questions))
	}
	if questions[0].IsYesNo {
		t.Errorf("IsYesNo = true, want false for non-yes/no options")
	}
}

func TestParseQuestionsSkipFencedCodeBlocks(t *testing.T) {
	md := "Some intro text.\n\n```\n1. This is inside a code block?\n2. This too?\n```\n\n1. This is a real question?"
	questions := ParseQuestions(md)
	if len(questions) != 1 {
		t.Fatalf("expected 1 question, got %d: %v", len(questions), questions)
	}
	if questions[0].Text != "This is a real question?" {
		t.Errorf("Text = %q, want %q", questions[0].Text, "This is a real question?")
	}
}

func TestParseQuestionsRealWorldFiveQuestions(t *testing.T) {
	md := `1. Who are the API consumers? (internal services, third-party devs, mobile app)
2. What operations go beyond CRUD? (search, bulk, export, webhooks)
3. What are the auth requirements? (public, API key, OAuth, per-tenant)
4. Are there real-time needs? (websockets, SSE, polling)
5. Describe any existing systems this must integrate with.`

	questions := ParseQuestions(md)
	if len(questions) != 5 {
		t.Fatalf("expected 5 questions, got %d", len(questions))
	}

	// Check indices are 1-based and sequential
	for i, q := range questions {
		if q.Index != i+1 {
			t.Errorf("questions[%d].Index = %d, want %d", i, q.Index, i+1)
		}
	}

	// Q1: consumers with 3 options
	if questions[0].Text != "Who are the API consumers?" {
		t.Errorf("Q1 text = %q", questions[0].Text)
	}
	if len(questions[0].Options) != 3 {
		t.Errorf("Q1 options len = %d, want 3", len(questions[0].Options))
	}

	// Q2: operations with 4 options
	if questions[1].Text != "What operations go beyond CRUD?" {
		t.Errorf("Q2 text = %q", questions[1].Text)
	}
	if len(questions[1].Options) != 4 {
		t.Errorf("Q2 options len = %d, want 4", len(questions[1].Options))
	}

	// Q3: auth with 4 options
	if len(questions[2].Options) != 4 {
		t.Errorf("Q3 options len = %d, want 4", len(questions[2].Options))
	}

	// Q4: real-time — websockets, SSE, polling — NOT yes/no
	if questions[3].IsYesNo {
		t.Errorf("Q4 should not be IsYesNo")
	}
	if len(questions[3].Options) != 3 {
		t.Errorf("Q4 options len = %d, want 3", len(questions[3].Options))
	}

	// Q5: imperative, no options
	if questions[4].Text != "Describe any existing systems this must integrate with." {
		t.Errorf("Q5 text = %q", questions[4].Text)
	}
	if len(questions[4].Options) != 0 {
		t.Errorf("Q5 should have no options, got %v", questions[4].Options)
	}
}

func TestParseQuestionsIndexing(t *testing.T) {
	md := `Here are my questions:

1. First question?
2. Second question?
3. Third question?`

	questions := ParseQuestions(md)
	if len(questions) != 3 {
		t.Fatalf("expected 3 questions, got %d", len(questions))
	}
	for i, q := range questions {
		if q.Index != i+1 {
			t.Errorf("questions[%d].Index = %d, want %d", i, q.Index, i+1)
		}
	}
}

func TestParseQuestionsParenthesesStyle(t *testing.T) {
	md := `1) Who are you?
2) What do you want?`
	questions := ParseQuestions(md)
	if len(questions) != 2 {
		t.Fatalf("expected 2 questions, got %d", len(questions))
	}
	if questions[0].Text != "Who are you?" {
		t.Errorf("Q1 text = %q", questions[0].Text)
	}
}

func TestParseQuestionsMixedFormats(t *testing.T) {
	md := `Please answer the following:

1. What is the primary use case?
- Who are the end users?
Describe the existing infrastructure.`

	questions := ParseQuestions(md)
	// Should find at least 3 questions (numbered, bullet, imperative)
	if len(questions) < 3 {
		t.Fatalf("expected at least 3 questions, got %d: %v", len(questions), questions)
	}
}

func TestParseQuestionsImperativeVerbs(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Describe the system.", "Describe the system."},
		{"Explain your approach.", "Explain your approach."},
		{"List all requirements.", "List all requirements."},
		{"Specify the format.", "Specify the format."},
		{"Provide details.", "Provide details."},
		{"Choose a method.", "Choose a method."},
		{"Select the tier.", "Select the tier."},
		{"Confirm your understanding.", "Confirm your understanding."},
		{"Rate the priority.", "Rate the priority."},
		{"Rank the options.", "Rank the options."},
	}
	for _, tc := range cases {
		questions := ParseQuestions(tc.input)
		if len(questions) != 1 {
			t.Errorf("input %q: expected 1 question, got %d", tc.input, len(questions))
			continue
		}
		if questions[0].Text != tc.want {
			t.Errorf("input %q: Text = %q, want %q", tc.input, questions[0].Text, tc.want)
		}
	}
}

func TestParseQuestionsImperativeBulletPrefix(t *testing.T) {
	md := `- Describe the system.
* Explain the approach.`
	questions := ParseQuestions(md)
	if len(questions) != 2 {
		t.Fatalf("expected 2 questions, got %d: %v", len(questions), questions)
	}
	if questions[0].Text != "Describe the system." {
		t.Errorf("Q1 text = %q", questions[0].Text)
	}
	if questions[1].Text != "Explain the approach." {
		t.Errorf("Q2 text = %q", questions[1].Text)
	}
}

func TestParseQuestionsYesNoTextPattern(t *testing.T) {
	// Even without (yes, no) options, some question texts imply yes/no
	md := `- Do you need authentication?`
	questions := ParseQuestions(md)
	if len(questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(questions))
	}
	// This tests that bullet questions ending in ? are captured
	if questions[0].Text != "Do you need authentication?" {
		t.Errorf("Text = %q", questions[0].Text)
	}
}

func TestParseQuestionsMultipleCodeBlocks(t *testing.T) {
	md := "Before.\n\n```\n1. Inside first block?\n```\n\nBetween blocks.\n\n```yaml\n2. Inside second block?\n```\n\n3. After all blocks?"
	questions := ParseQuestions(md)
	if len(questions) != 1 {
		t.Fatalf("expected 1 question (only outside blocks), got %d: %v", len(questions), questions)
	}
	if questions[0].Text != "After all blocks?" {
		t.Errorf("Text = %q, want %q", questions[0].Text, "After all blocks?")
	}
}

func TestParseQuestionsOptionsStripping(t *testing.T) {
	// Options with extra whitespace should be trimmed
	md := `1. Pick one? ( alpha , beta , gamma )`
	questions := ParseQuestions(md)
	if len(questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(questions))
	}
	if len(questions[0].Options) != 3 {
		t.Errorf("Options len = %d, want 3; got %v", len(questions[0].Options), questions[0].Options)
	}
	expected := []string{"alpha", "beta", "gamma"}
	for i, opt := range questions[0].Options {
		if opt != expected[i] {
			t.Errorf("Option[%d] = %q, want %q", i, opt, expected[i])
		}
		// Check no leading or trailing spaces
		if len(opt) > 0 && (opt[0] == ' ' || opt[len(opt)-1] == ' ') {
			t.Errorf("Option[%d] has leading/trailing space: %q", i, opt)
		}
	}
}

// ── Structured JSON parsing tests ──────────────────────────────

func TestParseStructuredQuestionsValid(t *testing.T) {
	input := `{"questions": [
		{"text": "Auth model?", "context": "Found 3 auth patterns in codebase", "options": ["API key", "OAuth", "JWT"]},
		{"text": "Scale expectations?", "options": ["low", "high"]},
		{"text": "Describe integrations"}
	]}`
	questions, err := ParseStructuredQuestions(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(questions) != 3 {
		t.Fatalf("expected 3 questions, got %d", len(questions))
	}
	if questions[0].Text != "Auth model?" {
		t.Errorf("Q1 text = %q", questions[0].Text)
	}
	if questions[0].Context != "Found 3 auth patterns in codebase" {
		t.Errorf("Q1 context = %q", questions[0].Context)
	}
	if len(questions[0].Options) != 3 {
		t.Errorf("Q1 options = %v", questions[0].Options)
	}
	if questions[1].Context != "" {
		t.Errorf("Q2 context should be empty, got %q", questions[1].Context)
	}
	if len(questions[2].Options) != 0 {
		t.Errorf("Q3 should have no options, got %v", questions[2].Options)
	}
}

func TestParseStructuredQuestionsYesNo(t *testing.T) {
	input := `{"questions": [{"text": "Is this production?", "options": ["yes", "no"]}]}`
	questions, err := ParseStructuredQuestions(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !questions[0].IsYesNo {
		t.Error("expected IsYesNo=true for yes/no options")
	}
}

func TestParseStructuredQuestionsCodeFenced(t *testing.T) {
	input := "Here are my questions:\n```json\n" +
		`{"questions": [{"text": "Auth?", "options": ["API key", "OAuth"]}]}` +
		"\n```\n"
	questions, err := ParseStructuredQuestions(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(questions) != 1 {
		t.Fatalf("expected 1 question, got %d", len(questions))
	}
	if questions[0].Text != "Auth?" {
		t.Errorf("text = %q", questions[0].Text)
	}
}

func TestParseStructuredQuestionsWithPreamble(t *testing.T) {
	input := "Based on my analysis, here are the scoping questions:\n\n" +
		`{"questions": [{"text": "Primary concern?", "context": "Multiple hot spots found", "options": ["correctness", "security"]}]}` +
		"\n\nLet me know if you need more detail."
	questions, err := ParseStructuredQuestions(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(questions) != 1 || questions[0].Text != "Primary concern?" {
		t.Errorf("unexpected result: %+v", questions)
	}
}

func TestParseStructuredQuestionsEmptyInput(t *testing.T) {
	_, err := ParseStructuredQuestions("")
	if err == nil {
		t.Error("expected error for empty input")
	}
}

func TestParseStructuredQuestionsNoJSON(t *testing.T) {
	_, err := ParseStructuredQuestions("Just some plain text without any JSON")
	if err == nil {
		t.Error("expected error for non-JSON input")
	}
}

func TestParseStructuredQuestionsEmptyArray(t *testing.T) {
	_, err := ParseStructuredQuestions(`{"questions": []}`)
	if err == nil {
		t.Error("expected error for empty questions array")
	}
}

func TestParseStructuredQuestionsEmptyText(t *testing.T) {
	_, err := ParseStructuredQuestions(`{"questions": [{"text": "", "options": ["a"]}]}`)
	if err == nil {
		t.Error("expected error for empty question text")
	}
}

func TestParseStructuredQuestionsInvalidJSON(t *testing.T) {
	_, err := ParseStructuredQuestions(`{"questions": [{"text": broken`)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestParseStructuredQuestionsIndices(t *testing.T) {
	input := `{"questions": [{"text": "Q1"}, {"text": "Q2"}, {"text": "Q3"}]}`
	questions, err := ParseStructuredQuestions(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, q := range questions {
		if q.Index != i+1 {
			t.Errorf("Q%d index = %d, want %d", i+1, q.Index, i+1)
		}
	}
}

// ── filterOtherOption tests ────────────────────────────────

func TestFilterOtherOptionExact(t *testing.T) {
	got := filterOtherOption([]string{"approve", "other", "reject"})
	if len(got) != 2 || got[0] != "approve" || got[1] != "reject" {
		t.Errorf("expected [approve reject], got %v", got)
	}
}

func TestFilterOtherOptionVariants(t *testing.T) {
	for _, variant := range []string{"Other", "OTHER", "other — describe below", "other — describe", "other - specify"} {
		got := filterOtherOption([]string{"keep", variant})
		if len(got) != 1 || got[0] != "keep" {
			t.Errorf("expected %q to be filtered, got %v", variant, got)
		}
	}
}

func TestFilterOtherOptionPreservesNonOther(t *testing.T) {
	got := filterOtherOption([]string{"Another option", "mother", "no other choice"})
	if len(got) != 3 {
		t.Errorf("expected 3 options preserved, got %v", got)
	}
}

func TestFilterOtherOptionEmpty(t *testing.T) {
	got := filterOtherOption(nil)
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestFilterOtherOptionYesNoWithOther(t *testing.T) {
	// After filtering "other", ["yes", "no"] should detect as yes/no
	filtered := filterOtherOption([]string{"yes", "no", "other"})
	if !isYesNoQuestion(filtered, "test?") {
		t.Error("expected yes/no detection after filtering other")
	}
}
