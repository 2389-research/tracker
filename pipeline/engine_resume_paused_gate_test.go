// ABOUTME: Tests resuming a gate-paused run: the budget spans the whole run,
// ABOUTME: not each answer (PriorUsage seeds the guard's baseline).
package pipeline

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// budgetPauseGraph is s -> spend1 -> gate -> spend2 -> end. spend1 costs
// $0.90, spend2 $0.20; gate pauses (with pauseErr) until *pause is false.
func budgetPauseGraph(pause *bool, pauseErr func() error) (*Graph, *HandlerRegistry) {
	g := NewGraph("budget_pause")
	for _, n := range []*Node{
		{ID: "s", Shape: "Mdiamond", Label: "Start"},
		{ID: "spend1", Shape: "box"}, {ID: "gate", Shape: "box"}, {ID: "spend2", Shape: "box"},
		{ID: "end", Shape: "Msquare", Label: "End"},
	} {
		g.AddNode(n)
	}
	for _, e := range [][2]string{{"s", "spend1"}, {"spend1", "gate"}, {"gate", "spend2"}, {"spend2", "end"}} {
		g.AddEdge(&Edge{From: e[0], To: e[1]})
	}
	cost := map[string]float64{"spend1": 0.90, "spend2": 0.20}
	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(_ context.Context, node *Node, _ *PipelineContext) (Outcome, error) {
			if node.ID == "gate" && *pause {
				return Outcome{}, pauseErr()
			}
			out := Outcome{Status: OutcomeSuccess}
			if c, ok := cost[node.ID]; ok {
				out.Stats = &SessionStats{CostUSD: c, TotalTokens: 1, InputTokens: 1}
			}
			return out, nil
		},
	})
	return g, reg
}

func runBudgetPause(t *testing.T, pauseErr func() error) *EngineResult {
	t.Helper()
	pause := true
	g, reg := budgetPauseGraph(&pause, pauseErr)
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	opts := func() []EngineOption {
		return []EngineOption{WithCheckpointPath(cpPath), WithBudgetGuard(NewBudgetGuard(BudgetLimits{MaxCostCents: 100}))}
	}
	first, _ := NewEngine(g, reg, opts()...).Run(context.Background())
	if first == nil || !first.Status.IsPaused() {
		t.Fatalf("first segment = %v, want a pause", first)
	}
	pause = false
	second, _ := NewEngine(g, reg, opts()...).Run(context.Background())
	if second == nil {
		t.Fatal("nil resume result")
	}
	return second
}

func TestResume_PausedGateSeedsBudget(t *testing.T) {
	got := runBudgetPause(t, func() error { return NewPausedGateError(PausedGate{NodeID: "gate"}) })
	if got.Status != OutcomeBudgetExceeded {
		t.Fatalf("resume status = %q, want budget_exceeded ($0.90 before the pause + $0.20 after > $1.00)", got.Status)
	}
}

func TestResume_OrdinaryResumeBudgetStartsFresh(t *testing.T) {
	got := runBudgetPause(t, func() error { return NewPauseError(OutcomePausedBilling, errors.New("credit balance too low")) })
	if got.Status != OutcomeSuccess {
		t.Fatalf("resume status = %q, want success (an ordinary resume's budget is unchanged)", got.Status)
	}
}
