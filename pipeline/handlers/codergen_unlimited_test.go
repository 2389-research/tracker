// ABOUTME: Tests unlimited turns through real Dippin parsing and native session construction.
// ABOUTME: Pins defaults, warm continuation, invalid negatives, and external-backend refusal.
package handlers

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/dippin-lang/ir"
	"github.com/2389-research/dippin-lang/parser"
	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

func TestDippinNativeTurnPolicy(t *testing.T) {
	for _, tc := range []struct {
		field string
		want  int
		valid bool
	}{
		{"max_turns: -1", -1, true},
		{"max_turns: 7", 7, true},
		{"max_turns: 0", 80, true},
		{"", 80, true},
		{"max_turns: -2", -2, false},
	} {
		t.Run(tc.field, func(t *testing.T) {
			src := fmt.Sprintf(`workflow Turns
  start: Work
  exit: Work
  agent Work
    %s
    prompt: Finish the task
`, tc.field)
			wf, err := parser.NewParser(src, "turns.dip").Parse()
			if err != nil {
				t.Fatal(err)
			}
			if tc.want < 0 && wf.Nodes[0].Config.(ir.AgentConfig).MaxTurns != tc.want {
				t.Fatal("parser dropped negative turn policy")
			}
			graph, err := pipeline.FromDippinIR(wf)
			if err != nil {
				t.Fatal(err)
			}
			node := graph.Nodes["Work"]
			if tc.want < 0 && node.AgentConfig(nil).MaxTurns != tc.want {
				t.Fatalf("node turn policy = %d, want %d", node.AgentConfig(nil).MaxTurns, tc.want)
			}
			h := NewCodergenHandler(nil, t.TempDir())
			backend := NewNativeBackend(nil, nil)
			runCfg, err := h.buildRunConfig(node, "work", backend, pipeline.NewPipelineContext())
			if err != nil {
				t.Fatal(err)
			}
			cfg := backend.buildSessionConfig(runCfg)
			if cfg.MaxTurns != tc.want || runCfg.MaxTurns != tc.want {
				t.Fatalf("run/session turns = %d/%d, want %d", runCfg.MaxTurns, cfg.MaxTurns, tc.want)
			}
			// Construct the real session; no model call is needed to validate the boundary.
			if _, err := agent.NewSession(nil, cfg); (err == nil) != tc.valid {
				t.Fatalf("session error=%v, want valid=%v", err, tc.valid)
			}
		})
	}
}

func TestUnlimitedWarmContinueKeepsTurnPolicy(t *testing.T) {
	workdir := t.TempDir()
	writeTurnOverride(t, workdir, "Work", "130")
	h := NewCodergenHandler(nil, workdir)
	node := &pipeline.Node{ID: "Work", Attrs: map[string]string{"max_turns": "-1"}}
	if got := h.buildConfig(node).MaxTurns; got != -1 {
		t.Fatalf("unlimited warm continuation turns = %d, want -1", got)
	}
	if got := resolveMaxTurns(workdir, "Work", -2); got != -2 {
		t.Fatalf("override masked invalid negative policy: %d", got)
	}
}

func TestNativeBackendDirectTurnPolicy(t *testing.T) {
	for _, turns := range []int{-2, -1, 0, 4} {
		cfg := NewNativeBackend(nil, nil).buildSessionConfig(pipeline.AgentRunConfig{MaxTurns: turns})
		want := turns
		if turns == 0 {
			want = agent.DefaultConfig().MaxTurns
		}
		if cfg.MaxTurns != want {
			t.Errorf("direct MaxTurns=%d became %d, want %d", turns, cfg.MaxTurns, want)
		}
	}
}

func TestExternalBackendsRejectUnlimitedTurns(t *testing.T) {
	h := NewCodergenHandler(nil, t.TempDir())
	node := &pipeline.Node{ID: "Work", Attrs: map[string]string{"max_turns": "-1"}}
	for _, backend := range []pipeline.AgentBackend{&ClaudeCodeBackend{}, &ACPBackend{}} {
		_, err := h.buildRunConfig(node, "work", backend, pipeline.NewPipelineContext())
		if err == nil || !strings.Contains(err.Error(), "native") {
			t.Errorf("backend %T config error=%v, want native-only refusal", backend, err)
		}
	}
	if _, err := buildArgs(pipeline.AgentRunConfig{MaxTurns: -1}); err == nil {
		t.Error("direct claude-code config silently dropped unlimited turns")
	}
	if _, err := (&ACPBackend{}).Run(context.Background(), pipeline.AgentRunConfig{MaxTurns: -1}, nil); err == nil || !strings.Contains(err.Error(), "native") {
		t.Errorf("direct ACP config error=%v, want native-only refusal", err)
	}
}

func TestDippinUnlimitedNativeDeadlineIntegration(t *testing.T) {
	src := `workflow Turns
  start: Work
  exit: Work
  agent Work
    backend: native
    max_turns: -1
    prompt: Finish the task
`
	wf, err := parser.NewParser(src, "turns.dip").Parse()
	if err != nil {
		t.Fatal(err)
	}
	graph, err := pipeline.FromDippinIR(wf)
	if err != nil {
		t.Fatal(err)
	}
	// A deadline that has already elapsed exercises real handler/backend/session
	// execution without a provider request, fixture, or external service.
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	h := NewCodergenHandler(&llm.Client{}, t.TempDir())
	starts, ends := 0, 0
	h.eventHandler = agent.EventHandlerFunc(func(evt agent.Event) {
		switch evt.Type {
		case agent.EventSessionStart:
			starts++
		case agent.EventSessionEnd:
			ends++
		}
	})
	outcome, err := h.Execute(ctx, graph.Nodes["Work"], pipeline.NewPipelineContext())
	if err != nil {
		t.Fatal(err)
	}
	if starts != 1 || ends != 1 || outcome.ContextUpdates[pipeline.ContextKeyLastResponse] != context.DeadlineExceeded.Error() {
		t.Fatalf("deadline outcome=%+v, session starts/ends=%d/%d", outcome, starts, ends)
	}
}
