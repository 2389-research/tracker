// ABOUTME: Tests parsing the llm_fallbacks attr into ordered failover targets.
package handlers

import (
	"testing"

	"github.com/2389-research/tracker/llm"
)

func TestParseFallbackTargets(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []llm.Target
	}{
		{"empty", "", nil},
		{"whitespace", "   ", nil},
		{"single", "openai/gpt-5.2", []llm.Target{{Provider: "openai", Model: "gpt-5.2"}}},
		{"ordered pair", "openai/gpt-5.2, gemini/gemini-2.5-pro", []llm.Target{
			{Provider: "openai", Model: "gpt-5.2"}, {Provider: "gemini", Model: "gemini-2.5-pro"},
		}},
		{"trims whitespace", "  openai / gpt-5.2 ", []llm.Target{{Provider: "openai", Model: "gpt-5.2"}}},
		{"skips malformed", "openai/gpt-5.2, garbage, /nomodel, noprovider/, gemini/g", []llm.Target{
			{Provider: "openai", Model: "gpt-5.2"}, {Provider: "gemini", Model: "g"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseFallbackTargets(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("target[%d] = %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
