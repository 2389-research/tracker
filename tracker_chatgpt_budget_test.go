// ABOUTME: Tests warnSubscriptionCeilings — the run-start warning that a dollar
// ABOUTME: ceiling can't see openai calls billed to a ChatGPT subscription (design §3 "Cost (D6)").
package tracker

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/2389-research/tracker/internal/chatgptauth"
	"github.com/2389-research/tracker/internal/diag"
	"github.com/2389-research/tracker/pipeline"
)

// openAIGraph returns a single-node graph whose codergen node routes to
// openai, with any extra node attrs (e.g. a ReviewCodex-style max_cost_usd)
// merged in.
func openAIGraph(extraNodeAttrs map[string]string) *pipeline.Graph {
	attrs := map[string]string{"llm_provider": "openai"}
	for k, v := range extraNodeAttrs {
		attrs[k] = v
	}
	return &pipeline.Graph{
		Nodes: map[string]*pipeline.Node{
			"Agent": {ID: "Agent", Handler: "codergen", Attrs: attrs},
		},
	}
}

func anthropicGraph() *pipeline.Graph {
	return &pipeline.Graph{
		Nodes: map[string]*pipeline.Node{
			"Agent": {ID: "Agent", Handler: "codergen", Attrs: map[string]string{"llm_provider": "anthropic"}},
		},
	}
}

func TestWarnSubscriptionCeilings(t *testing.T) {
	tests := []struct {
		name   string
		mode   chatgptauth.Mode
		graph  *pipeline.Graph
		budget pipeline.BudgetLimits
		want   bool
	}{
		{
			name:   "chatgpt + openai node + MaxCostCents ceiling warns",
			mode:   chatgptauth.ModeChatGPT,
			graph:  openAIGraph(nil),
			budget: pipeline.BudgetLimits{MaxCostCents: 400},
			want:   true,
		},
		{
			name:  "chatgpt + openai node + node-level max_cost_usd warns (ReviewCodex-style cap)",
			mode:  chatgptauth.ModeChatGPT,
			graph: openAIGraph(map[string]string{"max_cost_usd": "4.00"}),
			want:  true,
		},
		{
			name: "chatgpt + openai node + graph-level max_cost_usd warns",
			mode: chatgptauth.ModeChatGPT,
			graph: &pipeline.Graph{
				Attrs: map[string]string{"max_cost_usd": "4.00"},
				Nodes: map[string]*pipeline.Node{
					"Agent": {ID: "Agent", Handler: "codergen", Attrs: map[string]string{"llm_provider": "openai"}},
				},
			},
			want: true,
		},
		{
			name:   "api-key mode is silent even with a ceiling",
			mode:   chatgptauth.ModeAPIKey,
			graph:  openAIGraph(nil),
			budget: pipeline.BudgetLimits{MaxCostCents: 400},
			want:   false,
		},
		{
			name:   "no openai nodes is silent",
			mode:   chatgptauth.ModeChatGPT,
			graph:  anthropicGraph(),
			budget: pipeline.BudgetLimits{MaxCostCents: 400},
			want:   false,
		},
		{
			name:  "no ceilings is silent",
			mode:  chatgptauth.ModeChatGPT,
			graph: openAIGraph(nil),
			want:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(chatgptauth.EnvVar, string(tc.mode))
			var buf bytes.Buffer
			diag.SetLogger(slog.New(slog.NewTextHandler(&buf, nil)))
			t.Cleanup(func() { diag.SetLogger(nil) })

			warnSubscriptionCeilings(tc.budget, tc.graph)

			got := strings.Contains(buf.String(), "openai runs on your ChatGPT subscription")
			if got != tc.want {
				t.Errorf("warned = %v, want %v; log = %q", got, tc.want, buf.String())
			}
		})
	}
}

// TestWarnSubscriptionCeilingsExactMessage pins the brief's exact wording —
// downstream tooling (and operators) may grep for it.
func TestWarnSubscriptionCeilingsExactMessage(t *testing.T) {
	t.Setenv(chatgptauth.EnvVar, string(chatgptauth.ModeChatGPT))
	var buf bytes.Buffer
	diag.SetLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { diag.SetLogger(nil) })

	warnSubscriptionCeilings(pipeline.BudgetLimits{MaxCostCents: 400}, openAIGraph(nil))

	const want = "[tracker] openai runs on your ChatGPT subscription and costs $0 here: --max-cost, max_cost_cents and max_cost_usd ceilings don't count it. --max-tokens still applies."
	if !strings.Contains(buf.String(), want) {
		t.Errorf("log = %q, want to contain %q", buf.String(), want)
	}
}

// TestWarnSubscriptionCeilingsNilGraph guards buildEngineOpts's nil-graph call
// path (tracker_steering_test.go calls it with a nil graph).
func TestWarnSubscriptionCeilingsNilGraph(t *testing.T) {
	t.Setenv(chatgptauth.EnvVar, string(chatgptauth.ModeChatGPT))
	var buf bytes.Buffer
	diag.SetLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { diag.SetLogger(nil) })

	warnSubscriptionCeilings(pipeline.BudgetLimits{MaxCostCents: 400}, nil) // must not panic

	if buf.Len() != 0 {
		t.Errorf("warned on nil graph: %q", buf.String())
	}
}
