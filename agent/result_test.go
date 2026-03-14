// ABOUTME: Tests for SessionResult formatting and statistics.
// ABOUTME: Validates the String() output matches the design doc format.
package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/llm"
)

func TestResultString(t *testing.T) {
	r := SessionResult{
		SessionID:     "a3f2",
		Duration:      2*time.Minute + 34*time.Second,
		Turns:         14,
		ToolCalls:     map[string]int{"read": 12, "edit": 3, "bash": 8},
		FilesModified: []string{"auth.go", "auth_test.go"},
		FilesCreated:  []string{"oauth_handler.go"},
		Usage: llm.Usage{
			InputTokens:  32100,
			OutputTokens: 13131,
			TotalTokens:  45231,
		},
	}

	s := r.String()

	if !strings.Contains(s, "a3f2") {
		t.Errorf("expected session ID in output: %s", s)
	}
	if !strings.Contains(s, "2m34s") {
		t.Errorf("expected duration in output: %s", s)
	}
	if !strings.Contains(s, "14") {
		t.Errorf("expected turn count in output: %s", s)
	}
	if !strings.Contains(s, "23") {
		t.Errorf("expected total tool calls in output: %s", s)
	}
}

func TestResultStringWithTimings(t *testing.T) {
	r := SessionResult{
		SessionID: "b4c1",
		Duration:  1*time.Minute + 30*time.Second,
		Turns:     5,
		ToolCalls: map[string]int{"read": 3, "edit": 1},
		Usage: llm.Usage{
			InputTokens:   50000,
			OutputTokens:  10000,
			TotalTokens:   60000,
			EstimatedCost: 1.25,
		},
		CompactionsApplied: 2,
		LongestTurn:        15 * time.Second,
		ToolTimings: map[string]time.Duration{
			"read": 5 * time.Second,
			"edit": 2 * time.Second,
		},
	}

	s := r.String()

	if !strings.Contains(s, "Compactions: 2") {
		t.Errorf("expected 'Compactions: 2' in output: %s", s)
	}
	if !strings.Contains(s, "Longest turn: 15s") {
		t.Errorf("expected 'Longest turn: 15s' in output: %s", s)
	}
	if !strings.Contains(s, "$1.25") {
		t.Errorf("expected '$1.25' in output: %s", s)
	}
}

func TestResultTotalToolCalls(t *testing.T) {
	r := SessionResult{
		ToolCalls: map[string]int{"read": 5, "write": 3},
	}
	if r.TotalToolCalls() != 8 {
		t.Errorf("expected 8 total tool calls, got %d", r.TotalToolCalls())
	}
}
