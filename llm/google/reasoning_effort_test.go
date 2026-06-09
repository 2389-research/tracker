// ABOUTME: Tests that the unified reasoning_effort maps to Gemini thinkingConfig.thinkingLevel.
package google

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// genConfig translates req and returns the decoded generationConfig object from
// the resulting Gemini request body (nil if the request produced none), so the
// assertions below can stay terse.
func genConfig(t *testing.T, req *llm.Request) map[string]any {
	t.Helper()
	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	gc, _ := m["generationConfig"].(map[string]any)
	return gc
}

// TestTranslateRequestThinkingLevel verifies that each reasoning_effort level
// maps to generationConfig.thinkingConfig.thinkingLevel for Gemini 3 models.
// "minimal" is included because it is a valid Gemini-3 level (≈ thinking off).
func TestTranslateRequestThinkingLevel(t *testing.T) {
	for _, effort := range []string{"low", "medium", "high", "minimal"} {
		req := &llm.Request{
			Model:           "gemini-3-flash-preview",
			Messages:        []llm.Message{llm.UserMessage("Decompose this spec")},
			ReasoningEffort: effort,
		}
		gc := genConfig(t, req)
		if gc == nil {
			t.Fatalf("effort=%s: expected generationConfig", effort)
		}
		tc, ok := gc["thinkingConfig"].(map[string]any)
		if !ok {
			t.Fatalf("effort=%s: expected thinkingConfig, got %v", effort, gc["thinkingConfig"])
		}
		if tc["thinkingLevel"] != effort {
			t.Errorf("effort=%s: thinkingLevel = %v, want %s", effort, tc["thinkingLevel"], effort)
		}
	}
}

// TestTranslateRequestGemini25OmitsThinkingLevel guards a regression that would
// break shipped pipelines: thinkingLevel is Gemini 3+ only, and Gemini 2.5
// models reject it (400). build_product / build_product_with_superspec run their
// adversarial review on gemini-2.5-pro WITH reasoning_effort: high. So for a
// Gemini 2.5 model, reasoning_effort must NOT emit thinkingConfig — and when it
// is the only would-be field, no generationConfig at all (prior behavior).
func TestTranslateRequestGemini25OmitsThinkingLevel(t *testing.T) {
	req := &llm.Request{
		Model:           "gemini-2.5-pro",
		Messages:        []llm.Message{llm.UserMessage("Review this")},
		ReasoningEffort: "high",
	}
	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	gc, ok := m["generationConfig"].(map[string]any)
	if ok {
		if _, has := gc["thinkingConfig"]; has {
			t.Errorf("gemini-2.5-pro must not get thinkingConfig (unsupported → 400), got %v", gc["thinkingConfig"])
		}
		// reasoning_effort was the only non-default field → no generationConfig at all.
		t.Errorf("gemini-2.5-pro + reasoning-only must produce no generationConfig, got %v", gc)
	}
}

// TestTranslateRequestGemini25KeepsOtherGenConfig verifies the gating is scoped:
// a Gemini 2.5 request with another gen-config field (temperature) still builds
// generationConfig — just without thinkingConfig.
func TestTranslateRequestGemini25KeepsOtherGenConfig(t *testing.T) {
	temp := 0.5
	req := &llm.Request{
		Model:           "gemini-2.5-pro",
		Messages:        []llm.Message{llm.UserMessage("Review this")},
		ReasoningEffort: "high",
		Temperature:     &temp,
	}
	gc := genConfig(t, req)
	if gc == nil {
		t.Fatal("expected generationConfig (temperature set)")
	}
	if _, has := gc["thinkingConfig"]; has {
		t.Errorf("gemini-2.5-pro must not get thinkingConfig, got %v", gc["thinkingConfig"])
	}
	if gc["temperature"] == nil {
		t.Error("temperature should still be present")
	}
}

// TestTranslateRequestReasoningOnlyStillBuildsGenConfig guards the early-return
// in buildGenerationConfig: a request whose ONLY non-default field is
// reasoning_effort must still produce a generationConfig carrying thinkingLevel,
// rather than being dropped as "no config needed".
func TestTranslateRequestReasoningOnlyStillBuildsGenConfig(t *testing.T) {
	req := &llm.Request{
		Model:           "gemini-3-flash-preview",
		Messages:        []llm.Message{llm.UserMessage("Hello")},
		ReasoningEffort: "low",
	}
	gc := genConfig(t, req)
	if gc == nil {
		t.Fatal("expected generationConfig for reasoning-only request, got none")
	}
	tc, ok := gc["thinkingConfig"].(map[string]any)
	if !ok || tc["thinkingLevel"] != "low" {
		t.Errorf("expected thinkingLevel=low, got %v", gc["thinkingConfig"])
	}
}

// TestTranslateRequestNoReasoningEffortOmitsThinkingConfig verifies that when
// reasoning_effort is unset (and no other gen-config field forces one), the
// request carries no thinkingConfig, leaving Gemini at its default thinking.
func TestTranslateRequestNoReasoningEffortOmitsThinkingConfig(t *testing.T) {
	req := &llm.Request{
		Model:    "gemini-3-flash-preview",
		Messages: []llm.Message{llm.UserMessage("Hello")},
		// ReasoningEffort unset, no other gen-config fields
	}
	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if gc, ok := m["generationConfig"].(map[string]any); ok {
		if _, has := gc["thinkingConfig"]; has {
			t.Errorf("expected no thinkingConfig when reasoning_effort unset, got %v", gc["thinkingConfig"])
		}
	}
}
