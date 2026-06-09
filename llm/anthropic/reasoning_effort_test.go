// ABOUTME: Tests that the unified reasoning_effort maps to Anthropic output_config.effort.
package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// TestTranslateRequestReasoningEffort verifies that each reasoning_effort level
// is emitted as output_config.effort in the Anthropic request body (the GA
// effort knob). "max" is included because it is valid on Opus-tier models.
func TestTranslateRequestReasoningEffort(t *testing.T) {
	for _, effort := range []string{"low", "medium", "high", "max"} {
		req := &llm.Request{
			Model:           "claude-opus-4-6",
			Messages:        []llm.Message{llm.UserMessage("Decompose this spec")},
			ReasoningEffort: effort,
		}
		body, err := translateRequest(req)
		if err != nil {
			t.Fatalf("effort=%s: %v", effort, err)
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("effort=%s: %v", effort, err)
		}
		oc, ok := m["output_config"].(map[string]any)
		if !ok {
			t.Fatalf("effort=%s: expected output_config object, got %v", effort, m["output_config"])
		}
		if oc["effort"] != effort {
			t.Errorf("effort=%s: output_config.effort = %v, want %s", effort, oc["effort"], effort)
		}
	}
}

// TestTranslateRequestNoReasoningEffortOmitsOutputConfig verifies that when
// reasoning_effort is unset, the request omits output_config entirely so the
// model falls back to its API default (high) rather than being pinned.
func TestTranslateRequestNoReasoningEffortOmitsOutputConfig(t *testing.T) {
	req := &llm.Request{
		Model:    "claude-opus-4-6",
		Messages: []llm.Message{llm.UserMessage("Hello")},
		// ReasoningEffort unset
	}
	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["output_config"]; ok {
		t.Errorf("expected no output_config when reasoning_effort unset, got %v", m["output_config"])
	}
}
