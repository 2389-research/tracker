// ABOUTME: Tests for the pipeline execution engine covering edge selection, retries, goal gates, and checkpoints.
// ABOUTME: Uses configurable test handlers and both programmatic graphs and parsed DOT files.
package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// testHandler is a configurable stub handler for engine tests.
type testHandler struct {
	name      string
	executeFn func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error)
}

func (h *testHandler) Name() string { return h.name }
func (h *testHandler) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
	return h.executeFn(ctx, node, pctx)
}

// newTestRegistry creates a registry with all shape handlers returning success by default.
func newTestRegistry() *HandlerRegistry {
	reg := NewHandlerRegistry()
	defaultFn := func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		return Outcome{Status: OutcomeSuccess}, nil
	}
	for _, name := range []string{"start", "exit", "codergen", "wait.human", "conditional", "parallel", "parallel.fan_in", "tool"} {
		n := name
		reg.Register(&testHandler{name: n, executeFn: defaultFn})
	}
	return reg
}

func TestEngineSimplePipeline(t *testing.T) {
	dot, err := os.ReadFile("testdata/simple.dot")
	if err != nil {
		t.Fatalf("read simple.dot: %v", err)
	}
	g, err := ParseDOT(string(dot))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	reg := newTestRegistry()
	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("expected success, got %q", result.Status)
	}
	if len(result.CompletedNodes) < 3 {
		t.Errorf("expected at least 3 completed nodes, got %d: %v", len(result.CompletedNodes), result.CompletedNodes)
	}
}

func TestEngineDiamondPipeline(t *testing.T) {
	dot, err := os.ReadFile("testdata/diamond.dot")
	if err != nil {
		t.Fatalf("read diamond.dot: %v", err)
	}
	g, err := ParseDOT(string(dot))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	reg := newTestRegistry()
	// The conditional handler sets outcome=success so the "pass" path is taken.
	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:         OutcomeSuccess,
				ContextUpdates: map[string]string{"outcome": "success"},
			}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("expected success, got %q", result.Status)
	}
	// Should have: start, check, pass_path, done = 4 nodes
	if len(result.CompletedNodes) < 4 {
		t.Errorf("expected at least 4 completed nodes, got %d: %v", len(result.CompletedNodes), result.CompletedNodes)
	}
}

func TestEngineEdgeSelectionByCondition(t *testing.T) {
	g := NewGraph("cond_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "a", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "b", Shape: "box", Label: "B"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "a", Condition: "route=alpha"})
	g.AddEdge(&Edge{From: "s", To: "b", Condition: "route=beta"})
	g.AddEdge(&Edge{From: "a", To: "end"})
	g.AddEdge(&Edge{From: "b", To: "end"})

	reg := newTestRegistry()
	// Start handler sets route=beta to route to B.
	reg.Register(&testHandler{
		name: "start",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:         OutcomeSuccess,
				ContextUpdates: map[string]string{"route": "beta"},
			}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	// Verify B was visited but not A.
	completedSet := make(map[string]bool)
	for _, n := range result.CompletedNodes {
		completedSet[n] = true
	}
	if !completedSet["b"] {
		t.Error("expected node 'b' to be completed (condition route=beta)")
	}
	if completedSet["a"] {
		t.Error("expected node 'a' to NOT be completed")
	}
}

func TestEngineEdgeSelectionByPreferredLabel(t *testing.T) {
	g := NewGraph("label_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "a", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "b", Shape: "box", Label: "B"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "a", Label: "left"})
	g.AddEdge(&Edge{From: "s", To: "b", Label: "right"})
	g.AddEdge(&Edge{From: "a", To: "end"})
	g.AddEdge(&Edge{From: "b", To: "end"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "start",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:         OutcomeSuccess,
				PreferredLabel: "right",
			}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	completedSet := make(map[string]bool)
	for _, n := range result.CompletedNodes {
		completedSet[n] = true
	}
	if !completedSet["b"] {
		t.Error("expected node 'b' to be completed via preferred label 'right'")
	}
	if completedSet["a"] {
		t.Error("expected node 'a' to NOT be completed")
	}
}

func TestEngineEdgeSelectionByWeight(t *testing.T) {
	g := NewGraph("weight_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "lo", Shape: "box", Label: "Low"})
	g.AddNode(&Node{ID: "hi", Shape: "box", Label: "High"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "lo", Attrs: map[string]string{"weight": "1"}})
	g.AddEdge(&Edge{From: "s", To: "hi", Attrs: map[string]string{"weight": "10"}})
	g.AddEdge(&Edge{From: "lo", To: "end"})
	g.AddEdge(&Edge{From: "hi", To: "end"})

	reg := newTestRegistry()
	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	completedSet := make(map[string]bool)
	for _, n := range result.CompletedNodes {
		completedSet[n] = true
	}
	if !completedSet["hi"] {
		t.Error("expected node 'hi' to be completed (higher weight)")
	}
	if completedSet["lo"] {
		t.Error("expected node 'lo' to NOT be completed")
	}
}

func TestEngineRetryLogic(t *testing.T) {
	g := NewGraph("retry_test")
	g.Attrs["default_max_retry"] = "3"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "flaky", Shape: "box", Label: "Flaky"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "flaky"})
	g.AddEdge(&Edge{From: "flaky", To: "end"})

	reg := newTestRegistry()
	var mu sync.Mutex
	attempts := 0
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			attempts++
			current := attempts
			mu.Unlock()
			if current < 3 {
				return Outcome{Status: OutcomeRetry}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("expected success after retries, got %q", result.Status)
	}
}

func TestEngineRetryExhausted(t *testing.T) {
	g := NewGraph("retry_exhaust_test")
	g.Attrs["default_max_retry"] = "2"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "stuck", Shape: "box", Label: "Stuck"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "stuck"})
	g.AddEdge(&Edge{From: "stuck", To: "end"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{Status: OutcomeRetry}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != OutcomeFail {
		t.Errorf("expected fail after retries exhausted, got %q", result.Status)
	}
}

func TestEngineHandlerError(t *testing.T) {
	g := NewGraph("error_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "bad", Shape: "box", Label: "Bad"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "bad"})
	g.AddEdge(&Edge{From: "bad", To: "end"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{}, fmt.Errorf("handler exploded")
		},
	})

	engine := NewEngine(g, reg)
	_, err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from handler to propagate")
	}
}

func TestEngineContextCancellation(t *testing.T) {
	g := NewGraph("cancel_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "slow", Shape: "box", Label: "Slow"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "slow"})
	g.AddEdge(&Edge{From: "slow", To: "end"})

	ctx, cancel := context.WithCancel(context.Background())

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			cancel()
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	engine := NewEngine(g, reg)
	_, err := engine.Run(ctx)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestEngineEventEmission(t *testing.T) {
	g := NewGraph("event_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "end"})

	reg := newTestRegistry()

	var mu sync.Mutex
	var events []PipelineEventType
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		mu.Lock()
		events = append(events, evt.Type)
		mu.Unlock()
	})

	engine := NewEngine(g, reg, WithPipelineEventHandler(handler))
	_, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	hasStarted := false
	hasCompleted := false
	for _, e := range events {
		if e == EventPipelineStarted {
			hasStarted = true
		}
		if e == EventPipelineCompleted {
			hasCompleted = true
		}
	}
	if !hasStarted {
		t.Error("expected EventPipelineStarted to be emitted")
	}
	if !hasCompleted {
		t.Error("expected EventPipelineCompleted to be emitted")
	}
}

func TestEngineGoalGate(t *testing.T) {
	g := NewGraph("goal_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "critical", Shape: "box", Label: "Critical", Attrs: map[string]string{"goal_gate": "true"}})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "critical"})
	g.AddEdge(&Edge{From: "critical", To: "end"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{Status: OutcomeFail}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != OutcomeFail {
		t.Errorf("expected fail for goal gate node failure, got %q", result.Status)
	}
}

func TestEngineCheckpointResume(t *testing.T) {
	g := NewGraph("resume_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "step1", Shape: "box", Label: "Step 1"})
	g.AddNode(&Node{ID: "step2", Shape: "box", Label: "Step 2"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "step1"})
	g.AddEdge(&Edge{From: "step1", To: "step2"})
	g.AddEdge(&Edge{From: "step2", To: "end"})

	dir := t.TempDir()
	cpPath := filepath.Join(dir, "cp.json")

	// Pre-create a checkpoint that has s and step1 already completed, sitting at step2.
	cp := &Checkpoint{
		RunID:          "resume-run",
		CurrentNode:    "step2",
		CompletedNodes: []string{"s", "step1"},
		RetryCounts:    map[string]int{},
		Context:        map[string]string{"from_step1": "data"},
	}
	if err := SaveCheckpoint(cp, cpPath); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	reg := newTestRegistry()
	var mu sync.Mutex
	executedNodes := []string{}
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			executedNodes = append(executedNodes, node.ID)
			mu.Unlock()
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	engine := NewEngine(g, reg, WithCheckpointPath(cpPath))
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("expected success, got %q", result.Status)
	}

	mu.Lock()
	defer mu.Unlock()

	// step1 should NOT have been re-executed.
	for _, n := range executedNodes {
		if n == "step1" {
			t.Error("step1 should not have been re-executed on resume")
		}
	}

	// Verify context was restored from checkpoint.
	if result.Context["from_step1"] != "data" {
		t.Error("expected context to be restored from checkpoint")
	}
}

func TestEngineWithStylesheet(t *testing.T) {
	g := NewGraph("style_test")
	g.Attrs["model_stylesheet"] = `* { llm_model: gpt-4; } #special { llm_model: claude-sonnet; }`
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "special", Shape: "box", Label: "Special"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "special"})
	g.AddEdge(&Edge{From: "special", To: "end"})

	reg := newTestRegistry()
	var capturedAttrs map[string]string
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			// Capture node attrs at execution time.
			capturedAttrs = make(map[string]string)
			for k, v := range node.Attrs {
				capturedAttrs[k] = v
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	engine := NewEngine(g, reg, WithStylesheetResolution(true))
	_, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	if capturedAttrs["llm_model"] != "claude-sonnet" {
		t.Errorf("expected llm_model=claude-sonnet from stylesheet, got %q", capturedAttrs["llm_model"])
	}
}

func TestEngineEdgeSelectionBySuggestedIDs(t *testing.T) {
	g := NewGraph("suggested_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "decide", Shape: "diamond", Label: "Decide"})
	g.AddNode(&Node{ID: "alpha", Shape: "box", Label: "Alpha"})
	g.AddNode(&Node{ID: "beta", Shape: "box", Label: "Beta"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "decide"})
	g.AddEdge(&Edge{From: "decide", To: "alpha", Label: "a"})
	g.AddEdge(&Edge{From: "decide", To: "beta", Label: "b"})
	g.AddEdge(&Edge{From: "alpha", To: "end"})
	g.AddEdge(&Edge{From: "beta", To: "end"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:             OutcomeSuccess,
				SuggestedNextNodes: []string{"beta"},
			}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	foundBeta := false
	for _, nodeID := range result.CompletedNodes {
		if nodeID == "beta" {
			foundBeta = true
		}
	}
	if !foundBeta {
		t.Errorf("expected 'beta' via suggested IDs, completed: %v", result.CompletedNodes)
	}
}

func TestEngineNoEdgesFromNode(t *testing.T) {
	g := NewGraph("deadend_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "dead", Shape: "box", Label: "Dead End"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	// s -> dead, but dead has no outgoing edges and is not exit.
	g.AddEdge(&Edge{From: "s", To: "dead"})
	// end is unreachable but exists in graph.

	reg := newTestRegistry()
	engine := NewEngine(g, reg)
	_, err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for dead-end non-exit node")
	}
}
