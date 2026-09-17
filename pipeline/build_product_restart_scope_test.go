// ABOUTME: #643 regression on the REAL build_product.dip graph: milestone loops must not exhaust the restart budget.
// ABOUTME: Pins the derived loop nesting and runs scripted 60-milestone sims through the engine.
package pipeline

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// TestBuildProductRestartScopes pins the loop structure the engine derives
// from build_product.dip, so a graph edit that silently flattens the nesting
// (and re-opens #643) fails here rather than in a 50-milestone run:
//
//	PickNextMilestone  (outermost milestone loop; back edge MarkMilestoneDone)
//	└── CommitIfDirty  (verified_green re-commit loop; back edge FixMilestone)
//	    └── TestMilestone (fix loop; back edge FixMilestone)
//	└── Implement      (continue-with-more-turns loop; back edge ContinueWithMoreTurns)
//
// #640 A4 added CheckVerifyFailBudget on the VerifyMilestone-fail edge, so it
// sits inside the TestMilestone (and therefore CommitIfDirty) fix loop body.
func TestBuildProductRestartScopes(t *testing.T) {
	g := loadBuildProduct(t)
	rs := computeRestartScopes(g)

	if !rs.isBackEdge("MarkMilestoneDone", "PickNextMilestone") {
		t.Fatal("MarkMilestoneDone -> PickNextMilestone must be a back edge (PickNextMilestone dominates the milestone loop)")
	}
	if !rs.isBackEdge("FixMilestone", "TestMilestone") {
		t.Fatal("FixMilestone -> TestMilestone must be a back edge")
	}
	if !rs.isBackEdge("FixMilestone", "CommitIfDirty") {
		t.Fatal("FixMilestone -> CommitIfDirty must be a back edge")
	}
	if !rs.isBackEdge("ContinueWithMoreTurns", "Implement") {
		t.Fatal("ContinueWithMoreTurns -> Implement must be a back edge")
	}

	if got := rs.innerNodes("TestMilestone"); fmt.Sprint(got) != "[CheckVerifyFailBudget FixMilestone VerifyMilestone]" {
		t.Errorf("loop(TestMilestone) inner = %v", got)
	}
	if got := rs.innerNodes("CommitIfDirty"); fmt.Sprint(got) != "[CheckVerifyFailBudget FixMilestone TestMilestone VerifyMilestone]" {
		t.Errorf("loop(CommitIfDirty) inner = %v", got)
	}
	if got := rs.innerNodes("Implement"); fmt.Sprint(got) != "[ContinueWithMoreTurns OperatorDecision]" {
		t.Errorf("loop(Implement) inner = %v", got)
	}
	outer := rs.inner["PickNextMilestone"]
	for _, nested := range []string{"TestMilestone", "CommitIfDirty", "Implement", "FixMilestone", "VerifyMilestone", "MarkMilestoneDone", "EscalateMilestone"} {
		if !outer[nested] {
			t.Errorf("loop(PickNextMilestone) must contain %s", nested)
		}
	}
	// The milestone loop is outermost: no header's loop contains it, so its
	// max_restarts is the run-wide bound on milestones (see the .dip comment).
	for h, body := range rs.inner {
		if body["PickNextMilestone"] {
			t.Errorf("PickNextMilestone unexpectedly nested inside loop(%s)", h)
		}
	}
}

// buildProductSim scripts every build_product node so the engine walks the
// real graph: `milestones` picks, each failing TestMilestone `fixesPerMilestone`
// times before green. Everything else succeeds along the happy path, the
// review fan-out included, so a run that finishes reaches Done.
type buildProductSim struct {
	mu        sync.Mutex
	picked    int
	fixesLeft int
	escalated int
	// commitFailsOn lists milestone numbers on which CommitIfDirty returns
	// OutcomeFail. In the shipped graph that routes to the AbortRun terminal
	// (#640 A2); TestBuildProductFallbackLatchReArmsPerMilestone rebuilds the
	// pre-#640 strict-failure + graph-fallback shape in memory to keep
	// exercising the #642 one-shot latch. gateLabel is what the human gates
	// answer; "" makes reaching any gate a hard test failure.
	commitFailsOn map[int]bool
	gateLabel     string
}

func runBuildProductSim(t *testing.T, g *Graph, milestones, fixesPerMilestone int) (*buildProductSim, *EngineResult, error, map[string]int) {
	t.Helper()
	return runBuildProductSimWith(t, g, &buildProductSim{}, milestones, fixesPerMilestone)
}

func runBuildProductSimWith(t *testing.T, g *Graph, sim *buildProductSim, milestones, fixesPerMilestone int) (*buildProductSim, *EngineResult, error, map[string]int) {
	t.Helper()
	ok := func(extra map[string]string) Outcome {
		cu := map[string]string{"outcome": "success"}
		for k, v := range extra {
			cu[k] = v
		}
		return Outcome{Status: OutcomeSuccess, ContextUpdates: cu}
	}
	script := func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		sim.mu.Lock()
		defer sim.mu.Unlock()
		switch node.ID {
		case "ApprovePlan":
			return Outcome{Status: OutcomeSuccess, PreferredLabel: "approve"}, nil
		case "PickNextMilestone":
			if sim.picked >= milestones {
				return ok(map[string]string{"tool_stdout": "all-done"}), nil
			}
			sim.picked++
			sim.fixesLeft = fixesPerMilestone
			return ok(map[string]string{"tool_stdout": fmt.Sprintf("milestone %d", sim.picked)}), nil
		case "TestMilestone":
			if sim.fixesLeft > 0 {
				sim.fixesLeft--
				return Outcome{Status: OutcomeFail, ContextUpdates: map[string]string{"outcome": "fail", "tool_stdout": "red"}}, nil
			}
			return ok(map[string]string{"tool_stdout": "green"}), nil
		case "CommitIfDirty":
			if sim.commitFailsOn[sim.picked] {
				return Outcome{Status: OutcomeFail, ContextUpdates: map[string]string{"outcome": "fail", "tool_stderr": "git commit failed"}}, nil
			}
		case "CheckMilestoneOutputs":
			return ok(map[string]string{"tool_stdout": "outputs-present"}), nil
		case "EscalateMilestone", "EscalateReview", "OperatorDecision":
			sim.escalated++
			if sim.gateLabel != "" {
				return Outcome{Status: OutcomeSuccess, PreferredLabel: sim.gateLabel}, nil
			}
			return Outcome{Status: OutcomeFail, ContextUpdates: map[string]string{"outcome": "fail"}}, fmt.Errorf("sim: human gate %s reached", node.ID)
		}
		return ok(nil), nil
	}
	reg := NewHandlerRegistry()
	for _, name := range []string{"start", "exit", "codergen", "wait.human", "conditional", "parallel", "parallel.fan_in", "tool"} {
		reg.Register(&testHandler{name: name, executeFn: script})
	}

	var evMu sync.Mutex
	counts := map[string]int{}
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		switch evt.Type {
		case EventLoopRestart, EventRestartBudgetReset:
			evMu.Lock()
			counts[string(evt.Type)+":"+evt.NodeID]++
			if evt.Decision != nil && evt.Decision.FallbackLatchCleared {
				counts["latch_cleared:"+evt.NodeID]++
			}
			evMu.Unlock()
		}
	})
	result, err := NewEngine(g, reg, WithPipelineEventHandler(handler)).Run(context.Background())
	evMu.Lock()
	defer evMu.Unlock()
	return sim, result, err, counts
}

// TestBuildProductSixtyMilestonesWithFixes is the #643 repro (30 milestones ×
// 2 fixes died at milestone 17 with a shared TestMilestone budget) pushed to
// 60 milestones × 2 fixes under the shipped max_restarts. Each milestone's
// TestMilestone restarts are reset by the enclosing PickNextMilestone
// restart, so the run reaches Done with no gate ever hit.
func TestBuildProductSixtyMilestonesWithFixes(t *testing.T) {
	const milestones = 60
	g := loadBuildProduct(t)
	sim, result, err, counts := runBuildProductSim(t, g, milestones, 2)
	if err != nil {
		t.Fatalf("run failed after %d milestones: %v (events=%v)", sim.picked, err, counts)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("status = %q", result.Status)
	}
	if sim.picked != milestones || sim.escalated != 0 {
		t.Fatalf("picked=%d escalated=%d", sim.picked, sim.escalated)
	}
	if got := counts["loop_restart:TestMilestone"]; got != milestones*2 {
		t.Errorf("TestMilestone restarts = %d, want %d", got, milestones*2)
	}
	if got := counts["loop_restart:PickNextMilestone"]; got != milestones {
		// milestones-1 re-picks plus the final all-done pick.
		t.Errorf("PickNextMilestone restarts = %d, want %d", got, milestones)
	}
	if got := counts["restart_budget_reset:TestMilestone"]; got != milestones {
		t.Errorf("TestMilestone budget resets = %d, want %d", got, milestones)
	}
}

// TestBuildProductSixtyCleanMilestones: 60 milestones with no fix loops need
// 60 restarts of the outermost PickNextMilestone loop — the run-wide bound —
// which the shipped max_restarts must accommodate (the pre-#643 value of 50
// failed on milestone 51).
func TestBuildProductSixtyCleanMilestones(t *testing.T) {
	g := loadBuildProduct(t)
	sim, result, err, counts := runBuildProductSim(t, g, 60, 0)
	if err != nil {
		t.Fatalf("run failed after %d milestones: %v (max_restarts=%s)", sim.picked, err, g.Attrs["max_restarts"])
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("status = %q", result.Status)
	}
	if got := counts["loop_restart:TestMilestone"]; got != 0 {
		t.Errorf("unexpected TestMilestone restarts: %d", got)
	}
}

// TestBuildProductFixLoopStillBoundedPerMilestone: a milestone whose tests
// never go green still trips the engine ceiling inside that milestone — the
// per-iteration reset never widens the budget for a loop that does not exit.
func TestBuildProductFixLoopStillBoundedPerMilestone(t *testing.T) {
	g := loadBuildProduct(t)
	sim, _, err, counts := runBuildProductSim(t, g, 5, 1_000_000)
	if err == nil {
		t.Fatal("expected the non-converging fix loop to trip max restarts")
	}
	if !strings.Contains(err.Error(), "max restarts") {
		t.Fatalf("err = %v", err)
	}
	if sim.picked != 1 {
		t.Errorf("tripped after %d picks, want 1 (inside the first milestone)", sim.picked)
	}
	if got := counts["restart_budget_reset:TestMilestone"]; got != 0 {
		t.Errorf("no reset should fire while the fix loop never exits; got %d", got)
	}
}

// TestBuildProductFallbackLatchReArmsPerMilestone pins the #643 iteration
// boundary for the one-shot fallback latch (#642) on build_product's real
// loop nesting: a strict-failure node whose only route on OutcomeFail is a
// graph-level fallback to EscalateReview fails in milestone 1 (fallback
// taken, latch set, operator answers "retry"), then fails again in
// milestone 3. Between the two, MarkMilestoneDone -> PickNextMilestone is a
// counted restart of the enclosing milestone loop, which re-arms the latch,
// so the second failure reaches the gate again instead of halting terminal
// with `fallback_latched`. Expected: escalated=2, status=success.
//
// The SHIPPED graph no longer has that shape (#640 A2: no graph-level
// on_failure; CommitIfDirty routes `ctx.outcome = fail -> AbortRun` and the
// run ends fail — see TestBuildProduct640A2StrictFailuresNeverShip), so the
// pre-#640 shape is rebuilt in memory here: it is the engine's latch
// semantics on this loop structure that this test pins, not the workflow's
// routing choice.
func TestBuildProductFallbackLatchReArmsPerMilestone(t *testing.T) {
	loaded := loadBuildProduct(t)
	// Rebuild (the adjacency index is private, so re-add every edge but the
	// #640 one) with the graph-level fallback restored.
	g := NewGraph(loaded.Name)
	for k, v := range loaded.Attrs {
		g.Attrs[k] = v
	}
	g.Attrs["fallback_target"] = "EscalateReview"
	g.StartNode, g.ExitNode = loaded.StartNode, loaded.ExitNode
	for _, id := range loaded.NodeOrder {
		g.AddNode(loaded.Nodes[id])
	}
	for _, e := range loaded.Edges {
		if e.From == "CommitIfDirty" && e.To == "AbortRun" {
			continue
		}
		g.AddEdge(e)
	}
	if hasAnyConditionalEdge(g.OutgoingEdges("CommitIfDirty")) {
		t.Fatal("fixture: CommitIfDirty must be a strict-failure node (only unconditional edges) for the latch to apply")
	}
	sim := &buildProductSim{commitFailsOn: map[int]bool{1: true, 3: true}, gateLabel: "retry"}
	sim, result, err, counts := runBuildProductSimWith(t, g, sim, 5, 0)
	if err != nil {
		t.Fatalf("run failed after %d milestones (escalated=%d): %v — latch not re-armed on the milestone boundary?", sim.picked, sim.escalated, err)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("status = %q", result.Status)
	}
	if sim.escalated != 2 {
		t.Errorf("gate reached %d times, want 2 (milestones 1 and 3)", sim.escalated)
	}
	if sim.picked != 5 {
		t.Errorf("picked = %d, want 5", sim.picked)
	}
	// The latch is re-armed exactly when it was set and the milestone loop
	// advanced: after milestone 2 (set in 1) and after milestone 4 (set in
	// 3). The restart after milestone 3's re-decompose path clears nothing
	// (PickNextMilestone was un-completed by the fallback's clearDownstream,
	// so that re-entry is not a counted restart).
	if got := counts["restart_budget_reset:CommitIfDirty"]; got != 2 {
		t.Errorf("CommitIfDirty resets = %d, want 2", got)
	}
	if got := counts["latch_cleared:CommitIfDirty"]; got != 2 {
		t.Errorf("CommitIfDirty latch_cleared events = %d, want 2", got)
	}
}
