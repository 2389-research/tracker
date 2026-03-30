// ABOUTME: Tests for InterviewContent — fullscreen multi-field interview form modal.
// ABOUTME: Covers interface compliance, field creation, answer collection, submit, cancel, and pre-fill.
package tui

import (
	"encoding/json"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/2389-research/tracker/pipeline/handlers"
)

// Compile-time interface assertions.
var _ ModalContent = (*InterviewContent)(nil)
var _ Cancellable = (*InterviewContent)(nil)
var _ FullscreenContent = (*InterviewContent)(nil)

func TestInterviewContentIsFullscreen(t *testing.T) {
	ic := NewInterviewContent(nil, nil, nil, 80, 24)
	if !ic.IsFullscreen() {
		t.Error("expected IsFullscreen() to return true")
	}
}

func TestInterviewContentFieldCreation(t *testing.T) {
	questions := []handlers.Question{
		{Index: 1, Text: "What auth model?", Options: []string{"OAuth", "SAML"}},
		{Index: 2, Text: "Do you need SSO?", IsYesNo: true},
		{Index: 3, Text: "Describe your requirements"},
	}
	ic := NewInterviewContent(questions, nil, nil, 80, 24)
	if len(ic.fields) != 3 {
		t.Fatalf("expected 3 fields, got %d", len(ic.fields))
	}
	// First field: select (has options)
	if len(ic.fields[0].question.Options) != 2 {
		t.Errorf("field 0: expected 2 options, got %d", len(ic.fields[0].question.Options))
	}
	// Second field: yes/no
	if !ic.fields[1].question.IsYesNo {
		t.Error("field 1: expected IsYesNo")
	}
	// Third field: text
	if ic.fields[2].question.IsYesNo || len(ic.fields[2].question.Options) > 0 {
		t.Error("field 2: expected plain text field")
	}
}

func TestInterviewContentCollectAnswers(t *testing.T) {
	questions := []handlers.Question{
		{Index: 1, Text: "Auth model?", Options: []string{"OAuth", "SAML"}},
		{Index: 2, Text: "Need SSO?", IsYesNo: true},
		{Index: 3, Text: "Describe requirements"},
	}
	ic := NewInterviewContent(questions, nil, nil, 80, 24)

	// Set field states manually.
	ic.fields[0].selectCursor = 1 // SAML
	yes := true
	ic.fields[1].confirmed = &yes
	ic.fields[2].textInput.SetValue("Must support LDAP")

	result := ic.collectAnswers()
	if len(result.Questions) != 3 {
		t.Fatalf("expected 3 answers, got %d", len(result.Questions))
	}
	if result.Questions[0].Answer != "SAML" {
		t.Errorf("q1: expected 'SAML', got %q", result.Questions[0].Answer)
	}
	if result.Questions[0].ID != "q1" {
		t.Errorf("q1: expected id 'q1', got %q", result.Questions[0].ID)
	}
	if result.Questions[1].Answer != "Yes" {
		t.Errorf("q2: expected 'Yes', got %q", result.Questions[1].Answer)
	}
	if result.Questions[2].Answer != "Must support LDAP" {
		t.Errorf("q3: expected 'Must support LDAP', got %q", result.Questions[2].Answer)
	}
}

func TestInterviewContentCollectAnswersOther(t *testing.T) {
	questions := []handlers.Question{
		{Index: 1, Text: "Auth model?", Options: []string{"OAuth", "SAML"}},
	}
	ic := NewInterviewContent(questions, nil, nil, 80, 24)

	// Select "Other" (index == len(options))
	ic.fields[0].selectCursor = 2
	ic.fields[0].isOther = true
	ic.fields[0].otherInput.SetValue("Custom JWT")

	result := ic.collectAnswers()
	if result.Questions[0].Answer != "Custom JWT" {
		t.Errorf("expected 'Custom JWT', got %q", result.Questions[0].Answer)
	}
}

func TestInterviewContentCollectAnswersElaboration(t *testing.T) {
	questions := []handlers.Question{
		{Index: 1, Text: "Auth model?", Options: []string{"OAuth", "SAML"}},
	}
	ic := NewInterviewContent(questions, nil, nil, 80, 24)

	ic.fields[0].selectCursor = 0 // OAuth
	ic.fields[0].elaboration.SetValue("Google SSO preferred")

	result := ic.collectAnswers()
	if result.Questions[0].Answer != "OAuth" {
		t.Errorf("expected 'OAuth', got %q", result.Questions[0].Answer)
	}
	if result.Questions[0].Elaboration != "Google SSO preferred" {
		t.Errorf("expected elaboration 'Google SSO preferred', got %q", result.Questions[0].Elaboration)
	}
}

func TestInterviewContentCollectAnswersConfirmNo(t *testing.T) {
	questions := []handlers.Question{
		{Index: 1, Text: "Need SSO?", IsYesNo: true},
	}
	ic := NewInterviewContent(questions, nil, nil, 80, 24)

	no := false
	ic.fields[0].confirmed = &no

	result := ic.collectAnswers()
	if result.Questions[0].Answer != "No" {
		t.Errorf("expected 'No', got %q", result.Questions[0].Answer)
	}
}

func TestInterviewContentCollectAnswersConfirmUnanswered(t *testing.T) {
	questions := []handlers.Question{
		{Index: 1, Text: "Need SSO?", IsYesNo: true},
	}
	ic := NewInterviewContent(questions, nil, nil, 80, 24)
	// confirmed is nil (unanswered)

	result := ic.collectAnswers()
	if result.Questions[0].Answer != "" {
		t.Errorf("expected empty answer for unanswered, got %q", result.Questions[0].Answer)
	}
}

func TestInterviewContentSubmit(t *testing.T) {
	questions := []handlers.Question{
		{Index: 1, Text: "Auth model?", Options: []string{"OAuth", "SAML"}},
	}
	ch := make(chan string, 1)
	ic := NewInterviewContent(questions, nil, ch, 80, 24)

	ic.fields[0].selectCursor = 0 // OAuth
	ic.Update(tea.KeyMsg{Type: tea.KeyCtrlS})

	select {
	case got := <-ch:
		var result handlers.InterviewResult
		if err := json.Unmarshal([]byte(got), &result); err != nil {
			t.Fatalf("failed to unmarshal result: %v", err)
		}
		if result.Canceled {
			t.Error("expected Canceled=false")
		}
		if len(result.Questions) != 1 {
			t.Fatalf("expected 1 answer, got %d", len(result.Questions))
		}
		if result.Questions[0].Answer != "OAuth" {
			t.Errorf("expected 'OAuth', got %q", result.Questions[0].Answer)
		}
	default:
		t.Error("expected value on reply channel")
	}
}

func TestInterviewContentCancel(t *testing.T) {
	questions := []handlers.Question{
		{Index: 1, Text: "Auth model?", Options: []string{"OAuth", "SAML"}},
	}
	ch := make(chan string, 1)
	ic := NewInterviewContent(questions, nil, ch, 80, 24)

	ic.fields[0].selectCursor = 0 // OAuth
	// Esc at top level cancels
	ic.Update(tea.KeyMsg{Type: tea.KeyEscape})

	select {
	case got := <-ch:
		var result handlers.InterviewResult
		if err := json.Unmarshal([]byte(got), &result); err != nil {
			t.Fatalf("failed to unmarshal result: %v", err)
		}
		if !result.Canceled {
			t.Error("expected Canceled=true")
		}
	default:
		t.Error("expected value on reply channel after cancel")
	}
}

func TestInterviewContentCancelMethod(t *testing.T) {
	questions := []handlers.Question{
		{Index: 1, Text: "Auth?", Options: []string{"OAuth"}},
	}
	ch := make(chan string, 1)
	ic := NewInterviewContent(questions, nil, ch, 80, 24)

	ic.Cancel()

	select {
	case got := <-ch:
		var result handlers.InterviewResult
		if err := json.Unmarshal([]byte(got), &result); err != nil {
			t.Fatalf("failed to unmarshal result: %v", err)
		}
		if !result.Canceled {
			t.Error("expected Canceled=true from Cancel()")
		}
	default:
		t.Error("expected value on reply channel after Cancel()")
	}
}

func TestInterviewContentPrefill(t *testing.T) {
	questions := []handlers.Question{
		{Index: 1, Text: "Auth model?", Options: []string{"OAuth", "SAML"}},
		{Index: 2, Text: "Need SSO?", IsYesNo: true},
		{Index: 3, Text: "Describe requirements"},
	}
	prev := &handlers.InterviewResult{
		Questions: []handlers.InterviewAnswer{
			{ID: "q1", Text: "Auth model?", Answer: "SAML", Elaboration: "with MFA"},
			{ID: "q2", Text: "Need SSO?", Answer: "Yes"},
			{ID: "q3", Text: "Describe requirements", Answer: "LDAP support"},
		},
	}
	ic := NewInterviewContent(questions, prev, nil, 80, 24)

	// Check select field pre-fill
	if ic.fields[0].selectCursor != 1 {
		t.Errorf("expected selectCursor=1 (SAML), got %d", ic.fields[0].selectCursor)
	}
	if ic.fields[0].elaboration.Value() != "with MFA" {
		t.Errorf("expected elaboration 'with MFA', got %q", ic.fields[0].elaboration.Value())
	}

	// Check yes/no pre-fill
	if ic.fields[1].confirmed == nil || !*ic.fields[1].confirmed {
		t.Error("expected confirmed=true for 'Yes' pre-fill")
	}

	// Check text pre-fill
	if ic.fields[2].textInput.Value() != "LDAP support" {
		t.Errorf("expected textInput 'LDAP support', got %q", ic.fields[2].textInput.Value())
	}
}

func TestInterviewContentPrefillOther(t *testing.T) {
	questions := []handlers.Question{
		{Index: 1, Text: "Auth model?", Options: []string{"OAuth", "SAML"}},
	}
	prev := &handlers.InterviewResult{
		Questions: []handlers.InterviewAnswer{
			{ID: "q1", Text: "Auth model?", Answer: "Custom JWT"},
		},
	}
	ic := NewInterviewContent(questions, prev, nil, 80, 24)

	// "Custom JWT" doesn't match any option, so it should be "Other"
	if !ic.fields[0].isOther {
		t.Error("expected isOther=true for non-matching pre-fill")
	}
	if ic.fields[0].otherInput.Value() != "Custom JWT" {
		t.Errorf("expected otherInput 'Custom JWT', got %q", ic.fields[0].otherInput.Value())
	}
}

func TestInterviewContentDoubleSubmitIgnored(t *testing.T) {
	questions := []handlers.Question{
		{Index: 1, Text: "Auth?", Options: []string{"OAuth"}},
	}
	ch := make(chan string, 2)
	ic := NewInterviewContent(questions, nil, ch, 80, 24)

	ic.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	ic.Update(tea.KeyMsg{Type: tea.KeyCtrlS})

	// Only one message should be sent
	<-ch
	select {
	case <-ch:
		t.Error("expected only one message on channel")
	default:
		// good
	}
}

func TestInterviewContentViewNotEmpty(t *testing.T) {
	questions := []handlers.Question{
		{Index: 1, Text: "Auth model?", Options: []string{"OAuth", "SAML"}},
		{Index: 2, Text: "Need SSO?", IsYesNo: true},
		{Index: 3, Text: "Describe requirements"},
	}
	ic := NewInterviewContent(questions, nil, nil, 80, 24)
	view := ic.View()
	if view == "" {
		t.Error("expected non-empty view")
	}
	// Should contain question text
	for _, q := range questions {
		if !contains(view, q.Text) {
			t.Errorf("expected view to contain %q", q.Text)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
