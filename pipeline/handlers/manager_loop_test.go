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
