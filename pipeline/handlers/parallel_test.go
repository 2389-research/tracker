// ABOUTME: Tests for the parallel fan-out handler that spawns concurrent goroutines per branch.
// ABOUTME: Validates concurrency, context isolation, result collection, and success/failure semantics.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// stubHandler is a configurable test handler that returns a fixed outcome or error.
type stubHandler struct {
	name    string
	outcome pipeline.Outcome
	err     error
	// called is incremented each time Execute runs, for concurrency verification.
	called atomic.Int32
	// execFunc, if set, overrides outcome/err with custom logic.
	execFunc func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error)
}

func (s *stubHandler) Name() string { return s.name }

func (s *stubHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	s.called.Add(1)
	if s.execFunc != nil {
		return s.execFunc(ctx, node, pctx)
	}
	return s.outcome, s.err
}

// buildTestGraph creates a graph with a parallel node and the given branch target node IDs.
func buildTestGraph(branchNodeIDs []string, branchHandlerName string) *pipeline.Graph {
	g := pipeline.NewGraph("test")
	parallelNode := &pipeline.Node{
		ID:      "parallel_node",
		Shape:   "component",
		Handler: "parallel",
		Attrs:   map[string]string{},
	}
	g.AddNode(parallelNode)

	for _, id := range branchNodeIDs {
		targetNode := &pipeline.Node{
			ID:    id,
			Shape: "box",
			Attrs: map[string]string{},
		}
		g.AddNode(targetNode)
		// Override the handler after AddNode, since AddNode maps shape->handler
		// automatically (box -> "codergen"), but tests use custom stub handlers.
		g.Nodes[id].Handler = branchHandlerName
		g.AddEdge(&pipeline.Edge{From: "parallel_node", To: id})
	}
	return g
}

func TestParallelHandlerName(t *testing.T) {
	h := NewParallelHandler(nil, nil)
	if h.Name() != "parallel" {
		t.Errorf("expected 'parallel', got %q", h.Name())
	}
}

func TestParallelHandlerNoEdges(t *testing.T) {
	g := pipeline.NewGraph("test")
	g.AddNode(&pipeline.Node{ID: "parallel_node", Shape: "component", Handler: "parallel"})
	registry := pipeline.NewHandlerRegistry()

	h := NewParallelHandler(g, registry)
	node := g.Nodes["parallel_node"]
	pctx := pipeline.NewPipelineContext()

	_, err := h.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected error for no outgoing edges")
	}
}

func TestParallelHandlerSuccess(t *testing.T) {
	g := buildTestGraph([]string{"branch_a", "branch_b"}, "stub_success")
	registry := pipeline.NewHandlerRegistry()
	stub := &stubHandler{
		name:    "stub_success",
		outcome: pipeline.Outcome{Status: pipeline.OutcomeSuccess, ContextUpdates: map[string]string{"done": "yes"}},
	}
	registry.Register(stub)

	h := NewParallelHandler(g, registry)
	node := g.Nodes["parallel_node"]
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected 'success', got %q", outcome.Status)
	}
	if stub.called.Load() != 2 {
		t.Errorf("expected stub called 2 times, got %d", stub.called.Load())
	}
}

func TestParallelHandlerPartialFailure(t *testing.T) {
	g := buildTestGraph([]string{"branch_ok", "branch_fail"}, "stub_mixed")
	registry := pipeline.NewHandlerRegistry()

	callCount := atomic.Int32{}
	mixed := &stubHandler{
		name: "stub_mixed",
		execFunc: func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			n := callCount.Add(1)
			if node.ID == "branch_fail" {
				return pipeline.Outcome{Status: pipeline.OutcomeFail}, nil
			}
			_ = n
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
		},
	}
	registry.Register(mixed)

	h := NewParallelHandler(g, registry)
	node := g.Nodes["parallel_node"]
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// At least one succeeded, so overall should be success.
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected 'success' (partial failure), got %q", outcome.Status)
	}
}

func TestParallelHandlerAllFail(t *testing.T) {
	g := buildTestGraph([]string{"branch_a", "branch_b"}, "stub_fail")
	registry := pipeline.NewHandlerRegistry()
	stub := &stubHandler{
		name:    "stub_fail",
		outcome: pipeline.Outcome{Status: pipeline.OutcomeFail},
	}
	registry.Register(stub)

	h := NewParallelHandler(g, registry)
	node := g.Nodes["parallel_node"]
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected 'fail' (all failed), got %q", outcome.Status)
	}
}

func TestParallelHandlerContextIsolation(t *testing.T) {
	g := buildTestGraph([]string{"branch_a", "branch_b"}, "stub_writer")
	registry := pipeline.NewHandlerRegistry()

	writer := &stubHandler{
		name: "stub_writer",
		execFunc: func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			// Each branch writes its own ID to the same key.
			pctx.Set("writer", node.ID)
			// Small sleep to increase chance of race detection under -race.
			time.Sleep(5 * time.Millisecond)
			// Read back -- should see its own write, not the other branch's.
			val, _ := pctx.Get("writer")
			if val != node.ID {
				return pipeline.Outcome{Status: pipeline.OutcomeFail}, fmt.Errorf("context leak: expected %q, got %q", node.ID, val)
			}
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
		},
	}
	registry.Register(writer)

	h := NewParallelHandler(g, registry)
	node := g.Nodes["parallel_node"]
	pctx := pipeline.NewPipelineContext()
	pctx.Set("shared_key", "original")

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected 'success', got %q — branches may have leaked context", outcome.Status)
	}

	// The parent context should not have been mutated by branches.
	val, _ := pctx.Get("writer")
	if val != "" {
		t.Errorf("parent context was mutated by branch: writer=%q", val)
	}
	// Original value should still be intact.
	shared, _ := pctx.Get("shared_key")
	if shared != "original" {
		t.Errorf("parent context shared_key changed: got %q", shared)
	}
}

func TestParallelHandlerResultsInContext(t *testing.T) {
	g := buildTestGraph([]string{"branch_a", "branch_b"}, "stub_ctx")
	registry := pipeline.NewHandlerRegistry()
	stub := &stubHandler{
		name: "stub_ctx",
		outcome: pipeline.Outcome{
			Status:         pipeline.OutcomeSuccess,
			ContextUpdates: map[string]string{"result": "done"},
		},
	}
	registry.Register(stub)

	h := NewParallelHandler(g, registry)
	node := g.Nodes["parallel_node"]
	pctx := pipeline.NewPipelineContext()

	_, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, ok := pctx.Get("parallel.results")
	if !ok {
		t.Fatal("expected parallel.results to be set in context")
	}

	var results []ParallelResult
	if err := json.Unmarshal([]byte(raw), &results); err != nil {
		t.Fatalf("failed to unmarshal parallel.results: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Verify each result has the correct status and node ID.
	nodeIDs := map[string]bool{}
	for _, r := range results {
		nodeIDs[r.NodeID] = true
		if r.Status != pipeline.OutcomeSuccess {
			t.Errorf("expected success for %q, got %q", r.NodeID, r.Status)
		}
	}
	if !nodeIDs["branch_a"] || !nodeIDs["branch_b"] {
		t.Errorf("expected results for branch_a and branch_b, got %v", nodeIDs)
	}
}

func TestParallelHandlerPreservesInternalArtifactDir(t *testing.T) {
	g := buildTestGraph([]string{"branch_a", "branch_b"}, "stub_internal")
	registry := pipeline.NewHandlerRegistry()
	stub := &stubHandler{
		name: "stub_internal",
		execFunc: func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			dir, ok := pctx.GetInternal(pipeline.InternalKeyArtifactDir)
			if !ok || dir == "" {
				return pipeline.Outcome{Status: pipeline.OutcomeFail}, fmt.Errorf("missing internal artifact dir")
			}
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
		},
	}
	registry.Register(stub)

	h := NewParallelHandler(g, registry)
	node := g.Nodes["parallel_node"]
	pctx := pipeline.NewPipelineContext()
	pctx.SetInternal(pipeline.InternalKeyArtifactDir, "/tmp/artifacts/run-123")

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Fatalf("expected success, got %q", outcome.Status)
	}
}
