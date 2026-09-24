// ABOUTME: Tests for merging a request's per-provider options into an adapter's JSON body.
// ABOUTME: Covers the no-options passthrough, key overrides, reserved keys, and a bad body.
package llm

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMergeProviderOptions_NoOptionsReturnsBodyUnchanged(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":10}`)
	cases := map[string]map[string]any{
		"nil options":       nil,
		"other provider":    {"gemini": map[string]any{"top_k": 5}},
		"options not a map": {"openai": "top_k=5"},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := MergeProviderOptions(body, opts, "openai")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != string(body) {
				t.Errorf("body changed: got %s, want %s", got, body)
			}
		})
	}
}

func TestMergeProviderOptions_OverridesAndSkipsReserved(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":10}`)
	opts := map[string]any{"anthropic": map[string]any{
		"max_tokens":   20,
		"top_k":        5,
		"beta_headers": "interleaved-thinking",
		"auto_cache":   false,
	}}
	got, err := MergeProviderOptions(body, opts, "anthropic", "beta_headers", "auto_cache")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var merged map[string]any
	if err := json.Unmarshal(got, &merged); err != nil {
		t.Fatalf("merged body is not JSON: %v", err)
	}
	want := map[string]any{"model": "m", "max_tokens": 20.0, "top_k": 5.0}
	if !reflect.DeepEqual(merged, want) {
		t.Errorf("merged body = %v, want %v", merged, want)
	}
}

func TestMergeProviderOptions_InvalidBody(t *testing.T) {
	opts := map[string]any{"openai": map[string]any{"top_k": 5}}
	if _, err := MergeProviderOptions([]byte("not json"), opts, "openai"); err == nil {
		t.Error("expected an error for a body that is not a JSON object")
	}
}
