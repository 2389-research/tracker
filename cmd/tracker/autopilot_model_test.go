// ABOUTME: Tests --autopilot-model: flag parsing and the model the TUI autopilot asks for.
// ABOUTME: A recording LLM adapter shows which model every gate decision requested.
package main

import (
	"context"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/tui"
)

// gateModelRecorder answers every completion with a fixed autopilot decision
// and records the model each request named.
type gateModelRecorder struct {
	mu     sync.Mutex
	models []string
}

func (r *gateModelRecorder) Name() string { return "openai" }
func (r *gateModelRecorder) Close() error { return nil }

func (r *gateModelRecorder) Complete(_ context.Context, req *llm.Request) (*llm.Response, error) {
	r.mu.Lock()
	r.models = append(r.models, req.Model)
	r.mu.Unlock()
	return &llm.Response{
		Message:      llm.AssistantMessage(`{"choice":"yes","reasoning":"ok"}`),
		FinishReason: llm.FinishReason{Reason: "stop"},
	}, nil
}

func (r *gateModelRecorder) Stream(context.Context, *llm.Request) <-chan llm.StreamEvent {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch
}

func (r *gateModelRecorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.models...)
}

func TestParseFlagsAutopilotModel(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "--autopilot", "mid", "--autopilot-model", "gpt-6.1-sol", "pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.autopilotModel != "gpt-6.1-sol" {
		t.Fatalf("autopilotModel = %q, want %q", cfg.autopilotModel, "gpt-6.1-sol")
	}
}

func TestParseFlagsAutopilotModelRequiresAutopilot(t *testing.T) {
	_, err := parseFlags([]string{"tracker", "--autopilot-model", "gpt-6.1-sol", "pipeline.dip"})
	if err == nil || !strings.Contains(err.Error(), "--autopilot-model requires --autopilot") {
		t.Fatalf("parseFlags error = %v, want one saying --autopilot-model requires --autopilot", err)
	}
}

func TestTUIAutopilotUsesAutopilotModel(t *testing.T) {
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
			rec := &gateModelRecorder{}
			client, err := llm.NewClient(llm.WithProvider(rec))
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			// Answer the decision flash at once so Ask doesn't wait on its timer.
			send := func(msg tea.Msg) {
				if m, ok := msg.(tui.MsgGateAutopilot); ok {
					m.ReplyCh <- m.Decision
				}
			}
			iv := chooseTUIAutopilotInterviewer(send, autopilotCfg{persona: "mid", model: tc.model}, client, "")
			if iv == nil {
				t.Fatal("chooseTUIAutopilotInterviewer returned nil")
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
