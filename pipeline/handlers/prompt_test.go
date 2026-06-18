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

func TestResolvePromptExpandsParamsVariables(t *testing.T) {
	node := &pipeline.Node{
		ID:    "gen",
		Attrs: map[string]string{"prompt": "Build ${params.target_name}"},
	}
	pctx := pipeline.NewPipelineContext()
	graphAttrs := map[string]string{"params.target_name": "my-app"}

	prompt, err := ResolvePrompt(node, pctx, graphAttrs, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "my-app") {
		t.Errorf("expected ${params.target_name} expanded to 'my-app', got %q", prompt)
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

func TestResolvePromptSummaryMediumPinsReads(t *testing.T) {
	node := &pipeline.Node{
		ID: "gen",
		Attrs: map[string]string{
			"prompt":   "do work",
			"fidelity": "summary:medium",
			"reads":    "custom_key",
		},
	}
	pctx := pipeline.NewPipelineContext()
	pctx.Set(pipeline.ContextKeyOutcome, "success")
	pctx.Set("custom_key", "keep-me")

	prompt, err := ResolvePrompt(node, pctx, map[string]string{}, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "## custom_key\nkeep-me") {
		t.Fatalf("expected custom_key pinned by reads in prompt summary, got %q", prompt)
	}
}

// --- #352 item 1: injection_cap node attr overrides the default cap ---

func TestResolvePromptInjectionCapNodeAttr(t *testing.T) {
	node := &pipeline.Node{
		ID: "gen",
		Attrs: map[string]string{
			"prompt":        "do work",
			"injection_cap": "64",
		},
	}
	pctx := pipeline.NewPipelineContext()
	pctx.Set(pipeline.ContextKeyLastResponse, strings.Repeat("x", 1000))

	prompt, err := ResolvePrompt(node, pctx, map[string]string{}, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "elided") {
		t.Errorf("expected injection_cap=64 to truncate last_response, got %q", prompt)
	}
	if strings.Contains(prompt, strings.Repeat("x", 200)) {
		t.Errorf("expected at most ~64 bytes of last_response in prompt")
	}
}

func TestResolvePromptInjectionCapDefaultApplies(t *testing.T) {
	node := &pipeline.Node{
		ID:    "gen",
		Attrs: map[string]string{"prompt": "do work"},
	}
	pctx := pipeline.NewPipelineContext()
	pctx.Set(pipeline.ContextKeyLastResponse, strings.Repeat("y", pipeline.DefaultInjectedResponseCap*3))

	prompt, err := ResolvePrompt(node, pctx, map[string]string{}, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(prompt, "elided") {
		t.Errorf("expected default cap to truncate oversized last_response")
	}
}

func TestResolvePromptInjectionCapMalformed(t *testing.T) {
	node := &pipeline.Node{
		ID: "gen",
		Attrs: map[string]string{
			"prompt":        "do work",
			"injection_cap": "lots",
		},
	}
	pctx := pipeline.NewPipelineContext()

	_, err := ResolvePrompt(node, pctx, map[string]string{}, t.TempDir())
	if err == nil {
		t.Fatal("expected error for malformed injection_cap")
	}
	if !strings.Contains(err.Error(), "injection_cap") {
		t.Errorf("expected error to name injection_cap, got: %v", err)
	}
}

func TestResolvePrompt_LastResponseTruncate(t *testing.T) {
	node := &pipeline.Node{
		ID: "TestNode",
		Attrs: map[string]string{
			"prompt":                  "Do the task.",
			"last_response_truncate": "10",
		},
	}
	pctx := pipeline.NewPipelineContext()
	const longResp = "hello world this is a long response from the previous node"
	pctx.Set(pipeline.ContextKeyLastResponse, longResp)

	got, err := ResolvePrompt(node, pctx, nil, "")
	if err != nil {
		t.Fatalf("ResolvePrompt: %v", err)
	}
	if strings.Contains(got, "hello world this") {
		t.Errorf("last_response not truncated: full string appears in output")
	}
	// Original pctx value must be restored.
	restored, _ := pctx.Get(pipeline.ContextKeyLastResponse)
	if restored != longResp {
		t.Errorf("pctx last_response not restored: got %q", restored)
	}
}

func TestResolvePrompt_LastResponseTruncate_Zero_NoChange(t *testing.T) {
	node := &pipeline.Node{
		ID:    "TestNode",
		Attrs: map[string]string{"prompt": "Do the task."},
	}
	pctx := pipeline.NewPipelineContext()
	const resp = "short response"
	pctx.Set(pipeline.ContextKeyLastResponse, resp)

	got, err := ResolvePrompt(node, pctx, nil, "")
	if err != nil {
		t.Fatalf("ResolvePrompt: %v", err)
	}
	if !strings.Contains(got, resp) {
		t.Errorf("expected %q in prompt when truncate is 0", resp)
	}
}

func TestResolvePrompt_LastResponseTruncate_Malformed(t *testing.T) {
	node := &pipeline.Node{
		ID:    "TestNode",
		Attrs: map[string]string{"prompt": "Do the task.", "last_response_truncate": "abc"},
	}
	_, err := ResolvePrompt(node, pipeline.NewPipelineContext(), nil, "")
	if err == nil {
		t.Fatal("expected error for malformed last_response_truncate")
	}
}
