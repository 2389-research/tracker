// ABOUTME: Pins #697 — a run halted between a parallel node and its fan-in must resume
// ABOUTME: into the fan-in with parallel.results intact, at every fidelity level.
package handlers

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// overspendBranchRegistry wires s -> P(parallel) -> J(fan-in) -> e with branch
// targets that each report 10k tokens of child spend (so a MaxTotalTokens=100
// guard halts deterministically right after P) and write a per-branch key the
// fan-in must merge.
func overspendBranchRegistry(g *pipeline.Graph) *pipeline.HandlerRegistry {
	registry := pipeline.NewHandlerRegistry()
	registry.Register(NewStartHandler())
	registry.Register(NewExitHandler())
	registry.Register(&stubHandler{
		name: "overspend",
		execFunc: func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			return pipeline.Outcome{
				Status:         pipeline.OutcomeSuccess,
				ContextUpdates: map[string]string{"review_" + node.ID: "approved"},
				ChildUsage: &pipeline.UsageSummary{
					TotalTokens:  10_000,
					SessionCount: 1,
					ProviderTotals: map[string]pipeline.ProviderUsage{
						"anthropic": {TotalTokens: 10_000, SessionCount: 1},
					},
				},
			}, nil
		},
	})
	registry.Register(NewFanInHandler())
	registry.Register(NewParallelHandler(g, registry, nil))
	return registry
}

func buildFidelityParallelGraph(fidelity string) *pipeline.Graph {
	g := buildParallelChildUsageParentGraph("overspend")
	if fidelity != "" {
		g.Attrs["default_fidelity"] = fidelity
	}
	return g
}

// haltAfterParallel runs the graph once under a token guard that trips right
// after P, asserts the halt checkpoint carries the branch results, and returns
// the pre-halt parallel.results value.
func haltAfterParallel(t *testing.T, fidelity, cpPath string) string {
	t.Helper()
	g := buildFidelityParallelGraph(fidelity)
	guard := pipeline.NewBudgetGuard(pipeline.BudgetLimits{MaxTotalTokens: 100})
	r1, err := pipeline.NewEngine(g, overspendBranchRegistry(g),
		pipeline.WithBudgetGuard(guard), pipeline.WithCheckpointPath(cpPath)).Run(context.Background())
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if r1.Status != pipeline.OutcomeBudgetExceeded {
		t.Fatalf("first run status = %q, want %q", r1.Status, pipeline.OutcomeBudgetExceeded)
	}
	cp, err := pipeline.LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatalf("load halt checkpoint: %v", err)
	}
	if cp.CurrentNode != "P" || !slices.Contains(cp.CompletedNodes, "P") {
		t.Fatalf("halt checkpoint current=%q completed=%v, want current P and P completed", cp.CurrentNode, cp.CompletedNodes)
	}
	results := cp.Context[pipeline.ContextKeyParallelResults]
	if results == "" {
		t.Fatal("halt checkpoint has empty parallel.results")
	}
	return results
}

// TestParallelResults_SurviveResumeCompaction_697 pins #697: a budget halt
// between a parallel node and its fan-in, then `tracker -r`, must enter the
// fan-in with the branch results the checkpoint holds. Pre-fix, resume
// compaction at any fidelity below `full` blanked parallel.results and the
// fan-in failed with "unexpected end of JSON input".
func TestParallelResults_SurviveResumeCompaction_697(t *testing.T) {
	for _, fidelity := range []string{"", "summary:high", "summary:medium", "summary:low", "compact", "truncate"} {
		t.Run("fidelity="+fidelity, func(t *testing.T) {
			cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
			before := haltAfterParallel(t, fidelity, cpPath)

			g := buildFidelityParallelGraph(fidelity)
			r2, err := pipeline.NewEngine(g, overspendBranchRegistry(g),
				pipeline.WithCheckpointPath(cpPath)).Run(context.Background())
			if err != nil {
				t.Fatalf("resume: %v", err)
			}
			if r2.Status != pipeline.OutcomeSuccess {
				t.Fatalf("resume status = %q, want success", r2.Status)
			}
			if !slices.Contains(r2.CompletedNodes, "J") {
				t.Errorf("fan-in J not completed on resume: %v", r2.CompletedNodes)
			}
			for _, key := range []string{"review_branch_a", "review_branch_b"} {
				if v := r2.Context[key]; v != "approved" {
					t.Errorf("%s = %q after resume, want merged branch value %q", key, v, "approved")
				}
			}
			if got := r2.Context[pipeline.ContextKeyParallelResults]; got != before {
				t.Errorf("parallel.results changed across resume:\n got  %q\n want %q", got, before)
			}
		})
	}
}

// TestResumeCompaction_KeepsInputsAndGraphAttrs_697 pins the same-class loss
// found while fixing #697: under summary:high (resume level summary:medium),
// resume compaction blanked declared inputs (`inputs.*`) and graph attributes
// (`graph.*` other than graph.goal), so a node after the resume point silently
// expanded ${inputs.x} / $var to "".
func TestResumeCompaction_KeepsInputsAndGraphAttrs_697(t *testing.T) {
	mk := func() *pipeline.Graph {
		g := pipeline.NewGraph("g")
		g.Attrs["default_fidelity"] = "summary:high"
		g.Attrs["target"] = "widget"
		g.AddNode(&pipeline.Node{ID: "s", Shape: "Mdiamond"})
		g.AddNode(&pipeline.Node{ID: "a", Shape: "box"})
		g.AddNode(&pipeline.Node{ID: "b", Shape: "box"})
		g.AddNode(&pipeline.Node{ID: "e", Shape: "Msquare"})
		g.AddEdge(&pipeline.Edge{From: "s", To: "a"})
		g.AddEdge(&pipeline.Edge{From: "a", To: "b"})
		g.AddEdge(&pipeline.Edge{From: "b", To: "e"})
		return g
	}
	seen := map[string]map[string]string{}
	reg := func() *pipeline.HandlerRegistry {
		r := pipeline.NewHandlerRegistry()
		r.Register(NewStartHandler())
		r.Register(NewExitHandler())
		r.Register(&stubHandler{name: "codergen", execFunc: func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
			seen[node.ID] = pctx.Snapshot()
			// Node a overspends so the guard halts between a and b.
			return pipeline.Outcome{Status: pipeline.OutcomeSuccess, ChildUsage: &pipeline.UsageSummary{TotalTokens: 10_000, SessionCount: 1}}, nil
		}})
		return r
	}
	cpPath := filepath.Join(t.TempDir(), "cp.json")
	initial := map[string]string{"inputs.repo": "github.com/x/y"}
	guard := pipeline.NewBudgetGuard(pipeline.BudgetLimits{MaxTotalTokens: 100})
	r1, err := pipeline.NewEngine(mk(), reg(), pipeline.WithBudgetGuard(guard),
		pipeline.WithCheckpointPath(cpPath), pipeline.WithInitialContext(initial)).Run(context.Background())
	if err != nil || r1.Status != pipeline.OutcomeBudgetExceeded {
		t.Fatalf("first run = %v, %v; want budget_exceeded halt after a", r1.Status, err)
	}
	if _, ran := seen["b"]; ran {
		t.Fatal("node b ran in the first run; the guard did not halt between a and b")
	}

	r2, err := pipeline.NewEngine(mk(), reg(), pipeline.WithCheckpointPath(cpPath),
		pipeline.WithInitialContext(initial)).Run(context.Background())
	if err != nil || r2.Status != pipeline.OutcomeSuccess {
		t.Fatalf("resume = %v, %v; want success", r2.Status, err)
	}
	if got := seen["b"]["inputs.repo"]; got != "github.com/x/y" {
		t.Errorf("node b on resume saw inputs.repo=%q, want %q", got, "github.com/x/y")
	}
	if got := seen["b"]["graph.target"]; got != "widget" {
		t.Errorf("node b on resume saw graph.target=%q, want %q", got, "widget")
	}
}
