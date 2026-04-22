// ABOUTME: Tests for the manager loop handler that supervises a child pipeline asynchronously.
// ABOUTME: Validates child launch, polling, context merge, max_cycles exit, and cancellation.
package handlers

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// buildChildGraph creates a minimal child pipeline graph (start → step → exit)
// where "step" uses the given handler name. Uses Mdiamond/Msquare shapes so
// AddNode auto-assigns start/exit nodes. The step handler is set after AddNode
// to prevent shape-based auto-resolution from overriding it.
func buildChildGraph(stepHandlerName string) *pipeline.Graph {
	g := pipeline.NewGraph("child")
	g.AddNode(&pipeline.Node{ID: "start", Shape: "Mdiamond", Attrs: map[string]string{}})
	g.AddNode(&pipeline.Node{ID: "step", Shape: "box", Attrs: map[string]string{}})
	g.Nodes["step"].Handler = stepHandlerName
	g.AddNode(&pipeline.Node{ID: "exit", Shape: "Msquare", Attrs: map[string]string{}})
	g.AddEdge(&pipeline.Edge{From: "start", To: "step"})
	g.AddEdge(&pipeline.Edge{From: "step", To: "exit"})
	return g
}

// collectingEventHandler captures pipeline events for assertion.
type collectingEventHandler struct {
	mu     sync.Mutex
	events []pipeline.PipelineEvent
}

func (h *collectingEventHandler) HandlePipelineEvent(evt pipeline.PipelineEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, evt)
}

func (h *collectingEventHandler) Events() []pipeline.PipelineEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := make([]pipeline.PipelineEvent, len(h.events))
	copy(cp, h.events)
	return cp
}

func TestManagerLoopHandler_Name(t *testing.T) {
	h := NewManagerLoopHandler(nil, nil, nil, nil)
	if h.Name() != "stack.manager_loop" {
		t.Errorf("Name() = %q, want %q", h.Name(), "stack.manager_loop")
	}
}

func TestManagerLoopHandler_MissingSubgraphRef(t *testing.T) {
	h := NewManagerLoopHandler(nil, nil, nil, nil)
	node := &pipeline.Node{ID: "mgr", Handler: "stack.manager_loop", Attrs: map[string]string{}}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected OutcomeFail, got %q", outcome.Status)
	}
	if err == nil {
		t.Fatal("expected error for missing subgraph_ref")
	}
}

func TestManagerLoopHandler_SubgraphNotFound(t *testing.T) {
	graphs := map[string]*pipeline.Graph{}
	h := NewManagerLoopHandler(graphs, nil, nil, nil)
	node := &pipeline.Node{ID: "mgr", Handler: "stack.manager_loop", Attrs: map[string]string{
		"subgraph_ref": "nonexistent",
	}}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected OutcomeFail, got %q", outcome.Status)
	}
	if err == nil {
		t.Fatal("expected error for missing graph")
	}
}

func TestManagerLoopHandler_ChildSucceeds(t *testing.T) {
	childGraph := buildChildGraph("step_handler")

	// Build a registry with a stub that succeeds and writes a context key.
	registry := pipeline.NewHandlerRegistry()
	registry.Register(NewStartHandler())
	registry.Register(NewExitHandler())
	registry.Register(&stubHandler{
		name: "step_handler",
		execFunc: func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			return pipeline.Outcome{
				Status:         pipeline.OutcomeSuccess,
				ContextUpdates: map[string]string{"child_key": "child_value"},
			}, nil
		},
	})

	graphs := map[string]*pipeline.Graph{"child_pipeline": childGraph}
	h := NewManagerLoopHandler(graphs, registry, pipeline.PipelineNoopHandler, nil)

	node := &pipeline.Node{ID: "mgr", Handler: "stack.manager_loop", Attrs: map[string]string{
		"subgraph_ref":          "child_pipeline",
		"manager.poll_interval": "1ms",
		"manager.max_cycles":    "100",
	}}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected OutcomeSuccess, got %q", outcome.Status)
	}

	// Child context should be merged into outcome.
	if outcome.ContextUpdates["child_key"] != "child_value" {
		t.Errorf("expected child_key=child_value in ContextUpdates, got %v", outcome.ContextUpdates)
	}

	// Parent context should have status keys.
	if v, _ := pctx.Get("stack.child.status"); v != "success" {
		t.Errorf("expected stack.child.status=success, got %q", v)
	}
}

func TestManagerLoopHandler_ChildFails(t *testing.T) {
	childGraph := buildChildGraph("step_handler")

	registry := pipeline.NewHandlerRegistry()
	registry.Register(NewStartHandler())
	registry.Register(NewExitHandler())
	registry.Register(&stubHandler{
		name: "step_handler",
		outcome: pipeline.Outcome{
			Status:         pipeline.OutcomeFail,
			ContextUpdates: map[string]string{"fail_key": "fail_value"},
		},
	})

	graphs := map[string]*pipeline.Graph{"child_pipeline": childGraph}
	h := NewManagerLoopHandler(graphs, registry, pipeline.PipelineNoopHandler, nil)

	node := &pipeline.Node{ID: "mgr", Handler: "stack.manager_loop", Attrs: map[string]string{
		"subgraph_ref":          "child_pipeline",
		"manager.poll_interval": "1ms",
		"manager.max_cycles":    "100",
	}}
	pctx := pipeline.NewPipelineContext()

	outcome, _ := h.Execute(context.Background(), node, pctx)
	// The child engine may return both a result (Status=fail) and an error (strict
	// failure edges). Either way the manager should report OutcomeFail.
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected OutcomeFail, got %q", outcome.Status)
	}
	if v, _ := pctx.Get("stack.child.status"); v != "failed" {
		t.Errorf("expected stack.child.status=failed, got %q", v)
	}
}

func TestManagerLoopHandler_MaxCyclesExceeded(t *testing.T) {
	childGraph := buildChildGraph("step_handler")

	// Stub that blocks until context is cancelled.
	registry := pipeline.NewHandlerRegistry()
	registry.Register(NewStartHandler())
	registry.Register(NewExitHandler())
	registry.Register(&stubHandler{
		name: "step_handler",
		execFunc: func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			<-ctx.Done()
			return pipeline.Outcome{Status: pipeline.OutcomeFail}, ctx.Err()
		},
	})

	graphs := map[string]*pipeline.Graph{"child_pipeline": childGraph}
	h := NewManagerLoopHandler(graphs, registry, pipeline.PipelineNoopHandler, nil)

	node := &pipeline.Node{ID: "mgr", Handler: "stack.manager_loop", Attrs: map[string]string{
		"subgraph_ref":          "child_pipeline",
		"manager.poll_interval": "1ms",
		"manager.max_cycles":    "3",
	}}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected error for max_cycles exceeded")
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected OutcomeFail, got %q", outcome.Status)
	}
	if v, _ := pctx.Get("stack.child.status"); v != "max_cycles_exceeded" {
		t.Errorf("expected stack.child.status=max_cycles_exceeded, got %q", v)
	}
	if v, _ := pctx.Get("stack.child.cycles"); v != "3" {
		t.Errorf("expected stack.child.cycles=3, got %q", v)
	}
}

func TestManagerLoopHandler_CtxCancellation(t *testing.T) {
	childGraph := buildChildGraph("step_handler")

	registry := pipeline.NewHandlerRegistry()
	registry.Register(NewStartHandler())
	registry.Register(NewExitHandler())
	registry.Register(&stubHandler{
		name: "step_handler",
		execFunc: func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			<-ctx.Done()
			return pipeline.Outcome{Status: pipeline.OutcomeFail}, ctx.Err()
		},
	})

	graphs := map[string]*pipeline.Graph{"child_pipeline": childGraph}
	h := NewManagerLoopHandler(graphs, registry, pipeline.PipelineNoopHandler, nil)

	node := &pipeline.Node{ID: "mgr", Handler: "stack.manager_loop", Attrs: map[string]string{
		"subgraph_ref":          "child_pipeline",
		"manager.poll_interval": "1ms",
		"manager.max_cycles":    "10000",
	}}
	pctx := pipeline.NewPipelineContext()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	outcome, err := h.Execute(ctx, node, pctx)
	if err == nil {
		t.Fatal("expected error for context cancellation")
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected OutcomeFail, got %q", outcome.Status)
	}
	if v, _ := pctx.Get("stack.child.status"); v != "cancelled" {
		t.Errorf("expected stack.child.status=cancelled, got %q", v)
	}
}

func TestManagerLoopHandler_ChildPanic(t *testing.T) {
	childGraph := buildChildGraph("step_handler")

	registry := pipeline.NewHandlerRegistry()
	registry.Register(NewStartHandler())
	registry.Register(NewExitHandler())
	registry.Register(&stubHandler{
		name: "step_handler",
		execFunc: func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			panic("test panic in child")
		},
	})

	graphs := map[string]*pipeline.Graph{"child_pipeline": childGraph}
	h := NewManagerLoopHandler(graphs, registry, pipeline.PipelineNoopHandler, nil)

	node := &pipeline.Node{ID: "mgr", Handler: "stack.manager_loop", Attrs: map[string]string{
		"subgraph_ref":          "child_pipeline",
		"manager.poll_interval": "1ms",
		"manager.max_cycles":    "100",
	}}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected error from panic in child")
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected OutcomeFail, got %q", outcome.Status)
	}
	if v, _ := pctx.Get("stack.child.status"); v != "error" {
		t.Errorf("expected stack.child.status=error, got %q", v)
	}
}

func TestManagerLoopHandler_EventsEmitted(t *testing.T) {
	childGraph := buildChildGraph("step_handler")

	registry := pipeline.NewHandlerRegistry()
	registry.Register(NewStartHandler())
	registry.Register(NewExitHandler())
	// Block the child on a channel until the manager has ticked at least
	// one cycle. This replaces a fragile time.Sleep — the manager's
	// EventManagerCycleTick is what the assertion below is looking for,
	// and it is emitted from a goroutine we can synchronise on via the
	// collector.
	releaseChild := make(chan struct{})
	registry.Register(&stubHandler{
		name: "step_handler",
		execFunc: func(ctx context.Context, _ *pipeline.Node, _ *pipeline.PipelineContext) (pipeline.Outcome, error) {
			select {
			case <-releaseChild:
			case <-ctx.Done():
				return pipeline.Outcome{}, ctx.Err()
			}
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
		},
	})

	collector := &collectingEventHandler{}
	graphs := map[string]*pipeline.Graph{"child_pipeline": childGraph}
	h := NewManagerLoopHandler(graphs, registry, collector, nil)

	node := &pipeline.Node{ID: "mgr", Handler: "stack.manager_loop", Attrs: map[string]string{
		"subgraph_ref":          "child_pipeline",
		"manager.poll_interval": "1ms",
		"manager.max_cycles":    "100",
	}}
	pctx := pipeline.NewPipelineContext()

	// Watch the collector for the first cycle tick, then release the
	// child. Poll on a short interval that's still fast enough to be
	// effectively instantaneous but doesn't race against the test harness.
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			for _, evt := range collector.Events() {
				if evt.Type == pipeline.EventManagerCycleTick {
					close(releaseChild)
					return
				}
			}
			time.Sleep(500 * time.Microsecond)
		}
		// Safety valve: release anyway so the test fails loudly on the
		// assertion below rather than hanging indefinitely.
		close(releaseChild)
	}()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Fatalf("expected success, got %q", outcome.Status)
	}

	events := collector.Events()
	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}

	// Verify we got a stage_started event for the child launch.
	var hasStarted, hasCompleted, hasCycleTick bool
	for _, evt := range events {
		switch evt.Type {
		case pipeline.EventStageStarted:
			hasStarted = true
		case pipeline.EventStageCompleted:
			hasCompleted = true
		case pipeline.EventManagerCycleTick:
			hasCycleTick = true
		}
	}
	if !hasStarted {
		t.Error("expected EventStageStarted event")
	}
	if !hasCompleted {
		t.Error("expected EventStageCompleted event")
	}
	if !hasCycleTick {
		t.Error("expected at least one EventManagerCycleTick event")
	}
}

func TestManagerLoopHandler_DefaultConfig(t *testing.T) {
	// Verify default parsing when no attrs are specified (except subgraph_ref).
	cfg, err := parseManagerLoopConfig(map[string]string{
		"subgraph_ref": "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.subgraphRef != "test" {
		t.Errorf("subgraphRef = %q, want %q", cfg.subgraphRef, "test")
	}
	if cfg.pollInterval != 45*time.Second {
		t.Errorf("pollInterval = %v, want 45s", cfg.pollInterval)
	}
	if cfg.maxCycles != 1000 {
		t.Errorf("maxCycles = %d, want 1000", cfg.maxCycles)
	}
}

func TestManagerLoopHandler_CustomConfig(t *testing.T) {
	cfg, err := parseManagerLoopConfig(map[string]string{
		"subgraph_ref":            "my_child",
		"manager.poll_interval":   "10s",
		"manager.max_cycles":      "50",
		"manager.stop_condition":  "stack.child.cycles = 5",
		"manager.steer_condition": "stack.child.cycles = 3",
		"manager.steer_context":   "hint=speed_up,priority=high",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.subgraphRef != "my_child" {
		t.Errorf("subgraphRef = %q, want %q", cfg.subgraphRef, "my_child")
	}
	if cfg.pollInterval != 10*time.Second {
		t.Errorf("pollInterval = %v, want 10s", cfg.pollInterval)
	}
	if cfg.maxCycles != 50 {
		t.Errorf("maxCycles = %d, want 50", cfg.maxCycles)
	}
	if cfg.stopCondition != "stack.child.cycles = 5" {
		t.Errorf("stopCondition = %q, want %q", cfg.stopCondition, "stack.child.cycles = 5")
	}
	if cfg.steerExpr != "stack.child.cycles = 3" {
		t.Errorf("steerExpr = %q, want %q", cfg.steerExpr, "stack.child.cycles = 3")
	}
	if cfg.steerKeys["hint"] != "speed_up" {
		t.Errorf("steerKeys[hint] = %q, want %q", cfg.steerKeys["hint"], "speed_up")
	}
	if cfg.steerKeys["priority"] != "high" {
		t.Errorf("steerKeys[priority] = %q, want %q", cfg.steerKeys["priority"], "high")
	}
}

func TestManagerLoopHandler_StopConditionMet(t *testing.T) {
	childGraph := buildChildGraph("step_handler")

	// Stub that blocks until context is cancelled.
	registry := pipeline.NewHandlerRegistry()
	registry.Register(NewStartHandler())
	registry.Register(NewExitHandler())
	registry.Register(&stubHandler{
		name: "step_handler",
		execFunc: func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			<-ctx.Done()
			return pipeline.Outcome{Status: pipeline.OutcomeFail}, ctx.Err()
		},
	})

	graphs := map[string]*pipeline.Graph{"child_pipeline": childGraph}
	h := NewManagerLoopHandler(graphs, registry, pipeline.PipelineNoopHandler, nil)

	// Stop condition triggers when cycles reaches 3.
	node := &pipeline.Node{ID: "mgr", Handler: "stack.manager_loop", Attrs: map[string]string{
		"subgraph_ref":           "child_pipeline",
		"manager.poll_interval":  "1ms",
		"manager.max_cycles":     "100",
		"manager.stop_condition": "stack.child.cycles = 3",
	}}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Stop condition returns success — intentional early exit.
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected OutcomeSuccess, got %q", outcome.Status)
	}
	if v, _ := pctx.Get("stack.child.status"); v != "stop_condition_met" {
		t.Errorf("expected stack.child.status=stop_condition_met, got %q", v)
	}
	if v, _ := pctx.Get("stack.child.cycles"); v != "3" {
		t.Errorf("expected stack.child.cycles=3, got %q", v)
	}
}

func TestManagerLoopHandler_StopConditionNotMet(t *testing.T) {
	childGraph := buildChildGraph("step_handler")

	// Child completes quickly — stop condition never fires.
	registry := pipeline.NewHandlerRegistry()
	registry.Register(NewStartHandler())
	registry.Register(NewExitHandler())
	registry.Register(&stubHandler{
		name: "step_handler",
		execFunc: func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
		},
	})

	graphs := map[string]*pipeline.Graph{"child_pipeline": childGraph}
	h := NewManagerLoopHandler(graphs, registry, pipeline.PipelineNoopHandler, nil)

	// Stop condition would fire at cycles=100, but child finishes first.
	node := &pipeline.Node{ID: "mgr", Handler: "stack.manager_loop", Attrs: map[string]string{
		"subgraph_ref":           "child_pipeline",
		"manager.poll_interval":  "1ms",
		"manager.max_cycles":     "1000",
		"manager.stop_condition": "stack.child.cycles = 100",
	}}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected OutcomeSuccess, got %q", outcome.Status)
	}
	// Child completed normally, not via stop condition.
	if v, _ := pctx.Get("stack.child.status"); v != "success" {
		t.Errorf("expected stack.child.status=success, got %q", v)
	}
}

func TestManagerLoopHandler_SteeringInjection(t *testing.T) {
	// Build a two-step child graph: start → step1 → step2 → exit.
	// step1 blocks so the manager has time to send steering.
	// The engine drains the steering channel between step1 and step2.
	// step2 reads the steered value.
	g := pipeline.NewGraph("child")
	g.AddNode(&pipeline.Node{ID: "start", Shape: "Mdiamond", Attrs: map[string]string{}})
	g.AddNode(&pipeline.Node{ID: "step1", Shape: "box", Attrs: map[string]string{}})
	g.Nodes["step1"].Handler = "step1_handler"
	g.AddNode(&pipeline.Node{ID: "step2", Shape: "box", Attrs: map[string]string{}})
	g.Nodes["step2"].Handler = "step2_handler"
	g.AddNode(&pipeline.Node{ID: "exit", Shape: "Msquare", Attrs: map[string]string{}})
	g.AddEdge(&pipeline.Edge{From: "start", To: "step1"})
	g.AddEdge(&pipeline.Edge{From: "step1", To: "step2"})
	g.AddEdge(&pipeline.Edge{From: "step2", To: "exit"})

	var childSawHint string
	registry := pipeline.NewHandlerRegistry()
	registry.Register(NewStartHandler())
	registry.Register(NewExitHandler())
	// step1 blocks on a channel rather than sleeping — the watcher
	// goroutine closes the channel as soon as the manager has injected
	// "hint" into the parent context (which is the observable signal
	// that steering fired). Synchronising on the actual state change
	// makes the test deterministic across slow CI runners.
	releaseStep1 := make(chan struct{})
	registry.Register(&stubHandler{
		name: "step1_handler",
		execFunc: func(ctx context.Context, _ *pipeline.Node, _ *pipeline.PipelineContext) (pipeline.Outcome, error) {
			select {
			case <-releaseStep1:
			case <-ctx.Done():
				return pipeline.Outcome{}, ctx.Err()
			}
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
		},
	})
	registry.Register(&stubHandler{
		name: "step2_handler",
		execFunc: func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			// By now the engine has drained the steering channel (between step1 and step2).
			val, _ := pctx.Get("hint")
			childSawHint = val
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
		},
	})

	// Collect events so the watcher can observe when the manager emitted
	// "steered N keys into child" — the deterministic signal that the
	// steering channel was written to and ready to be drained by the
	// engine between step1 and step2.
	steerCollector := &collectingEventHandler{}
	graphs := map[string]*pipeline.Graph{"child_pipeline": g}
	h := NewManagerLoopHandler(graphs, registry, steerCollector, nil)

	// Steer condition fires at cycles=1, injecting "hint=go_faster".
	node := &pipeline.Node{ID: "mgr", Handler: "stack.manager_loop", Attrs: map[string]string{
		"subgraph_ref":            "child_pipeline",
		"manager.poll_interval":   "1ms",
		"manager.max_cycles":      "100",
		"manager.steer_condition": "stack.child.cycles = 1",
		"manager.steer_context":   "hint=go_faster",
	}}
	pctx := pipeline.NewPipelineContext()

	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			for _, evt := range steerCollector.Events() {
				if strings.Contains(evt.Message, "steered") {
					close(releaseStep1)
					return
				}
			}
			time.Sleep(500 * time.Microsecond)
		}
		close(releaseStep1) // safety valve
	}()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected OutcomeSuccess, got %q", outcome.Status)
	}

	// step2 should have seen the steered value (drained between step1 and step2).
	if childSawHint != "go_faster" {
		t.Errorf("child saw hint=%q, want %q", childSawHint, "go_faster")
	}
}

func TestParseSteerContext(t *testing.T) {
	tests := []struct {
		input string
		want  map[string]string
	}{
		{"", nil},
		{"key=val", map[string]string{"key": "val"}},
		{"a=1,b=2", map[string]string{"a": "1", "b": "2"}},
		{" a = 1 , b = 2 ", map[string]string{"a": "1", "b": "2"}},
		{"noequals", nil},
	}
	for _, tt := range tests {
		got := parseSteerContext(tt.input)
		if tt.want == nil {
			if got != nil {
				t.Errorf("parseSteerContext(%q) = %v, want nil", tt.input, got)
			}
			continue
		}
		for k, v := range tt.want {
			if got[k] != v {
				t.Errorf("parseSteerContext(%q)[%q] = %q, want %q", tt.input, k, got[k], v)
			}
		}
	}
}

// TestParseSteerContext_PercentDecoding verifies the decoder reverses the
// percent-encoding applied by pipeline.flattenSteerContext (mirroring
// dippin-lang v0.22.0 export.flattenSteerContext). Required for lossless
// DOT → IR → adapter → handler round-trips when keys/values contain the
// three reserved delimiter chars.
func TestParseSteerContext_PercentDecoding(t *testing.T) {
	// Encoded form produced by the adapter for keys/values with reserved chars.
	in := "hint=speed%2Cup,priority=high%3Dcritical,tag=50%25off"
	got := parseSteerContext(in)
	want := map[string]string{
		"hint":     "speed,up",
		"priority": "high=critical",
		"tag":      "50%off",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("parseSteerContext[%q] = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("parseSteerContext returned %d entries, want %d (%v)", len(got), len(want), got)
	}
}

// TestParseSteerContext_LiteralPercentEncodedSequenceIsPreserved proves the
// decoder does not double-decode a literal `%2C` sequence that the encoder
// wrote as `%252C`. The encoder replaces `%` first (so `%` becomes `%25`,
// and `%2C` becomes `%252C`); the decoder uses strings.NewReplacer with
// non-overlapping left-to-right scanning, so on `%252C` it matches `%25`
// at position 0, emits `%`, advances past the match, and the trailing `2C`
// is copied verbatim — result `%2C`, not `,`.
//
// This is a regression guard against a re-ordering of the decoder replacer
// arguments (a Copilot review raised this as a suspected bug in PR #170
// round-2; the test confirms the current implementation is correct).
func TestParseSteerContext_LiteralPercentEncodedSequenceIsPreserved(t *testing.T) {
	// Note `literal=keep%252Cexact`: the source value was `keep%2Cexact`,
	// encoded to `keep%252Cexact`. The decoder must yield `keep%2Cexact`,
	// NOT `keep,exact`.
	in := "hint=speed%2Cup,priority=high%3Dcritical,tag=50%25off,literal=keep%252Cexact"
	got := parseSteerContext(in)
	want := map[string]string{
		"hint":     "speed,up",
		"priority": "high=critical",
		"tag":      "50%off",
		"literal":  "keep%2Cexact",
	}
	if len(got) != len(want) {
		t.Fatalf("parseSteerContext returned %d entries, want %d (%v)", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("parseSteerContext[%q] = %q, want %q", k, got[k], v)
		}
	}
}

// TestParseManagerLoopConfig_UnprefixedAttrs verifies the handler reads the
// unprefixed DOT contract attrs emitted by the v0.22.0 adapter. These are
// the authoritative names; the legacy "manager.*" forms remain for manually
// authored DOT files.
func TestParseManagerLoopConfig_UnprefixedAttrs(t *testing.T) {
	cfg, err := parseManagerLoopConfig(map[string]string{
		"subgraph_ref":    "child",
		"poll_interval":   "15s",
		"max_cycles":      "20",
		"stop_condition":  "stack.child.cycles = 9",
		"steer_condition": "stack.child.cycles = 3",
		"steer_context":   "hint=speed%2Cup,priority=high",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.subgraphRef != "child" {
		t.Errorf("subgraphRef = %q, want %q", cfg.subgraphRef, "child")
	}
	if cfg.pollInterval != 15*time.Second {
		t.Errorf("pollInterval = %v, want 15s", cfg.pollInterval)
	}
	if cfg.maxCycles != 20 {
		t.Errorf("maxCycles = %d, want 20", cfg.maxCycles)
	}
	if cfg.stopCondition != "stack.child.cycles = 9" {
		t.Errorf("stopCondition = %q, want %q", cfg.stopCondition, "stack.child.cycles = 9")
	}
	if cfg.steerExpr != "stack.child.cycles = 3" {
		t.Errorf("steerExpr = %q, want %q", cfg.steerExpr, "stack.child.cycles = 3")
	}
	if cfg.steerKeys["hint"] != "speed,up" {
		t.Errorf("steerKeys[hint] = %q, want %q (expected percent-decoded)", cfg.steerKeys["hint"], "speed,up")
	}
	if cfg.steerKeys["priority"] != "high" {
		t.Errorf("steerKeys[priority] = %q, want %q", cfg.steerKeys["priority"], "high")
	}
}

// TestParseManagerLoopConfig_PartialSteeringRejected verifies that supplying
// only one half of the steering pair (condition without context, or context
// without condition) is rejected at parse time. A half-configured steering
// mechanism is inert (channel creation in Execute requires both), so silently
// accepting the partial config would violate CLAUDE.md's "never silently
// swallow errors" rule.
func TestParseManagerLoopConfig_PartialSteeringRejected(t *testing.T) {
	t.Run("steer_condition without steer_context", func(t *testing.T) {
		_, err := parseManagerLoopConfig(map[string]string{
			"subgraph_ref":    "child",
			"steer_condition": "stack.child.cycles = 3",
		})
		if err == nil {
			t.Fatal("expected error when steer_condition is set without steer_context")
		}
		if !strings.Contains(err.Error(), "steer_condition is set but steer_context is empty") {
			t.Errorf("error = %q, want message about steer_context being empty", err.Error())
		}
	})

	t.Run("steer_context without steer_condition", func(t *testing.T) {
		_, err := parseManagerLoopConfig(map[string]string{
			"subgraph_ref":  "child",
			"steer_context": "hint=go_faster",
		})
		if err == nil {
			t.Fatal("expected error when steer_context is set without steer_condition")
		}
		if !strings.Contains(err.Error(), "steer_context is set but steer_condition is empty") {
			t.Errorf("error = %q, want message about steer_condition being empty", err.Error())
		}
	})

	t.Run("malformed steer_context surfaces as invalid, not empty", func(t *testing.T) {
		// A non-empty value with no `=` pairs parses to nil — the prior
		// error message conflated this with "unset". The message must now
		// cite the raw value and call out invalidity.
		_, err := parseManagerLoopConfig(map[string]string{
			"subgraph_ref":    "child",
			"steer_condition": "stack.child.cycles = 3",
			"steer_context":   "bad",
		})
		if err == nil {
			t.Fatal("expected error for malformed steer_context")
		}
		if !strings.Contains(err.Error(), "invalid") {
			t.Errorf("error = %q, want message to call out invalid steer_context", err.Error())
		}
		if !strings.Contains(err.Error(), "\"bad\"") {
			t.Errorf("error = %q, want message to include the raw invalid value %q", err.Error(), "bad")
		}
	})

	t.Run("malformed steer_context is rejected even without steer_condition", func(t *testing.T) {
		// Edge case: author sets steer_context with malformed content and
		// no steer_condition. Previously this slipped through both
		// validation branches (neither fires when both sides are empty
		// from the validator's perspective) and the malformed input was
		// silently discarded. The invalid-steer-context check runs
		// independently of steer_condition, so this must now error.
		_, err := parseManagerLoopConfig(map[string]string{
			"subgraph_ref":  "child",
			"steer_context": "bad",
		})
		if err == nil {
			t.Fatal("expected error for malformed steer_context even without steer_condition")
		}
		if !strings.Contains(err.Error(), "invalid") {
			t.Errorf("error = %q, want message to call out invalid steer_context", err.Error())
		}
		if !strings.Contains(err.Error(), "\"bad\"") {
			t.Errorf("error = %q, want message to include the raw invalid value %q", err.Error(), "bad")
		}
	})
}

// TestParseManagerLoopConfig_UnprefixedWinsOverPrefixed verifies that when an
// attr is present in both the unprefixed (v0.22.0+) and legacy "manager.*"
// forms, the unprefixed value is used. This matters for migrated pipelines
// that may carry leftover "manager.*" attrs — the new contract takes priority.
func TestParseManagerLoopConfig_UnprefixedWinsOverPrefixed(t *testing.T) {
	cfg, err := parseManagerLoopConfig(map[string]string{
		"subgraph_ref":          "child",
		"poll_interval":         "5s",
		"manager.poll_interval": "99s",
		"max_cycles":            "3",
		"manager.max_cycles":    "999",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.pollInterval != 5*time.Second {
		t.Errorf("pollInterval = %v, want 5s (unprefixed must win)", cfg.pollInterval)
	}
	if cfg.maxCycles != 3 {
		t.Errorf("maxCycles = %d, want 3 (unprefixed must win)", cfg.maxCycles)
	}
}

// TestManagerAttr_EmptyStringPrecedence pins the comma-ok behavior of
// managerAttr: an explicit empty string on the unprefixed key must win over
// a non-empty legacy `manager.*` value. This was the issue #173 footgun —
// the prior zero-value check silently fell through to the legacy prefix,
// letting authors "accidentally" resurrect legacy values they thought they
// had cleared.
func TestManagerAttr_EmptyStringPrecedence(t *testing.T) {
	t.Run("explicit empty unprefixed beats non-empty legacy", func(t *testing.T) {
		attrs := map[string]string{
			"poll_interval":         "",
			"manager.poll_interval": "60s",
		}
		if got := managerAttr(attrs, "poll_interval"); got != "" {
			t.Errorf("managerAttr = %q, want %q (explicit empty must win over legacy)", got, "")
		}
	})

	t.Run("missing entirely returns empty", func(t *testing.T) {
		attrs := map[string]string{
			"subgraph_ref": "child",
		}
		if got := managerAttr(attrs, "poll_interval"); got != "" {
			t.Errorf("managerAttr = %q, want %q (missing key)", got, "")
		}
	})

	t.Run("only legacy present is returned", func(t *testing.T) {
		attrs := map[string]string{
			"manager.poll_interval": "60s",
		}
		if got := managerAttr(attrs, "poll_interval"); got != "60s" {
			t.Errorf("managerAttr = %q, want %q (legacy fallback)", got, "60s")
		}
	})

	t.Run("non-empty unprefixed wins over legacy", func(t *testing.T) {
		attrs := map[string]string{
			"poll_interval":         "5s",
			"manager.poll_interval": "60s",
		}
		if got := managerAttr(attrs, "poll_interval"); got != "5s" {
			t.Errorf("managerAttr = %q, want %q (unprefixed wins)", got, "5s")
		}
	})
}
