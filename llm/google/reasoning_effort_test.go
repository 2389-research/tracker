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
