// ABOUTME: Tests for the shared ResolvePrompt function extracted from CodergenHandler.
// ABOUTME: Verifies variable expansion, context injection, fidelity compaction, and error cases.
package handlers

import (
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func TestResolvePromptExpandsGraphVariables(t *testing.T) {
	node := &pipeline.Node{
		ID:    "gen",
		Attrs: map[string]string{"prompt": "Build ${graph.target_name}"},
	}
	pctx := pipeline.NewPipelineContext()
	graphAttrs := map[string]string{"target_name": "my-app"}

	prompt, err := ResolvePrompt(node, pctx, graphAttrs, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "my-app") {
		t.Errorf("expected ${graph.target_name} expanded to 'my-app', got %q", prompt)
	}
	if strings.Contains(prompt, "${graph.target_name}") {
		t.Errorf("expected ${graph.target_name} to be replaced, got %q", prompt)
	}
}

func TestResolvePromptInjectsPipelineContext(t *testing.T) {
	node := &pipeline.Node{
		ID:    "gen",
		Attrs: map[string]string{"prompt": "do work", "fidelity": "full"},
	}
	pctx := pipeline.NewPipelineContext()
	pctx.Set(pipeline.ContextKeyLastResponse, "prior output from previous node")

	prompt, err := ResolvePrompt(node, pctx, map[string]string{}, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "prior output from previous node") {
		t.Errorf("expected last_response injected into prompt, got %q", prompt)
	}
}

func TestResolvePromptMissingPromptAttr(t *testing.T) {
	node := &pipeline.Node{
		ID:    "gen",
		Attrs: map[string]string{},
	}
	pctx := pipeline.NewPipelineContext()

	_, err := ResolvePrompt(node, pctx, nil, t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing prompt attribute")
	}
	if !strings.Contains(err.Error(), "missing required attribute 'prompt'") {
		t.Errorf("expected descriptive error, got: %v", err)
	}
}

func TestResolvePromptExpandsGoalVariable(t *testing.T) {
	node := &pipeline.Node{
		ID:    "gen",
		Attrs: map[string]string{"prompt": "Plan for $goal"},
	}
	pctx := pipeline.NewPipelineContext()
	pctx.Set(pipeline.ContextKeyGoal, "ship a widget")

	prompt, err := ResolvePrompt(node, pctx, nil, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "ship a widget") {
		t.Errorf("expected $goal expanded, got %q", prompt)
	}
}

func TestResolvePromptCompactFidelity(t *testing.T) {
	node := &pipeline.Node{
		ID:    "gen",
		Attrs: map[string]string{"prompt": "do work", "fidelity": "compact"},
	}
	pctx := pipeline.NewPipelineContext()
	pctx.Set(pipeline.ContextKeyGoal, "build a widget")
	pctx.Set(pipeline.ContextKeyLastResponse, "long response that should be excluded")

	prompt, err := ResolvePrompt(node, pctx, map[string]string{}, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "Context Summary") {
		t.Errorf("expected context summary for compact fidelity, got %q", prompt)
	}
	if !strings.Contains(prompt, "build a widget") {
		t.Errorf("expected goal in compact context, got %q", prompt)
	}
	if strings.Contains(prompt, "long response that should be excluded") {
		t.Errorf("compact fidelity should NOT include last_response")
	}
}
