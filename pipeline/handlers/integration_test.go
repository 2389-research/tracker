// ABOUTME: Integration test that loads the shipped sprint_exec.dip and runs it through the full pipeline engine.
// ABOUTME: Validates that all built-in handlers wire up correctly via NewDefaultRegistry.
package handlers

import (
	"context"
	"os"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// stubHandlers returns codergen/tool/human stubs that all report success with
// an outcome=success context update so conditional (outcome=success) edges are
// taken. Shared by the DIP integration test and the DOT-parser fixture test.
func stubHandlers() []RegistryOption {
	successStub := func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
		return pipeline.Outcome{
			Status: pipeline.OutcomeSuccess,
			ContextUpdates: map[string]string{
				"outcome": "success",
			},
		}, nil
	}
	humanStub := func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
		return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
	}
	return []RegistryOption{
		WithCodergenFunc(successStub),
		WithToolExecFunc(successStub),
		WithHumanCallback(humanStub),
	}
}

// runGraph wires the default registry with stub handlers and runs the graph to
// completion, returning the set of completed node IDs.
func runGraph(t *testing.T, graph *pipeline.Graph) map[string]bool {
	t.Helper()

	if err := pipeline.Validate(graph); err != nil {
		t.Fatalf("graph validation failed: %v", err)
	}

	registry := NewDefaultRegistry(graph, stubHandlers()...)
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

	completedSet := make(map[string]bool)
	for _, n := range result.CompletedNodes {
		completedSet[n] = true
	}
	return completedSet
}

// TestSprintExecIntegration loads the shipped DIP source of the sprint_exec
// example — the sole authority for that workflow (SIFT-SUB-14-01) — and runs it
// through the full engine, confirming every built-in handler wires up.
func TestSprintExecIntegration(t *testing.T) {
	const path = "../../examples/sprint_exec.dip"
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read sprint_exec.dip: %v", err)
	}

	// Pass the on-disk path as the filename so the DIP's command_file /
	// prompt_file directives anchor to examples/ (matching the dippin CLI).
	graph, _, err := pipeline.LoadDippinWorkflow(string(source), path)
	if err != nil {
		t.Fatalf("failed to load DIP: %v", err)
	}

	completedSet := runGraph(t, graph)

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

// TestDOTParserFixtureIntegration keeps the public DOT parser exercised through
// the full engine after sprint_exec migrated to DIP (SIFT-SUB-14-01). It uses a
// dedicated, minimal parser fixture — NOT a shipped product workflow — that
// covers the DOT grammar the engine relies on: start/exit shapes, codergen/tool
// nodes, parallel + fan-in, conditional edges, and an unconditional fallback.
func TestDOTParserFixtureIntegration(t *testing.T) {
	dotBytes, err := os.ReadFile("testdata/dot_parser_fixture.dot")
	if err != nil {
		t.Fatalf("failed to read dot_parser_fixture.dot: %v", err)
	}

	graph, err := pipeline.ParseDOT(string(dotBytes))
	if err != nil {
		t.Fatalf("failed to parse DOT: %v", err)
	}

	// Assert the DOT grammar mapped shapes to the right handlers, including the
	// second parallel branch and its edges (so BranchB is parser-covered even
	// though the DOT parallel fan-in reports on the first branch at runtime).
	wantHandler := map[string]string{
		"Start":   "start",
		"Exit":    "exit",
		"Setup":   "tool",
		"Fan":     "parallel",
		"BranchA": "codergen",
		"BranchB": "codergen",
		"Join":    "parallel.fan_in",
		"Gate":    "codergen",
		"Finish":  "tool",
	}
	for id, want := range wantHandler {
		n := graph.Nodes[id]
		if n == nil {
			t.Fatalf("node %q missing from parsed graph", id)
			continue
		}
		if n.Handler != want {
			t.Errorf("node %q handler = %q, want %q", id, n.Handler, want)
		}
	}

	// Assert the conditional edge grammar parsed (condition + label).
	edgeSet := make(map[string]string) // "From->To" -> condition
	for _, e := range graph.Edges {
		edgeSet[e.From+"->"+e.To] = e.Condition
	}
	for edge, wantCond := range map[string]string{
		"Fan->BranchA": "",
		"Fan->BranchB": "",
		"Gate->Finish": "outcome=success",
		"Gate->Exit":   "outcome=fail",
	} {
		cond, ok := edgeSet[edge]
		if !ok {
			t.Errorf("missing edge %s", edge)
			continue
		}
		if cond != wantCond {
			t.Errorf("edge %s condition = %q, want %q", edge, cond, wantCond)
		}
	}

	completedSet := runGraph(t, graph)

	// The DOT parallel fan-in reports on the first branch, so only the primary
	// path is asserted as completed; branch coverage is via the parse assertions.
	expectedNodes := []string{
		"Start", "Exit",
		"Setup", "Fan", "Join", "Gate", "Finish",
	}
	for _, name := range expectedNodes {
		if !completedSet[name] {
			t.Errorf("expected node %q to be completed, but it was not", name)
		}
	}
}
