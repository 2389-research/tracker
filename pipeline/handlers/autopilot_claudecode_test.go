// ABOUTME: Tests for the claude-code autopilot interviewer.
// ABOUTME: Verifies interface compliance, fallback behavior, and decision parsing.
package handlers

import (
	"testing"
)

func TestClaudeCodeAutopilotImplementsLabeledFreeformInterviewer(t *testing.T) {
	// Verify interface compliance at compile time.
	var _ LabeledFreeformInterviewer = (*ClaudeCodeAutopilotInterviewer)(nil)
}

func TestClaudeCodeAutopilotFallback(t *testing.T) {
	ai := &ClaudeCodeAutopilotInterviewer{persona: PersonaLax}

	tests := []struct {
		name          string
		options       []string
		defaultOption string
		want          string
	}{
		{"default wins", []string{"a", "b"}, "b", "b"},
		{"first option fallback", []string{"a", "b"}, "", "a"},
		{"empty options", nil, "", ""},
		{"empty options with default", nil, "x", "x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ai.fallback(tt.options, tt.defaultOption)
			if got != tt.want {
				t.Errorf("fallback(%v, %q) = %q, want %q", tt.options, tt.defaultOption, got, tt.want)
			}
		})
	}
}

func TestClaudeCodeAutopilotAllPersonas(t *testing.T) {
	for _, p := range []Persona{PersonaLax, PersonaMid, PersonaHard, PersonaMentor} {
		t.Run(string(p), func(t *testing.T) {
			ai := &ClaudeCodeAutopilotInterviewer{
				persona:    p,
				claudePath: "/nonexistent/claude",
			}
			if ai.persona != p {
				t.Errorf("expected persona %s, got %s", p, ai.persona)
			}
		})
	}
}

func TestClaudeCodeAutopilotAskFailsWithBadPath(t *testing.T) {
	ai := &ClaudeCodeAutopilotInterviewer{
		persona:    PersonaLax,
		claudePath: "/nonexistent/claude",
	}
	_, err := ai.Ask("pick one", []string{"a", "b"}, "a")
	if err == nil {
		t.Error("expected error with nonexistent claude path")
	}
}

func TestClaudeCodeAutopilotAskFreeformFailsGracefully(t *testing.T) {
	ai := &ClaudeCodeAutopilotInterviewer{
		persona:    PersonaLax,
		claudePath: "/nonexistent/claude",
	}
	// AskFreeform falls back to "auto-approved" on failure.
	result, err := ai.AskFreeform("describe your plan")
	if err != nil {
		t.Fatalf("AskFreeform should not error (falls back), got: %v", err)
	}
	if result != "auto-approved" {
		t.Errorf("expected 'auto-approved' fallback, got %q", result)
	}
}

func TestClaudeCodeAutopilotAskFreeformWithLabelsFailsWithBadPath(t *testing.T) {
	ai := &ClaudeCodeAutopilotInterviewer{
		persona:    PersonaMid,
		claudePath: "/nonexistent/claude",
	}
	_, err := ai.AskFreeformWithLabels("review", []string{"approve", "reject"}, "approve")
	if err == nil {
		t.Error("expected error with nonexistent claude path")
	}
}
