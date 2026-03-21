// ABOUTME: Tests for Dippin semantic lint rules (DIP101-DIP112).
package pipeline

import (
	"strings"
	"testing"
)

// DIP110: Empty prompt warning

func TestLintDIP110_EmptyPrompt(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{
		ID:      "Agent1",
		Handler: "codergen",
		Attrs:   map[string]string{}, // No prompt
	})

	warnings := LintDippinRules(g)
	if !containsWarning(warnings, "DIP110", "Agent1") {
		t.Errorf("expected DIP110 warning, got: %v", warnings)
	}
}

func TestLintDIP110_NoWarningWithPrompt(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{
		ID:      "Agent1",
		Handler: "codergen",
		Attrs:   map[string]string{"prompt": "do something"},
	})

	warnings := LintDippinRules(g)
	if containsWarning(warnings, "DIP110", "") {
		t.Errorf("unexpected DIP110 warning: %v", warnings)
	}
}

// DIP111: Tool without timeout warning

func TestLintDIP111_ToolWithoutTimeout(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{
		ID:      "RunTests",
		Handler: "tool",
		Attrs:   map[string]string{"tool_command": "pytest"}, // No timeout
	})

	warnings := LintDippinRules(g)
	if !containsWarning(warnings, "DIP111", "RunTests") {
		t.Errorf("expected DIP111 warning, got: %v", warnings)
	}
}

func TestLintDIP111_NoWarningWithTimeout(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{
		ID:      "RunTests",
		Handler: "tool",
		Attrs: map[string]string{
			"tool_command": "pytest",
			"timeout":      "60s",
		},
	})

	warnings := LintDippinRules(g)
	if containsWarning(warnings, "DIP111", "") {
		t.Errorf("unexpected DIP111 warning: %v", warnings)
	}
}

// DIP102: No default edge warning

func TestLintDIP102_NoDefaultEdge(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "Check", Handler: "codergen", Attrs: map[string]string{"prompt": "check"}})
	g.AddNode(&Node{ID: "Pass", Handler: "codergen", Attrs: map[string]string{"prompt": "pass"}})
	g.AddNode(&Node{ID: "Fail", Handler: "codergen", Attrs: map[string]string{"prompt": "fail"}})

	g.AddEdge(&Edge{From: "Check", To: "Pass", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "Check", To: "Fail", Condition: "outcome=fail"})

	warnings := LintDippinRules(g)
	if !containsWarning(warnings, "DIP102", "Check") {
		t.Errorf("expected DIP102 warning, got: %v", warnings)
	}
}

func TestLintDIP102_NoWarningWithDefault(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "Check", Handler: "codergen", Attrs: map[string]string{"prompt": "check"}})
	g.AddNode(&Node{ID: "Fail", Handler: "codergen", Attrs: map[string]string{"prompt": "fail"}})

	g.AddEdge(&Edge{From: "Check", To: "Check", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "Check", To: "Fail"}) // Unconditional fallback

	warnings := LintDippinRules(g)
	if containsWarning(warnings, "DIP102", "") {
		t.Errorf("unexpected DIP102 warning: %v", warnings)
	}
}

// DIP104: Unbounded retry warning

func TestLintDIP104_UnboundedRetry(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{
		ID:      "Task",
		Handler: "codergen",
		Attrs:   map[string]string{
			"prompt":       "do work",
			"retry_target": "Task",
		},
	})

	warnings := LintDippinRules(g)
	if !containsWarning(warnings, "DIP104", "Task") {
		t.Errorf("expected DIP104 warning, got: %v", warnings)
	}
}

func TestLintDIP104_NoWarningWithMaxRetries(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{
		ID:      "Task",
		Handler: "codergen",
		Attrs: map[string]string{
			"prompt":       "do work",
			"retry_target": "Task",
			"max_retries":  "3",
		},
	})

	warnings := LintDippinRules(g)
	if containsWarning(warnings, "DIP104", "") {
		t.Errorf("unexpected DIP104 warning: %v", warnings)
	}
}

// Helper function to check if warnings contain a specific DIP code
func containsWarning(warnings []string, dipCode string, nodeID string) bool {
	for _, w := range warnings {
		if strings.Contains(w, dipCode) {
			if nodeID == "" || strings.Contains(w, nodeID) {
				return true
			}
		}
	}
	return false
}
