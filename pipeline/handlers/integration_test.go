// ABOUTME: Integration test that loads sprint_exec.dot and runs it through the full pipeline engine.
// ABOUTME: Validates that all built-in handlers wire up correctly via NewDefaultRegistry.
package handlers

import (
	"context"
	"os"
	"testing"

	"github.com/2389-research/mammoth-lite/pipeline"
)

func TestSprintExecIntegration(t *testing.T) {
	dotBytes, err := os.ReadFile("../../examples/sprint_exec.dot")
	if err != nil {
		t.Fatalf("failed to read sprint_exec.dot: %v", err)
	}

	graph, err := pipeline.ParseDOT(string(dotBytes))
	if err != nil {
		t.Fatalf("failed to parse DOT: %v", err)
	}

	if err := pipeline.Validate(graph); err != nil {
		t.Fatalf("graph validation failed: %v", err)
	}

	// Stub codergen: returns success with outcome=success context update
	// so conditional edges (outcome=success) are taken.
	codergenStub := func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
		return pipeline.Outcome{
			Status: pipeline.OutcomeSuccess,
			ContextUpdates: map[string]string{
				"outcome": "success",
			},
		}, nil
	}

	// Stub tool: returns success.
	toolStub := func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
		return pipeline.Outcome{
			Status: pipeline.OutcomeSuccess,
			ContextUpdates: map[string]string{
				"outcome": "success",
			},
		}, nil
	}

	// Stub human: returns success.
	humanStub := func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
		return pipeline.Outcome{
			Status: pipeline.OutcomeSuccess,
		}, nil
	}

	registry := NewDefaultRegistry(graph,
		WithCodergenFunc(codergenStub),
		WithToolExecFunc(toolStub),
		WithHumanCallback(humanStub),
	)

	engine := pipeline.NewEngine(graph, registry)

	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine.Run failed: %v", err)
	}
	if result == nil {
		t.Fatal("engine.Run returned nil result")
	}
	if result.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected status %q, got %q", pipeline.OutcomeSuccess, result.Status)
	}

	// Verify key nodes were visited.
	completedSet := make(map[string]bool)
	for _, n := range result.CompletedNodes {
		completedSet[n] = true
	}

	expectedNodes := []string{
		"Start", "Exit",
		"EnsureLedger", "FindNextSprint", "SetCurrentSprint",
		"ReadSprint", "MarkInProgress", "ImplementSprint",
		"ValidateBuild", "CommitSprintWork",
		"ReviewParallel", "ReviewsJoin",
		"CritiquesParallel", "CritiquesJoin",
		"ReviewAnalysis", "CompleteSprint",
	}
	for _, name := range expectedNodes {
		if !completedSet[name] {
			t.Errorf("expected node %q to be completed, but it was not", name)
		}
	}
}
