// ABOUTME: Tests Config.AutopilotModel — the model native autopilot asks for at each gate.
// ABOUTME: A recording LLM adapter shows which model every gate decision requested.
package tracker

import (
	"context"
	"sync"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// autopilotModelRecorder answers every completion with a fixed autopilot
// decision and records the model each request named.
type autopilotModelRecorder struct {
	mu     sync.Mutex
	models []string
}

func (r *autopilotModelRecorder) Name() string { return "openai" }
func (r *autopilotModelRecorder) Close() error { return nil }

func (r *autopilotModelRecorder) Complete(_ context.Context, req *llm.Request) (*llm.Response, error) {
	r.mu.Lock()
	r.models = append(r.models, req.Model)
	r.mu.Unlock()
	return &llm.Response{
		Message:      llm.AssistantMessage(`{"choice":"yes","reasoning":"ok"}`),
		FinishReason: llm.FinishReason{Reason: "stop"},
	}, nil
}

func (r *autopilotModelRecorder) Stream(context.Context, *llm.Request) <-chan llm.StreamEvent {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch
}

func (r *autopilotModelRecorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.models...)
}

func TestResolveAutopilotUsesConfiguredModel(t *testing.T) {
	cases := []struct {
		name  string
		model string
		want  string
	}{
		{name: "configured model wins", model: "gpt-6.1-sol", want: "gpt-6.1-sol"},
		{name: "empty keeps the default provider's autopilot model", model: "", want: "gpt-4.1-mini"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &autopilotModelRecorder{}
			client, err := llm.NewClient(llm.WithProvider(rec))
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			iv, err := resolveAutopilot(Config{Autopilot: "mid", AutopilotModel: tc.model}, client, nil)
			if err != nil {
				t.Fatalf("resolveAutopilot: %v", err)
			}
			if _, err := iv.Ask("Proceed?", []string{"yes", "no"}, "yes"); err != nil {
				t.Fatalf("Ask: %v", err)
			}
			if got := rec.seen(); len(got) != 1 || got[0] != tc.want {
				t.Fatalf("gate decision asked for models %q, want [%q]", got, tc.want)
			}
		})
	}
}
