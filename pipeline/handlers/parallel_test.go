// ABOUTME: Tests for the parallel fan-out handler that spawns concurrent goroutines per branch.
// ABOUTME: Validates concurrency, context isolation, result collection, and success/failure semantics.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
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
		Attrs: map[string]string{
			"parallel_targets": strings.Join(branchNodeIDs, ","),
		},
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
	h := NewParallelHandler(nil, nil, nil)
	if h.Name() != "parallel" {
		t.Errorf("expected 'parallel', got %q", h.Name())
	}
}

func TestParallelHandlerNoEdges(t *testing.T) {
	g := pipeline.NewGraph("test")
	g.AddNode(&pipeline.Node{ID: "parallel_node", Shape: "component", Handler: "parallel"})
	registry := pipeline.NewHandlerRegistry()

	h := NewParallelHandler(g, registry, nil)
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

	h := NewParallelHandler(g, registry, nil)
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

	h := NewParallelHandler(g, registry, nil)
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

	h := NewParallelHandler(g, registry, nil)
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

	h := NewParallelHandler(g, registry, nil)
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

	h := NewParallelHandler(g, registry, nil)
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

func TestParseBranchOverrides(t *testing.T) {
	attrs := map[string]string{
		"branch.0.target":       "AgentA",
		"branch.0.llm_model":    "gpt-4",
		"branch.0.llm_provider": "openai",
		"branch.1.target":       "AgentB",
		"branch.1.llm_model":    "claude-opus-4",
		"branch.1.fidelity":     "compact",
		"parallel_targets":      "AgentA,AgentB", // unrelated attr, should be ignored
	}

	overrides := parseBranchOverrides(attrs)

	if len(overrides) != 2 {
		t.Fatalf("expected 2 branch overrides, got %d", len(overrides))
	}
	if overrides["AgentA"]["llm_model"] != "gpt-4" {
		t.Errorf("AgentA llm_model = %q, want gpt-4", overrides["AgentA"]["llm_model"])
	}
	if overrides["AgentA"]["llm_provider"] != "openai" {
		t.Errorf("AgentA llm_provider = %q, want openai", overrides["AgentA"]["llm_provider"])
	}
	if overrides["AgentB"]["llm_model"] != "claude-opus-4" {
		t.Errorf("AgentB llm_model = %q, want claude-opus-4", overrides["AgentB"]["llm_model"])
	}
	if overrides["AgentB"]["fidelity"] != "compact" {
		t.Errorf("AgentB fidelity = %q, want compact", overrides["AgentB"]["fidelity"])
	}
	// "target" should not be in the override attrs
	if _, ok := overrides["AgentA"]["target"]; ok {
		t.Error("target key should not be in override attrs")
	}
}

func TestParseBranchOverrides_Empty(t *testing.T) {
	overrides := parseBranchOverrides(map[string]string{"some_attr": "value"})
	if len(overrides) != 0 {
		t.Errorf("expected 0 overrides, got %d", len(overrides))
	}
}

func TestParallelHandlerBranchOverrides(t *testing.T) {
	g := pipeline.NewGraph("test")
	parallelNode := &pipeline.Node{
		ID:      "fanout",
		Shape:   "component",
		Handler: "parallel",
		Attrs: map[string]string{
			"parallel_targets":   "AgentA,AgentB",
			"branch.0.target":    "AgentA",
			"branch.0.llm_model": "gpt-4",
			"branch.1.target":    "AgentB",
			"branch.1.llm_model": "claude-opus-4",
		},
	}
	g.AddNode(parallelNode)

	// Both branches use the same handler but we'll capture which model each sees.
	agentA := &pipeline.Node{
		ID:    "AgentA",
		Shape: "box",
		Attrs: map[string]string{"llm_model": "default-model"},
	}
	agentB := &pipeline.Node{
		ID:    "AgentB",
		Shape: "box",
		Attrs: map[string]string{"llm_model": "default-model"},
	}
	g.AddNode(agentA)
	g.AddNode(agentB)
	// Override handler after AddNode (AddNode auto-maps shape→handler)
	g.Nodes["AgentA"].Handler = "stub_branch"
	g.Nodes["AgentB"].Handler = "stub_branch"
	g.AddEdge(&pipeline.Edge{From: "fanout", To: "AgentA"})
	g.AddEdge(&pipeline.Edge{From: "fanout", To: "AgentB"})

	// Track which model each branch received.
	models := make(map[string]string)
	var mu sync.Mutex

	registry := pipeline.NewHandlerRegistry()
	registry.Register(&stubHandler{
		name: "stub_branch",
		execFunc: func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			mu.Lock()
			models[node.ID] = node.Attrs["llm_model"]
			mu.Unlock()
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
		},
	})

	h := NewParallelHandler(g, registry, nil)
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), parallelNode, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Fatalf("expected success, got %q", outcome.Status)
	}

	if models["AgentA"] != "gpt-4" {
		t.Errorf("AgentA saw model %q, want gpt-4", models["AgentA"])
	}
	if models["AgentB"] != "claude-opus-4" {
		t.Errorf("AgentB saw model %q, want claude-opus-4", models["AgentB"])
	}
}

func TestParallelHandlerBranchOverridesPreserveOriginal(t *testing.T) {
	g := pipeline.NewGraph("test")
	parallelNode := &pipeline.Node{
		ID:      "fanout",
		Shape:   "component",
		Handler: "parallel",
		Attrs: map[string]string{
			"parallel_targets":   "Agent",
			"branch.0.target":    "Agent",
			"branch.0.llm_model": "overridden-model",
		},
	}
	g.AddNode(parallelNode)

	originalAttrs := map[string]string{"llm_model": "original-model", "prompt": "do stuff"}
	agent := &pipeline.Node{
		ID:      "Agent",
		Shape:   "box",
		Handler: "stub_preserve",
		Attrs:   originalAttrs,
	}
	g.AddNode(agent)
	g.AddEdge(&pipeline.Edge{From: "fanout", To: "Agent"})

	registry := pipeline.NewHandlerRegistry()
	registry.Register(&stubHandler{
		name:    "stub_preserve",
		outcome: pipeline.Outcome{Status: pipeline.OutcomeSuccess},
	})

	h := NewParallelHandler(g, registry, nil)
	pctx := pipeline.NewPipelineContext()

	_, err := h.Execute(context.Background(), parallelNode, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the original node was NOT mutated.
	if g.Nodes["Agent"].Attrs["llm_model"] != "original-model" {
		t.Errorf("original node was mutated: llm_model = %q", g.Nodes["Agent"].Attrs["llm_model"])
	}
}

func TestParallelHandlerAggregatesBranchStats(t *testing.T) {
	g := buildTestGraph([]string{"branch_a", "branch_b"}, "stub_stats")
	registry := pipeline.NewHandlerRegistry()

	stub := &stubHandler{
		name: "stub_stats",
		execFunc: func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			stats := &pipeline.SessionStats{
				Turns:          3,
				TotalToolCalls: 5,
				InputTokens:    1000,
				OutputTokens:   500,
				TotalTokens:    1500,
				CostUSD:        0.05,
				Compactions:    1,
				CacheHits:      10,
				CacheMisses:    2,
				LongestTurn:    100 * time.Millisecond,
				FilesModified:  []string{node.ID + "/main.go"},
				FilesCreated:   []string{node.ID + "/new.go"},
				ToolCalls:      map[string]int{"read": 3, "write": 2},
			}
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess, Stats: stats}, nil
		},
	}
	registry.Register(stub)

	h := NewParallelHandler(g, registry, nil)
	node := g.Nodes["parallel_node"]
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Fatalf("expected success, got %q", outcome.Status)
	}
	if outcome.Stats == nil {
		t.Fatal("expected aggregated stats on outcome, got nil")
	}

	s := outcome.Stats
	// Two branches, each with 3 turns → 6 total
	if s.Turns != 6 {
		t.Errorf("Turns = %d, want 6", s.Turns)
	}
	if s.TotalToolCalls != 10 {
		t.Errorf("TotalToolCalls = %d, want 10", s.TotalToolCalls)
	}
	if s.InputTokens != 2000 {
		t.Errorf("InputTokens = %d, want 2000", s.InputTokens)
	}
	if s.OutputTokens != 1000 {
		t.Errorf("OutputTokens = %d, want 1000", s.OutputTokens)
	}
	if s.TotalTokens != 3000 {
		t.Errorf("TotalTokens = %d, want 3000", s.TotalTokens)
	}
	if s.CostUSD != 0.10 {
		t.Errorf("CostUSD = %f, want 0.10", s.CostUSD)
	}
	if s.Compactions != 2 {
		t.Errorf("Compactions = %d, want 2", s.Compactions)
	}
	if s.CacheHits != 20 {
		t.Errorf("CacheHits = %d, want 20", s.CacheHits)
	}
	if s.CacheMisses != 4 {
		t.Errorf("CacheMisses = %d, want 4", s.CacheMisses)
	}
	if s.LongestTurn != 100*time.Millisecond {
		t.Errorf("LongestTurn = %v, want 100ms", s.LongestTurn)
	}
	if len(s.FilesModified) != 2 {
		t.Errorf("FilesModified count = %d, want 2", len(s.FilesModified))
	}
	if len(s.FilesCreated) != 2 {
		t.Errorf("FilesCreated count = %d, want 2", len(s.FilesCreated))
	}
	if s.ToolCalls["read"] != 6 {
		t.Errorf("ToolCalls[read] = %d, want 6", s.ToolCalls["read"])
	}
	if s.ToolCalls["write"] != 4 {
		t.Errorf("ToolCalls[write] = %d, want 4", s.ToolCalls["write"])
	}
}

func TestParallelHandlerNilStatsWhenNoBranchStats(t *testing.T) {
	g := buildTestGraph([]string{"branch_a"}, "stub_nostats")
	registry := pipeline.NewHandlerRegistry()

	stub := &stubHandler{
		name:    "stub_nostats",
		outcome: pipeline.Outcome{Status: pipeline.OutcomeSuccess},
	}
	registry.Register(stub)

	h := NewParallelHandler(g, registry, nil)
	node := g.Nodes["parallel_node"]
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Stats != nil {
		t.Errorf("expected nil stats when no branches have stats, got %+v", outcome.Stats)
	}
}

func TestParallelHandlerStatsInJSON(t *testing.T) {
	// Verify ParallelResult JSON round-trips the Stats field
	pr := ParallelResult{
		NodeID: "test",
		Status: pipeline.OutcomeSuccess,
		Stats:  &pipeline.SessionStats{InputTokens: 500, CostUSD: 0.01},
	}
	data, err := json.Marshal(pr)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var decoded ParallelResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if decoded.Stats == nil {
		t.Fatal("expected Stats to survive JSON round-trip")
	}
	if decoded.Stats.InputTokens != 500 {
		t.Errorf("InputTokens = %d, want 500", decoded.Stats.InputTokens)
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

	h := NewParallelHandler(g, registry, nil)
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
