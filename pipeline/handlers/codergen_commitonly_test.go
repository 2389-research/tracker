// ABOUTME: Tests for commit_only scope-guard injection in the codergen handler (#349).
// ABOUTME: Verifies that commit_only: true prepends commitOnlyScopeGuard to the session's
// ABOUTME: SystemPrompt, and that commit_only: false / absent produces no injection.
package handlers

import (
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func TestCommitOnlyScopeGuardInjected(t *testing.T) {
	h := NewCodergenHandler(nil, t.TempDir())
	node := &pipeline.Node{
		ID: "FinalCommit", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{
			"prompt":      "ensure all changes are committed",
			"commit_only": "true",
		},
	}
	cfg := h.buildConfig(node)
	if cfg.SystemPrompt != commitOnlyScopeGuard {
		t.Errorf("commit_only: true should set SystemPrompt to commitOnlyScopeGuard\ngot:  %q\nwant: %q", cfg.SystemPrompt, commitOnlyScopeGuard)
	}
}

func TestCommitOnlyScopeGuardAbsent(t *testing.T) {
	h := NewCodergenHandler(nil, t.TempDir())
	node := &pipeline.Node{
		ID: "Implement", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{
			"prompt": "implement the feature",
		},
	}
	cfg := h.buildConfig(node)
	if strings.Contains(cfg.SystemPrompt, "SCOPE RESTRICTION") {
		t.Errorf("commit_only absent should not inject scope guard, got: %q", cfg.SystemPrompt)
	}
}

func TestCommitOnlyScopeGuardFalseNoInjection(t *testing.T) {
	h := NewCodergenHandler(nil, t.TempDir())
	node := &pipeline.Node{
		ID: "Implement", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{
			"prompt":      "implement the feature",
			"commit_only": "false",
		},
	}
	cfg := h.buildConfig(node)
	if strings.Contains(cfg.SystemPrompt, "SCOPE RESTRICTION") {
		t.Errorf("commit_only: false should not inject scope guard, got: %q", cfg.SystemPrompt)
	}
}

func TestCommitOnlyScopeGuardPrependedBeforeNodeSystemPrompt(t *testing.T) {
	h := NewCodergenHandler(nil, t.TempDir())
	nodeSystemPrompt := "You are a helpful assistant."
	node := &pipeline.Node{
		ID: "FinalCommit", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{
			"prompt":        "ensure all changes are committed",
			"commit_only":   "true",
			"system_prompt": nodeSystemPrompt,
		},
	}
	cfg := h.buildConfig(node)
	want := commitOnlyScopeGuard + "\n\n" + nodeSystemPrompt
	if cfg.SystemPrompt != want {
		t.Errorf("commit_only with node system_prompt: SystemPrompt mismatch\ngot:  %q\nwant: %q", cfg.SystemPrompt, want)
	}
}
