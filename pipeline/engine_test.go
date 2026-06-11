// ABOUTME: Tests for the pipeline execution engine covering edge selection, retries, goal gates, and checkpoints.
// ABOUTME: Uses configurable test handlers and both programmatic graphs and parsed DOT files.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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
		return Outcome{Status: string(OutcomeSuccess)}, nil
	}
	for _, name := range []string{"start", "exit", "codergen", "wait.human", "conditional", "parallel", "parallel.fan_in", "tool"} {
		n := name
		reg.Register(&testHandler{name: n, executeFn: defaultFn})
	}
	return reg
}

// newTestRegistryWithOutcomes creates a registry where each node returns
// the specified outcome. Nodes not in the map return success.
func newTestRegistryWithOutcomes(outcomes map[string]Outcome) *HandlerRegistry {
	reg := NewHandlerRegistry()
	for _, name := range []string{"start", "exit", "codergen", "wait.human", "conditional", "parallel", "parallel.fan_in", "tool"} {
		n := name
		reg.Register(&testHandler{name: n, executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if o, ok := outcomes[node.ID]; ok {
				return o, nil
			}
			return Outcome{Status: string(OutcomeSuccess)}, nil
		}})
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

// TestEngine_StampsBundleIdentityOnEmittedEvents pins the contract that
// WithBundleIdentity stamps every PipelineEvent the engine emits. This is
// how `.dipx` bundle provenance reaches every line of activity.jsonl.
func TestEngine_StampsBundleIdentityOnEmittedEvents(t *testing.T) {
	g := NewGraph("bundle_id_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "end"})

	reg := newTestRegistry()

	// Guard against future concurrent emission paths: parallel/manager_loop
	// handlers can fire events from goroutines, and the engine reserves the
	// right to do so. This simple graph doesn't trigger that today, but the
	// mutex keeps the test correct under `go test -race` if it ever does.
	var capturedMu sync.Mutex
	var captured []PipelineEvent
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		capturedMu.Lock()
		defer capturedMu.Unlock()
		captured = append(captured, evt)
	})

	engine := NewEngine(g, reg,
		WithBundleIdentity("sha256:abcdef0123"),
		WithPipelineEventHandler(handler),
	)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("expected success, got %q", result.Status)
	}
	capturedMu.Lock()
	defer capturedMu.Unlock()
	if len(captured) == 0 {
		t.Fatal("no events captured — engine should at minimum emit started/completed")
	}
	for _, evt := range captured {
		if evt.BundleIdentity != "sha256:abcdef0123" {
			t.Errorf("event %s has wrong BundleIdentity: got %q want %q", evt.Type, evt.BundleIdentity, "sha256:abcdef0123")
		}
	}
}

// TestEngine_BundleIdentityEmptyByDefault pins the no-op contract: an
// engine constructed without WithBundleIdentity emits events whose
// BundleIdentity is the empty string, so plain .dip runs do not leak a
// stray "bundle_identity" field into activity.jsonl.
func TestEngine_BundleIdentityEmptyByDefault(t *testing.T) {
	g := NewGraph("bundle_id_default_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "end"})

	reg := newTestRegistry()

	// See TestEngine_StampsBundleIdentityOnEmittedEvents for rationale.
	var capturedMu sync.Mutex
	var captured []PipelineEvent
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		capturedMu.Lock()
		defer capturedMu.Unlock()
		captured = append(captured, evt)
	})

	engine := NewEngine(g, reg, WithPipelineEventHandler(handler))
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	capturedMu.Lock()
	defer capturedMu.Unlock()
	if len(captured) == 0 {
		t.Fatal("no events captured")
	}
	for _, evt := range captured {
		if evt.BundleIdentity != "" {
			t.Errorf("event %s has non-empty BundleIdentity without WithBundleIdentity: %q", evt.Type, evt.BundleIdentity)
		}
	}
}

// TestEngine_PersistsBundleIdentityToCheckpoint pins the contract that the
// engine's configured bundleIdentity (via WithBundleIdentity) is written
// into Checkpoint.BundleIdentity on every save. This is what Task 15's
// strict-resume verification reads back to fail-fast on bundle drift.
func TestEngine_PersistsBundleIdentityToCheckpoint(t *testing.T) {
	g := NewGraph("bundle_id_persist_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "end"})

	reg := newTestRegistry()
	cpPath := filepath.Join(t.TempDir(), "cp.json")
	engine := NewEngine(g, reg,
		WithCheckpointPath(cpPath),
		WithBundleIdentity("sha256:bundleid_test"),
	)
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	cp, err := LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if cp.BundleIdentity != "sha256:bundleid_test" {
		t.Errorf("BundleIdentity not persisted: got %q want %q", cp.BundleIdentity, "sha256:bundleid_test")
	}
}

// TestEngine_OmitsBundleIdentity_WhenNotSet pins the no-op contract: an
// engine constructed without WithBundleIdentity writes an empty
// BundleIdentity into the checkpoint, so plain .dip runs do not pollute
// checkpoint.json with a stray "bundle_identity" field.
func TestEngine_OmitsBundleIdentity_WhenNotSet(t *testing.T) {
	g := NewGraph("bundle_id_omit_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "end"})

	reg := newTestRegistry()
	cpPath := filepath.Join(t.TempDir(), "cp.json")
	engine := NewEngine(g, reg, WithCheckpointPath(cpPath))
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	cp, err := LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if cp.BundleIdentity != "" {
		t.Errorf("BundleIdentity should be empty when not set: got %q", cp.BundleIdentity)
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
				Status:         string(OutcomeSuccess),
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
				Status:         string(OutcomeSuccess),
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

func TestEngineEdgeSelectionByConditionWithParamsInterpolation(t *testing.T) {
	g := NewGraph("cond_params_test")
	g.Attrs["params.route"] = "beta"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "a", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "b", Shape: "box", Label: "B"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "a", Condition: "route=${params.route}"})
	g.AddEdge(&Edge{From: "s", To: "b", Condition: "route=alpha"})
	g.AddEdge(&Edge{From: "a", To: "end"})
	g.AddEdge(&Edge{From: "b", To: "end"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "start",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:         string(OutcomeSuccess),
				ContextUpdates: map[string]string{"route": "beta"},
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
	if !completedSet["a"] {
		t.Error("expected node 'a' to be completed (condition route=${params.route})")
	}
	if completedSet["b"] {
		t.Error("expected node 'b' to NOT be completed")
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
				Status:         string(OutcomeSuccess),
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
	g.Attrs["default_retry_policy"] = "none"
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
				return Outcome{Status: string(OutcomeRetry)}, nil
			}
			return Outcome{Status: string(OutcomeSuccess)}, nil
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
	g.Attrs["default_retry_policy"] = "none"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "stuck", Shape: "box", Label: "Stuck"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "stuck"})
	g.AddEdge(&Edge{From: "stuck", To: "end"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{Status: string(OutcomeRetry)}, nil
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

// TestEngineHandlerError_ChildUsagePreservedOnError verifies that when a handler
// returns both a non-nil ChildUsage and a non-nil error, executeNode still
// captures ChildUsage into the trace entry. This is required for cancelled
// child runs (e.g. manager_loop cancellation path) to contribute their
// accumulated spend to the parent's AggregateUsage and BudgetGuard rollup.
func TestEngineHandlerError_ChildUsagePreservedOnError(t *testing.T) {
	g := NewGraph("child_usage_error_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "child", Shape: "box", Label: "Child"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "child"})
	g.AddEdge(&Edge{From: "child", To: "end"})

	childUsage := &UsageSummary{
		TotalInputTokens:  100,
		TotalOutputTokens: 50,
		TotalTokens:       150,
		TotalCostUSD:      0.01,
		SessionCount:      1,
		ProviderTotals: map[string]ProviderUsage{
			"anthropic": {InputTokens: 100, OutputTokens: 50, TotalTokens: 150, CostUSD: 0.01, SessionCount: 1},
		},
	}

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:     string(OutcomeFail),
				ChildUsage: childUsage,
			}, fmt.Errorf("handler cancelled: %w", context.Canceled)
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from handler to propagate")
	}

	// Verify ChildUsage is in the trace entry for the "child" node.
	if result == nil {
		t.Fatal("expected non-nil result even on error")
	}
	var childEntry *TraceEntry
	for i := range result.Trace.Entries {
		if result.Trace.Entries[i].NodeID == "child" {
			childEntry = &result.Trace.Entries[i]
			break
		}
	}
	if childEntry == nil {
		t.Fatal("expected trace entry for 'child' node")
	}
	if childEntry.ChildUsage == nil {
		t.Fatal("expected ChildUsage to be preserved in trace entry even when handler returns error")
	}
	if childEntry.ChildUsage.TotalTokens != 150 {
		t.Errorf("expected TotalTokens=150, got %d", childEntry.ChildUsage.TotalTokens)
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
			return Outcome{Status: string(OutcomeSuccess)}, nil
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
	g.AddEdge(&Edge{From: "critical", To: "end", Condition: "ctx.outcome = success"})
	g.AddEdge(&Edge{From: "critical", To: "end", Condition: "ctx.outcome = fail"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{Status: string(OutcomeFail)}, nil
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
			return Outcome{Status: string(OutcomeSuccess)}, nil
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

func TestEngineCheckpointResumeWithFidelityDegradation(t *testing.T) {
	g := NewGraph("fidelity_resume")
	g.Attrs["default_fidelity"] = "summary:medium"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "step1", Shape: "box", Label: "Step 1"})
	g.AddNode(&Node{ID: "step2", Shape: "box", Label: "Step 2"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "step1"})
	g.AddEdge(&Edge{From: "step1", To: "step2"})
	g.AddEdge(&Edge{From: "step2", To: "end"})

	dir := t.TempDir()
	cpPath := filepath.Join(dir, "cp.json")

	// Pre-create checkpoint with context that includes keys that should be
	// dropped at summary:low (which is summary:medium degraded one level).
	cp := &Checkpoint{
		RunID:          "fidelity-run",
		CurrentNode:    "step2",
		CompletedNodes: []string{"s", "step1"},
		RetryCounts:    map[string]int{},
		Context: map[string]string{
			"graph.goal":      "build a widget",
			"outcome":         "success",
			"last_response":   "some output",
			"unrelated_extra": "should be dropped",
		},
	}
	if err := SaveCheckpoint(cp, cpPath); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	reg := newTestRegistry()
	var capturedCtx map[string]string
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			capturedCtx = pctx.Snapshot()
			return Outcome{Status: string(OutcomeSuccess)}, nil
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

	// The graph default fidelity is summary:medium, which degrades to summary:low.
	// In summary:low mode, only graph.goal and completed_summary are kept.
	if capturedCtx["graph.goal"] != "build a widget" {
		t.Errorf("expected graph.goal preserved, got %q", capturedCtx["graph.goal"])
	}

	// summary:low should include a completed_summary key.
	if _, ok := capturedCtx["completed_summary"]; !ok {
		t.Error("expected completed_summary key in degraded context")
	}

	// unrelated_extra should be dropped.
	if val := capturedCtx["unrelated_extra"]; val != "" {
		t.Errorf("expected unrelated_extra to be dropped, got %q", val)
	}
}

func TestEngineAutoCheckpointWithArtifactDir(t *testing.T) {
	g := NewGraph("auto_cp")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "step1", Shape: "box", Label: "Step 1"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "step1"})
	g.AddEdge(&Edge{From: "step1", To: "end"})

	dir := t.TempDir()
	artifactDir := filepath.Join(dir, "runs")

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{Status: string(OutcomeSuccess)}, nil
		},
	})

	engine := NewEngine(g, reg, WithArtifactDir(artifactDir))
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("expected success, got %q", result.Status)
	}

	// A checkpoint.json should have been auto-created in the artifact dir.
	cpPath := filepath.Join(artifactDir, result.RunID, "checkpoint.json")
	if _, err := os.Stat(cpPath); os.IsNotExist(err) {
		t.Fatalf("expected auto-checkpoint at %s, but file does not exist", cpPath)
	}

	// Verify the checkpoint is valid and contains the run ID.
	cp, err := LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatalf("load auto-checkpoint: %v", err)
	}
	if cp.RunID != result.RunID {
		t.Errorf("checkpoint runID = %q, want %q", cp.RunID, result.RunID)
	}
}

func TestEngineMirrorsGraphGoalAndExpandsPrompt(t *testing.T) {
	g := NewGraph("goal_prompt")
	g.Attrs["goal"] = "ship a hello world script"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "plan", Shape: "box", Label: "Plan", Attrs: map[string]string{"prompt": "Plan for $goal"}})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "plan"})
	g.AddEdge(&Edge{From: "plan", To: "end"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			goal, ok := pctx.Get(ContextKeyGoal)
			if !ok || goal != "ship a hello world script" {
				t.Fatalf("graph goal = %q, ok=%v", goal, ok)
			}
			if node.Attrs["prompt"] != "Plan for ship a hello world script" {
				t.Fatalf("prompt = %q", node.Attrs["prompt"])
			}
			return Outcome{Status: string(OutcomeSuccess)}, nil
		},
	})

	engine := NewEngine(g, reg)
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEngineExpandsGraphVariablesInToolCommand(t *testing.T) {
	g := NewGraph("graph_vars")
	g.Attrs["target_name"] = "myapp"
	g.Attrs["source_ref"] = "main"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "tool", Shape: "box", Label: "Tool",
		Attrs: map[string]string{"type": "tool", "tool_command": "echo $target_name $source_ref"}})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "tool"})
	g.AddEdge(&Edge{From: "tool", To: "end"})

	reg := newTestRegistry()
	var capturedCommand string
	var handlerCalled bool
	reg.Register(&testHandler{
		name: "tool",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			handlerCalled = true
			capturedCommand = node.Attrs["tool_command"]
			return Outcome{Status: string(OutcomeSuccess)}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("expected success, got %q", result.Status)
	}
	if !handlerCalled {
		t.Fatal("tool handler was never called")
	}

	expected := "echo myapp main"
	if capturedCommand != expected {
		t.Errorf("tool_command = %q, want %q", capturedCommand, expected)
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
			return Outcome{Status: string(OutcomeSuccess)}, nil
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
				Status:             string(OutcomeSuccess),
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

func TestEngineLoopBackToConditionalNode(t *testing.T) {
	// Regression: when a conditional node (e.g. ValidateBuild) fails and routes
	// to another node that loops back, the engine must re-execute the conditional
	// node instead of skipping it as "completed". The outcome must be preserved
	// for edge selection.
	//
	// Graph: Start -> Validate --(outcome=success)--> End
	//                 Validate --(outcome=fail)-----> Fix -> Validate (loop)
	g := NewGraph("loopback_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "validate", Shape: "parallelogram", Label: "Validate"})
	g.AddNode(&Node{ID: "fix", Shape: "box", Label: "Fix"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "validate"})
	g.AddEdge(&Edge{From: "validate", To: "end", Condition: "outcome=success", Label: "pass"})
	g.AddEdge(&Edge{From: "validate", To: "fix", Condition: "outcome=fail", Label: "fail"})
	g.AddEdge(&Edge{From: "fix", To: "validate"})

	reg := newTestRegistry()

	// Validate fails the first time, succeeds the second.
	var mu sync.Mutex
	validateAttempts := 0
	reg.Register(&testHandler{
		name: "tool",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			validateAttempts++
			attempt := validateAttempts
			mu.Unlock()
			if attempt == 1 {
				return Outcome{Status: string(OutcomeFail)}, nil
			}
			return Outcome{Status: string(OutcomeSuccess)}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("expected success after loop-back, got %q", result.Status)
	}
	if validateAttempts != 2 {
		t.Errorf("expected validate to run twice, ran %d times", validateAttempts)
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

func TestEngineConditionalEdgeMatchesOutcome(t *testing.T) {
	// Verifies that after a handler returns OutcomeSuccess, edges conditioned
	// on outcome=success are selected. This is the basic contract that
	// conditional routing depends on.
	g := NewGraph("cond_edge_basic")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "verify", Shape: "box", Label: "Verify", Handler: "codergen"})
	g.AddNode(&Node{ID: "pass_step", Shape: "box", Label: "PassStep", Handler: "codergen"})
	g.AddNode(&Node{ID: "fail_step", Shape: "box", Label: "FailStep", Handler: "codergen"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "verify"})
	g.AddEdge(&Edge{From: "verify", To: "pass_step", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "verify", To: "fail_step", Condition: "outcome=fail"})
	g.AddEdge(&Edge{From: "pass_step", To: "end"})
	g.AddEdge(&Edge{From: "fail_step", To: "end"})

	reg := newTestRegistry()
	// Override codergen to return success (no ContextUpdates for outcome —
	// the engine should set it automatically from outcome.Status).
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{Status: string(OutcomeSuccess)}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("expected success, got %q", result.Status)
	}
	// The "pass_step" node should have been reached (not fail_step).
	foundPass := false
	foundFail := false
	for _, n := range result.CompletedNodes {
		if n == "pass_step" {
			foundPass = true
		}
		if n == "fail_step" {
			foundFail = true
		}
	}
	if !foundPass {
		t.Errorf("expected 'pass_step' in completed nodes, got %v", result.CompletedNodes)
	}
	if foundFail {
		t.Errorf("did not expect 'fail_step' in completed nodes, got %v", result.CompletedNodes)
	}
}

func TestEngineConditionalEdgeMatchesFail(t *testing.T) {
	// When handler returns OutcomeFail, edges conditioned on outcome=fail
	// should be selected.
	g := NewGraph("cond_edge_fail")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "check", Shape: "box", Label: "Check", Handler: "codergen"})
	g.AddNode(&Node{ID: "ok_step", Shape: "box", Label: "OKStep", Handler: "codergen"})
	g.AddNode(&Node{ID: "nok_step", Shape: "box", Label: "NOKStep", Handler: "codergen"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "ok_step", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "check", To: "nok_step", Condition: "outcome=fail"})
	g.AddEdge(&Edge{From: "ok_step", To: "end"})
	g.AddEdge(&Edge{From: "nok_step", To: "end", Condition: "ctx.outcome = fail"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{Status: string(OutcomeFail)}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("expected completion, got error: %v", err)
	}
	found := false
	for _, n := range result.CompletedNodes {
		if n == "nok_step" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'nok_step' in completed nodes, got %v", result.CompletedNodes)
	}
}

func TestEngineConditionalEdgeDiagnosticOnMismatch(t *testing.T) {
	// All edges have conditions, none match — error should include diagnostic info.
	g := NewGraph("cond_edge_diag")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "step", Shape: "box", Label: "Step", Handler: "codergen"})
	g.AddNode(&Node{ID: "a", Shape: "Msquare", Label: "A"})
	g.AddNode(&Node{ID: "b", Shape: "Msquare", Label: "B"})

	g.AddEdge(&Edge{From: "s", To: "step"})
	// Conditions that won't match "success" status.
	g.AddEdge(&Edge{From: "step", To: "a", Condition: "custom_key=alpha"})
	g.AddEdge(&Edge{From: "step", To: "b", Condition: "custom_key=beta"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{Status: string(OutcomeSuccess)}, nil
		},
	})

	engine := NewEngine(g, reg)
	_, err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when no edges match")
	}
	if !strings.Contains(err.Error(), "no matching edges") {
		t.Errorf("expected 'no matching edges' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "custom_key=alpha") {
		t.Errorf("expected condition text in diagnostic, got: %v", err)
	}
}

func TestEngineStrictFailureEdgeStopsPipeline(t *testing.T) {
	// When a node fails and all outgoing edges are unconditional,
	// the pipeline should stop instead of silently continuing.
	g := NewGraph("strict_fail")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "setup", Shape: "box", Label: "Setup", Handler: "codergen"})
	g.AddNode(&Node{ID: "next", Shape: "box", Label: "Next", Handler: "codergen"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "Done"})
	g.StartNode = "s"
	g.ExitNode = "end"
	g.AddEdge(&Edge{From: "s", To: "setup"})
	g.AddEdge(&Edge{From: "setup", To: "next"}) // unconditional — no failure handling
	g.AddEdge(&Edge{From: "next", To: "end"})

	reg := newTestRegistryWithOutcomes(map[string]Outcome{
		"s":     {Status: string(OutcomeSuccess)},
		"setup": {Status: string(OutcomeFail)}, // Setup fails
		"next":  {Status: string(OutcomeSuccess)},
	})
	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())

	if err == nil {
		t.Fatal("expected error when node fails with no conditional edges")
	}
	if !strings.Contains(err.Error(), "setup") {
		t.Errorf("expected error to mention 'setup', got: %v", err)
	}
	if result.Status != OutcomeFail {
		t.Errorf("expected fail status, got %q", result.Status)
	}
	// "next" should NOT have been reached.
	for _, id := range result.CompletedNodes {
		if id == "next" {
			t.Error("'next' should not have been reached after setup failure")
		}
	}
}

func TestEngineStrictFailureEdgeAllowsConditionalRouting(t *testing.T) {
	// When a node fails but has conditional edges, the pipeline should
	// continue via the matching edge (not stop).
	g := NewGraph("strict_fail_conditional")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "check", Shape: "box", Label: "Check", Handler: "codergen"})
	g.AddNode(&Node{ID: "ok", Shape: "box", Label: "OK", Handler: "codergen"})
	g.AddNode(&Node{ID: "nok", Shape: "box", Label: "NOK", Handler: "codergen"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "Done"})
	g.StartNode = "s"
	g.ExitNode = "end"
	g.AddEdge(&Edge{From: "s", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "ok", Condition: "ctx.outcome = success"})
	g.AddEdge(&Edge{From: "check", To: "nok", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "ok", To: "end"})
	g.AddEdge(&Edge{From: "nok", To: "end", Condition: "ctx.outcome = success"})

	reg := newTestRegistryWithOutcomes(map[string]Outcome{
		"s":     {Status: string(OutcomeSuccess)},
		"check": {Status: string(OutcomeFail)}, // fails, routes to nok via condition
		"nok":   {Status: string(OutcomeSuccess)},
	})
	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("expected success, got %q", result.Status)
	}
	// "nok" should have been reached.
	found := false
	for _, id := range result.CompletedNodes {
		if id == "nok" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'nok' to be reached via conditional failure edge")
	}
}

func TestEngineResumePreservesEdgeContext(t *testing.T) {
	// On checkpoint resume, completed nodes should route correctly using
	// the preserved context (not cleared hints).
	g := NewGraph("resume_edge")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "a", Shape: "box", Label: "A", Handler: "codergen"})
	g.AddNode(&Node{ID: "b", Shape: "box", Label: "B", Handler: "codergen"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "a"})
	g.AddEdge(&Edge{From: "a", To: "b", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "a", To: "end", Condition: "outcome=fail"})
	g.AddEdge(&Edge{From: "b", To: "end"})

	dir := t.TempDir()
	cpPath := filepath.Join(dir, "cp.json")

	// Create a checkpoint where "s" and "a" are completed, with outcome=success.
	cp := &Checkpoint{
		CompletedNodes: []string{"s", "a"},
		CurrentNode:    "s",
		Context: map[string]string{
			"outcome": "success",
		},
	}
	cpData, _ := json.Marshal(cp)
	os.WriteFile(cpPath, cpData, 0644)

	reg := newTestRegistry()
	bExecuted := false
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "b" {
				bExecuted = true
			}
			return Outcome{Status: string(OutcomeSuccess)}, nil
		},
	})

	engine := NewEngine(g, reg, WithCheckpointPath(cpPath))
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("expected success on resume, got error: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("expected success, got %q", result.Status)
	}
	if !bExecuted {
		t.Error("expected node B to execute after resuming through A's conditional edge")
	}
}

func TestEngineResumeLoopingPipelineDoesNotInfiniteLoop(t *testing.T) {
	// Regression test: a looping pipeline (Plan → Implement → Gate → Plan)
	// where all loop nodes are marked completed on resume should not
	// cycle through the skip path forever. The engine should detect the
	// cycle and re-execute the loop.
	g := NewGraph("loop_resume")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "plan", Shape: "box", Label: "Plan", Handler: "codergen"})
	g.AddNode(&Node{ID: "impl", Shape: "box", Label: "Implement", Handler: "codergen"})
	g.AddNode(&Node{ID: "gate", Shape: "diamond", Label: "Gate", Handler: "conditional",
		Attrs: map[string]string{}})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "plan"})
	g.AddEdge(&Edge{From: "plan", To: "impl"})
	g.AddEdge(&Edge{From: "impl", To: "gate"})
	g.AddEdge(&Edge{From: "gate", To: "plan", Label: "retry"})
	g.AddEdge(&Edge{From: "gate", To: "end", Label: "done"})

	dir := t.TempDir()
	cpPath := filepath.Join(dir, "cp.json")

	// Checkpoint: all loop nodes completed, sitting at start. This
	// simulates a ctrl-c during a loop iteration where the checkpoint
	// was saved at the beginning of the graph traversal.
	cp := &Checkpoint{
		CompletedNodes: []string{"s", "plan", "impl", "gate"},
		CurrentNode:    "s",
		Context: map[string]string{
			ContextKeyPreferredLabel: "retry",
		},
	}
	cpData, _ := json.Marshal(cp)
	os.WriteFile(cpPath, cpData, 0644)

	reg := newTestRegistry()
	var mu sync.Mutex
	executedNodes := []string{}
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			executedNodes = append(executedNodes, node.ID)
			mu.Unlock()
			// On re-execution, gate should route to "done".
			pctx.Set(ContextKeyPreferredLabel, "done")
			return Outcome{Status: string(OutcomeSuccess)}, nil
		},
	})
	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			executedNodes = append(executedNodes, node.ID)
			mu.Unlock()
			return Outcome{Status: string(OutcomeSuccess)}, nil
		},
	})

	// Use a context with timeout to prevent infinite loop from hanging the test.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	engine := NewEngine(g, reg, WithCheckpointPath(cpPath))
	result, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("engine run failed (may have infinite-looped): %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("expected success, got %q", result.Status)
	}

	mu.Lock()
	defer mu.Unlock()

	// The loop nodes should have been re-executed after cycle detection.
	if len(executedNodes) == 0 {
		t.Error("expected loop nodes to be re-executed, but none were")
	}
}

func TestEngine_EmitsCostUpdatedAfterEachNode(t *testing.T) {
	// 3-node linear graph: start -> middle -> end
	g := NewGraph("cost_updated_test")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "middle", Shape: "box", Label: "Middle"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "start", To: "middle"})
	g.AddEdge(&Edge{From: "middle", To: "end"})

	// All three nodes return SessionStats so AggregateUsage returns non-nil.
	nodeStats := &SessionStats{
		InputTokens:  100,
		OutputTokens: 50,
		TotalTokens:  150,
		CostUSD:      0.01,
		Provider:     "test-provider",
	}

	reg := NewHandlerRegistry()
	for _, name := range []string{"start", "exit", "codergen", "wait.human", "conditional", "parallel", "parallel.fan_in", "tool"} {
		n := name
		reg.Register(&testHandler{name: n, executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{Status: string(OutcomeSuccess), Stats: nodeStats}, nil
		}})
	}

	var mu sync.Mutex
	var costEvents []PipelineEvent
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		if evt.Type == EventCostUpdated {
			mu.Lock()
			costEvents = append(costEvents, evt)
			mu.Unlock()
		}
	})

	engine := NewEngine(g, reg, WithPipelineEventHandler(handler))
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("expected success, got %q", result.Status)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(costEvents) < 3 {
		t.Errorf("expected at least 3 EventCostUpdated events, got %d", len(costEvents))
	}

	// Each event must carry a non-nil Cost snapshot.
	for i, evt := range costEvents {
		if evt.Cost == nil {
			t.Errorf("event %d has nil Cost", i)
			continue
		}
		if evt.Cost.TotalTokens <= 0 {
			t.Errorf("event %d has TotalTokens=%d, want > 0", i, evt.Cost.TotalTokens)
		}
	}

	// TotalTokens must be monotonically non-decreasing.
	for i := 1; i < len(costEvents); i++ {
		prev := costEvents[i-1].Cost
		curr := costEvents[i].Cost
		if prev == nil || curr == nil {
			continue
		}
		if curr.TotalTokens < prev.TotalTokens {
			t.Errorf("event %d TotalTokens=%d < event %d TotalTokens=%d (not monotonically non-decreasing)",
				i, curr.TotalTokens, i-1, prev.TotalTokens)
		}
	}

	// Final event must have cumulative cost >= 3 * 0.01 = 0.03 (with tolerance).
	if len(costEvents) > 0 {
		last := costEvents[len(costEvents)-1]
		if last.Cost == nil {
			t.Fatal("last EventCostUpdated has nil Cost")
		}
		const wantMinCost = 0.03 - 1e-9
		if last.Cost.TotalCostUSD < wantMinCost {
			t.Errorf("final Cost.TotalCostUSD=%.6f, want >= %.6f", last.Cost.TotalCostUSD, wantMinCost)
		}
		if last.Cost.WallElapsed <= 0 {
			t.Errorf("final Cost.WallElapsed=%v, want > 0", last.Cost.WallElapsed)
		}
	}
}

func TestEngine_HaltsOnBudgetBreach(t *testing.T) {
	// 5-node linear graph: start -> n1 -> n2 -> n3 -> end
	// Each handler returns 300 tokens. Guard: MaxTotalTokens=700.
	// After node n1 completes we have 600 total (start + n1), after n2 we have 900.
	// The check fires after emitCostUpdate in advanceToNextNode, so halt occurs
	// after n2's trace entry is committed (total 900 > 700).
	g := NewGraph("budget_halt_test")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "n1", Shape: "box", Label: "N1"})
	g.AddNode(&Node{ID: "n2", Shape: "box", Label: "N2"})
	g.AddNode(&Node{ID: "n3", Shape: "box", Label: "N3"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "start", To: "n1"})
	g.AddEdge(&Edge{From: "n1", To: "n2"})
	g.AddEdge(&Edge{From: "n2", To: "n3"})
	g.AddEdge(&Edge{From: "n3", To: "end"})

	nodeStats := &SessionStats{
		InputTokens:  200,
		OutputTokens: 100,
		TotalTokens:  300,
		CostUSD:      0.01,
		Provider:     "test-provider",
	}

	reg := NewHandlerRegistry()
	for _, name := range []string{"start", "exit", "codergen", "wait.human", "conditional", "parallel", "parallel.fan_in", "tool"} {
		n := name
		reg.Register(&testHandler{name: n, executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{Status: string(OutcomeSuccess), Stats: nodeStats}, nil
		}})
	}

	var mu sync.Mutex
	var budgetEvents []PipelineEvent
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		if evt.Type == EventBudgetExceeded {
			mu.Lock()
			budgetEvents = append(budgetEvents, evt)
			mu.Unlock()
		}
	})

	guard := NewBudgetGuard(BudgetLimits{MaxTotalTokens: 700})
	engine := NewEngine(g, reg,
		WithPipelineEventHandler(handler),
		WithBudgetGuard(guard),
	)

	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine.Run returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if result.Status != OutcomeBudgetExceeded {
		t.Errorf("result.Status = %q, want %q", result.Status, OutcomeBudgetExceeded)
	}

	if len(result.BudgetLimitsHit) != 1 || result.BudgetLimitsHit[0] != "tokens" {
		t.Errorf("BudgetLimitsHit = %v, want [\"tokens\"]", result.BudgetLimitsHit)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(budgetEvents) == 0 {
		t.Error("expected at least one EventBudgetExceeded, got none")
	}

	// Pipeline must not have run to completion — n3 and end should not be completed.
	completedSet := make(map[string]bool)
	for _, n := range result.CompletedNodes {
		completedSet[n] = true
	}
	if completedSet["n3"] || completedSet["end"] {
		t.Errorf("pipeline should have halted before n3/end, CompletedNodes=%v", result.CompletedNodes)
	}
}

// TestEngine_HaltsOnBudgetBreachDuringRetry verifies that the BudgetGuard fires
// on the retry path, not just on normal node advancement. The node returns
// OutcomeRetry twice, each attempt emitting 500 tokens. The guard limit is 600,
// so after the first retry's trace entry is committed (total 500 from the initial
// run + 500 from the retry attempt = 1000 > 600) the pipeline must halt.
func TestEngine_HaltsOnBudgetBreachDuringRetry(t *testing.T) {
	g := NewGraph("retry_budget_halt_test")
	g.Attrs["default_max_retry"] = "5"
	g.Attrs["default_retry_policy"] = "none"
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "retrying", Shape: "box", Label: "Retrying"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "start", To: "retrying"})
	g.AddEdge(&Edge{From: "retrying", To: "end"})

	nodeStats := &SessionStats{
		InputTokens:  350,
		OutputTokens: 150,
		TotalTokens:  500,
		CostUSD:      0.01,
		Provider:     "test-provider",
	}

	reg := NewHandlerRegistry()
	for _, name := range []string{"start", "exit", "codergen", "wait.human", "conditional", "parallel", "parallel.fan_in", "tool"} {
		n := name
		reg.Register(&testHandler{name: n, executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{Status: string(OutcomeRetry), Stats: nodeStats}, nil
		}})
	}

	var mu sync.Mutex
	var budgetEvents []PipelineEvent
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		if evt.Type == EventBudgetExceeded {
			mu.Lock()
			budgetEvents = append(budgetEvents, evt)
			mu.Unlock()
		}
	})

	// Guard: 600 tokens. First attempt on "retrying" adds 500 tokens.
	// After the retry trace entry is committed (attempt 1 = 500 tokens total),
	// the budget check fires (500 < 600 still ok). On the second retry attempt
	// another 500 is added (total 1000 > 600), budget must trip.
	guard := NewBudgetGuard(BudgetLimits{MaxTotalTokens: 600})
	engine := NewEngine(g, reg,
		WithPipelineEventHandler(handler),
		WithBudgetGuard(guard),
	)

	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine.Run returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if result.Status != OutcomeBudgetExceeded {
		t.Errorf("result.Status = %q, want %q", result.Status, OutcomeBudgetExceeded)
	}

	if len(result.BudgetLimitsHit) != 1 || result.BudgetLimitsHit[0] != "tokens" {
		t.Errorf("BudgetLimitsHit = %v, want [\"tokens\"]", result.BudgetLimitsHit)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(budgetEvents) == 0 {
		t.Error("expected at least one EventBudgetExceeded, got none")
	}
}

func TestEngine_UnknownOutcomeFailsNode(t *testing.T) {
	g := NewGraph("unknown_outcome_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "bogus", Shape: "box", Label: "Bogus"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "bogus"})
	g.AddEdge(&Edge{From: "bogus", To: "end"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{Status: "totally_bogus_status"}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())

	if err == nil && result.Status == OutcomeSuccess {
		t.Fatal("expected unknown outcome to NOT be treated as success")
	}
}

// TestEngine_OverrideEdge_SetsSticky pins the flip-point: when the engine
// traverses an Edge.Override-marked edge from a wait.human gate, the
// resulting EngineResult carries Status=OutcomeValidationOverridden and a
// single OverrideDetail with the expected gate/label/actor.
func TestEngine_OverrideEdge_SetsSticky(t *testing.T) {
	g := NewGraph("override_sticky")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "gate", Shape: "hexagon", Label: "Gate", Attrs: map[string]string{"label": "Accept?"}})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "gate"})
	g.AddEdge(&Edge{From: "gate", To: "end", Label: "accept", Override: true})
	g.AddEdge(&Edge{From: "gate", To: "s", Label: "retry"})

	reg := newTestRegistry()
	// Override the wait.human handler to return PreferredLabel=accept plus
	// OverrideActor=human, simulating what HumanHandler does after the
	// interviewer commits an override edge.
	reg.Register(&testHandler{
		name: "wait.human",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:         string(OutcomeSuccess),
				PreferredLabel: "accept",
				OverrideActor:  ActorHuman,
			}, nil
		},
	})

	var capturedMu sync.Mutex
	var sawEvent bool
	var capturedDetail *OverrideDetail
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		capturedMu.Lock()
		defer capturedMu.Unlock()
		if evt.Type == EventValidationOverridden {
			sawEvent = true
			if evt.Override != nil {
				d := *evt.Override
				capturedDetail = &d
			}
		}
	})

	engine := NewEngine(g, reg, WithPipelineEventHandler(handler))
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	if result.Status != OutcomeValidationOverridden {
		t.Errorf("Status = %q, want %q", result.Status, OutcomeValidationOverridden)
	}
	if len(result.ValidationOverrides) != 1 {
		t.Fatalf("ValidationOverrides length = %d, want 1: %+v", len(result.ValidationOverrides), result.ValidationOverrides)
	}
	got := result.ValidationOverrides[0]
	if got.GateNodeID != "gate" {
		t.Errorf("GateNodeID = %q, want gate", got.GateNodeID)
	}
	if got.Label != "accept" {
		t.Errorf("Label = %q, want accept", got.Label)
	}
	if got.Actor != ActorHuman {
		t.Errorf("Actor = %q, want %q", got.Actor, ActorHuman)
	}
	if got.Timestamp.IsZero() {
		t.Error("Timestamp should be populated")
	}
	capturedMu.Lock()
	defer capturedMu.Unlock()
	if !sawEvent {
		t.Error("expected EventValidationOverridden to be emitted")
	}
	if capturedDetail == nil || capturedDetail.GateNodeID != "gate" || capturedDetail.Label != "accept" {
		t.Errorf("event Override payload mismatch: %+v", capturedDetail)
	}
}

// TestEngine_OverrideEdge_NoActorDefaultsUnknown verifies that an override
// edge traversed without an Outcome.OverrideActor (third-party / future
// handler) records Actor=ActorUnknown rather than empty string.
func TestEngine_OverrideEdge_NoActorDefaultsUnknown(t *testing.T) {
	g := NewGraph("override_unknown_actor")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "gate", Shape: "hexagon", Label: "Gate"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "gate"})
	g.AddEdge(&Edge{From: "gate", To: "end", Label: "accept", Override: true})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "wait.human",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:         string(OutcomeSuccess),
				PreferredLabel: "accept",
				// Intentionally no OverrideActor set.
			}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if len(result.ValidationOverrides) != 1 {
		t.Fatalf("ValidationOverrides length = %d, want 1", len(result.ValidationOverrides))
	}
	if result.ValidationOverrides[0].Actor != ActorUnknown {
		t.Errorf("Actor = %q, want %q", result.ValidationOverrides[0].Actor, ActorUnknown)
	}
}

// TestEngine_OverrideEdge_RestartIdempotent pins the idempotency guard: if
// the flip-point is invoked twice for the same gate+label, the sticky list
// contains exactly one entry, not two. Exercises the helper directly because
// natural-runtime re-traversal requires a restart path that also clears
// downstream state (and the override is preserved across restarts by design).
func TestEngine_OverrideEdge_RestartIdempotent(t *testing.T) {
	g := NewGraph("override_idempotent")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "gate", Shape: "hexagon", Label: "Gate"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "gate"})
	g.AddEdge(&Edge{From: "gate", To: "end", Label: "accept", Override: true})

	reg := newTestRegistry()
	engine := NewEngine(g, reg)

	s, err := engine.initRunState(context.Background())
	if err != nil {
		t.Fatalf("initRunState: %v", err)
	}
	s.lastOutcome = Outcome{OverrideActor: ActorHuman}
	overrideEdge := &Edge{From: "gate", To: "end", Label: "accept", Override: true}
	engine.recordOverrideIfPresent(s, "gate", overrideEdge)
	engine.recordOverrideIfPresent(s, "gate", overrideEdge)

	if len(s.validationOverrides) != 1 {
		t.Errorf("validationOverrides length = %d, want 1 after duplicate flip-point calls", len(s.validationOverrides))
	}
	if len(s.cp.ValidationOverrides) != 1 {
		t.Errorf("cp.ValidationOverrides length = %d, want 1 after duplicate flip-point calls", len(s.cp.ValidationOverrides))
	}

	// Different label on the same gate is a different override and should
	// record a second entry.
	otherEdge := &Edge{From: "gate", To: "end", Label: "mark done", Override: true}
	engine.recordOverrideIfPresent(s, "gate", otherEdge)
	if len(s.validationOverrides) != 2 {
		t.Errorf("validationOverrides length = %d, want 2 after distinct label", len(s.validationOverrides))
	}
}

// TestEngine_OverrideEdge_FailureAfterOverride verifies that when an
// override fires AND a downstream handler then fails, the terminal status
// is OutcomeFail (failure dominates), but ValidationOverrides is still
// populated so forensics can see "this run had an override AND it failed."
func TestEngine_OverrideEdge_FailureAfterOverride(t *testing.T) {
	g := NewGraph("override_then_fail")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "gate", Shape: "hexagon", Label: "Gate"})
	g.AddNode(&Node{ID: "post", Shape: "box", Label: "Post"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "gate"})
	g.AddEdge(&Edge{From: "gate", To: "post", Label: "accept", Override: true})
	// post has only an unconditional edge, so a fail outcome triggers
	// strict-failure stop (Status=fail).
	g.AddEdge(&Edge{From: "post", To: "end"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "wait.human",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:         string(OutcomeSuccess),
				PreferredLabel: "accept",
				OverrideActor:  ActorHuman,
			}, nil
		},
	})
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{Status: string(OutcomeFail)}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, _ := engine.Run(context.Background())
	if result == nil {
		t.Fatal("expected non-nil result on failure path")
	}
	if result.Status != OutcomeFail {
		t.Errorf("Status = %q, want %q (failure dominates)", result.Status, OutcomeFail)
	}
	if len(result.ValidationOverrides) != 1 {
		t.Fatalf("ValidationOverrides length = %d, want 1 even on failure", len(result.ValidationOverrides))
	}
	if result.ValidationOverrides[0].GateNodeID != "gate" {
		t.Errorf("GateNodeID = %q, want gate", result.ValidationOverrides[0].GateNodeID)
	}
}

// TestEngine_OverrideEdge_NoOverride_StaysSuccess verifies the negative case:
// a run with no override edges traversed reports Status=success, not
// validation_overridden.
func TestEngine_OverrideEdge_NoOverride_StaysSuccess(t *testing.T) {
	g := NewGraph("no_override")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "gate", Shape: "hexagon", Label: "Gate"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "gate"})
	// No Override flag.
	g.AddEdge(&Edge{From: "gate", To: "end", Label: "accept"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "wait.human",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:         string(OutcomeSuccess),
				PreferredLabel: "accept",
				OverrideActor:  ActorHuman,
			}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("Status = %q, want %q", result.Status, OutcomeSuccess)
	}
	if len(result.ValidationOverrides) != 0 {
		t.Errorf("ValidationOverrides length = %d, want 0", len(result.ValidationOverrides))
	}
}

// TestEngine_OverrideEdge_DecisionPriorityIsOverride pins the
// DecisionDetail.EdgePriority value emitted on EventDecisionEdge when the
// engine selects an Edge.Override-marked edge. The override edge is also a
// labeled edge (so the existing selection logic would naturally tag it as
// "label"); this test asserts the runtime overrides that to "override" so
// external NDJSON consumers keying off edge_priority see the override
// classification alongside the dedicated EventValidationOverridden event.
func TestEngine_OverrideEdge_DecisionPriorityIsOverride(t *testing.T) {
	g := NewGraph("override_decision_priority")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "gate", Shape: "hexagon", Label: "Gate", Attrs: map[string]string{"label": "Accept?"}})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "gate"})
	g.AddEdge(&Edge{From: "gate", To: "end", Label: "accept", Override: true})
	g.AddEdge(&Edge{From: "gate", To: "s", Label: "retry"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "wait.human",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:         string(OutcomeSuccess),
				PreferredLabel: "accept",
				OverrideActor:  ActorHuman,
			}, nil
		},
	})

	var mu sync.Mutex
	var observedPriority string
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		mu.Lock()
		defer mu.Unlock()
		if evt.Type == EventDecisionEdge && evt.Decision != nil && evt.Decision.EdgeFrom == "gate" {
			observedPriority = evt.Decision.EdgePriority
		}
	})

	engine := NewEngine(g, reg, WithPipelineEventHandler(handler))
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if observedPriority != EdgePriorityOverride {
		t.Errorf("EdgePriority = %q, want %q", observedPriority, EdgePriorityOverride)
	}
}

func TestEngine_BudgetCheckedOnFallbackRetryTarget(t *testing.T) {
	// A node that exhausts its retry budget (OutcomeRetry attempts) and routes
	// to fallback_retry_target must hit the budget guard at the fallback
	// transition — not after the fallback node has already run (ceiling
	// overshoot by one node). The within-budget retry path and the strict
	// failure fallback (#311) already check; exhaustion must match.
	g := NewGraph("budget_fallback_test")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "flaky", Shape: "box", Label: "Flaky", Attrs: map[string]string{
		"max_retries":           "1",
		"base_delay":            "0s",
		"fallback_retry_target": "recovery",
	}})
	g.AddNode(&Node{ID: "recovery", Shape: "box", Label: "Recovery"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "start", To: "flaky"})
	g.AddEdge(&Edge{From: "flaky", To: "end"})
	g.AddEdge(&Edge{From: "recovery", To: "end"})

	heavyStats := &SessionStats{InputTokens: 200, OutputTokens: 100, TotalTokens: 300, Provider: "test-provider"}

	var mu sync.Mutex
	recoveryRan := false
	reg := NewHandlerRegistry()
	for _, name := range []string{"start", "exit", "codergen", "wait.human", "conditional", "parallel", "parallel.fan_in", "tool"} {
		n := name
		reg.Register(&testHandler{name: n, executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			switch node.ID {
			case "flaky":
				// Transient-style failure: routes through the retry machinery.
				return Outcome{Status: string(OutcomeRetry), Stats: heavyStats}, nil
			case "recovery":
				mu.Lock()
				recoveryRan = true
				mu.Unlock()
				return Outcome{Status: string(OutcomeSuccess), Stats: heavyStats}, nil
			default:
				return Outcome{Status: string(OutcomeSuccess)}, nil
			}
		}})
	}

	// Attempt 1 lands at 300 tokens (under 450, retry proceeds); the final
	// attempt lands at 600 — breached by the time exhaustion routes away.
	guard := NewBudgetGuard(BudgetLimits{MaxTotalTokens: 450})
	engine := NewEngine(g, reg, WithBudgetGuard(guard))

	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine.Run returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Status != OutcomeBudgetExceeded {
		t.Errorf("result.Status = %q, want %q", result.Status, OutcomeBudgetExceeded)
	}
	mu.Lock()
	defer mu.Unlock()
	if recoveryRan {
		t.Error("fallback target executed after budget breach — budget must be checked at the fallback transition")
	}
}
