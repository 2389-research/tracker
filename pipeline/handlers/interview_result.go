// ABOUTME: Types and serialization for interview mode answer collection.
// ABOUTME: Used by the human handler to persist answers to pipeline context as JSON strings.
package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
)

// InterviewAnswer represents a user's response to one question.
type InterviewAnswer struct {
	ID          string   `json:"id"`                    // "q1", "q2", ...
	Text        string   `json:"text"`                  // Original question text
	Options     []string `json:"options,omitempty"`     // Predefined options, if any
	Answer      string   `json:"answer"`                // Selected or typed answer
	Elaboration string   `json:"elaboration,omitempty"` // Optional free-text elaboration
}

// InterviewResult is the complete response serialized to context.
type InterviewResult struct {
	Questions  []InterviewAnswer `json:"questions"`
	Incomplete bool              `json:"incomplete"`
	Canceled   bool              `json:"canceled"`
}

// SerializeInterviewResult marshals an InterviewResult to a JSON string.
// Panics on marshal failure (which cannot happen for this struct type,
// but we enforce rather than silently returning empty).
func SerializeInterviewResult(r InterviewResult) string {
	b, err := json.Marshal(r)
	if err != nil {
		panic(fmt.Sprintf("interview result marshal failed: %v", err))
	}
	return string(b)
}

// DeserializeInterviewResult unmarshals an InterviewResult from a JSON string.
// Returns an error for invalid or empty input.
func DeserializeInterviewResult(s string) (InterviewResult, error) {
	if s == "" {
		return InterviewResult{}, fmt.Errorf("cannot deserialize empty string")
	}
	var r InterviewResult
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return InterviewResult{}, err
	}
	return r, nil
}

// BuildMarkdownSummary produces a human-readable markdown summary of an
// InterviewResult suitable for downstream agents.
//
// Format:
//
//	## Interview Answers
//
//	**Q1: Auth model?**
//	OAuth — Google SSO
//
//	**Q2: Describe integrations**
//	Salesforce nightly sync...
//
//	---
//	*2 of 2 questions answered*
func BuildMarkdownSummary(r InterviewResult) string {
	var b strings.Builder

	b.WriteString("## Interview Answers\n")

	for i, q := range r.Questions {
		b.WriteString(fmt.Sprintf("\n**Q%d: %s**\n", i+1, q.Text))

		if q.Answer == "" {
			b.WriteString("(skipped)\n")
		} else if q.Elaboration != "" {
			b.WriteString(fmt.Sprintf("%s — %s\n", q.Answer, q.Elaboration))
		} else {
			b.WriteString(q.Answer + "\n")
		}
	}

	b.WriteString("\n---\n")
	answered := 0
	for _, q := range r.Questions {
		if q.Answer != "" {
			answered++
		}
	}
	b.WriteString(fmt.Sprintf("*%d of %d questions answered*", answered, len(r.Questions)))

	if r.Canceled {
		b.WriteString("\n*Interview was canceled.*")
	}

	return b.String()
}
