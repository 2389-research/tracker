// ABOUTME: Tests that the unified reasoning_effort maps to Gemini thinkingConfig.thinkingLevel.
package google

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/llm"
)

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

// A request whose ONLY non-default field is reasoning_effort must still build a
// generationConfig (the early-return guard must not drop it).
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
