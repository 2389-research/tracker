// ABOUTME: Tests for loop-iteration scoping of per-target restart budgets (#643).
// ABOUTME: Covers natural-loop derivation, nested resets, run-wide outermost bound, and resume.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
)

// nestedLoopGraph builds the milestone-shaped graph the #643 sims run on:
//
//	s -> pick -> impl -> test -> verify -> done -> pick   (outer loop, header pick)
//	                     test <- fix <- test               (inner fix loop, header test)
//	verify --(all-done)--> end
//
// `pick` writes ctx.iteration; `test` fails N times per iteration (driven by
// the test handler), routing test -> fix -> test as a restart of `test`.
func nestedLoopGraph(maxRestarts string) *Graph {
	g := NewGraph("nested_loops")
	g.Attrs["max_restarts"] = maxRestarts
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "pick", Shape: "parallelogram", Label: "Pick"})
	g.AddNode(&Node{ID: "impl", Shape: "box", Label: "Impl"})
	g.AddNode(&Node{ID: "test", Shape: "parallelogram", Label: "Test"})
	g.AddNode(&Node{ID: "fix", Shape: "box", Label: "Fix"})
	g.AddNode(&Node{ID: "verify", Shape: "diamond", Label: "Verify"})
	g.AddNode(&Node{ID: "done", Shape: "parallelogram", Label: "MarkDone"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "pick"})
	g.AddEdge(&Edge{From: "pick", To: "impl"})
	g.AddEdge(&Edge{From: "impl", To: "test"})
	g.AddEdge(&Edge{From: "test", To: "verify", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "test", To: "fix", Condition: "outcome=fail"})
	g.AddEdge(&Edge{From: "fix", To: "test", Attrs: map[string]string{"restart": "true"}})
	g.AddEdge(&Edge{From: "verify", To: "end", Condition: "all_done=true"})
	g.AddEdge(&Edge{From: "verify", To: "done", Condition: "all_done=false"})
	g.AddEdge(&Edge{From: "done", To: "pick", Attrs: map[string]string{"restart": "true"}})
	return g
}

// milestoneSim wires the handlers for nestedLoopGraph: `iterations` outer
// passes, each of which fails `test` `failsPerIteration` times before going
// green. It returns the engine's result/error plus counters.
type milestoneSim struct {
	mu            sync.Mutex
	iteration     int
	testFailsLeft int
	testRuns      int
	pickRuns      int
}

func runMilestoneSim(t *testing.T, g *Graph, iterations, failsPerIteration int, opts ...EngineOption) (*milestoneSim, *EngineResult, error, []PipelineEvent) {
	t.Helper()
	sim := &milestoneSim{}
	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "tool",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			sim.mu.Lock()
			defer sim.mu.Unlock()
			switch node.ID {
			case "pick":
				sim.pickRuns++
				sim.iteration = sim.pickRuns
				sim.testFailsLeft = failsPerIteration
				return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"iteration": strconv.Itoa(sim.iteration)}}, nil
			case "test":
				sim.testRuns++
				if sim.testFailsLeft > 0 {
					sim.testFailsLeft--
					return Outcome{Status: OutcomeFail, ContextUpdates: map[string]string{"outcome": "fail"}}, nil
				}
				return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"outcome": "success"}}, nil
			case "done":
				return Outcome{Status: OutcomeSuccess}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})
	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			sim.mu.Lock()
			defer sim.mu.Unlock()
			allDone := "false"
			if sim.iteration >= iterations {
				allDone = "true"
			}
			return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"all_done": allDone}}, nil
		},
	})

	var evMu sync.Mutex
	var events []PipelineEvent
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		if evt.Type == EventLoopRestart || evt.Type == EventRestartBudgetReset {
			evMu.Lock()
			events = append(events, evt)
			evMu.Unlock()
		}
	})
	opts = append(opts, WithPipelineEventHandler(handler))
	engine := NewEngine(g, reg, opts...)
	result, err := engine.Run(context.Background())
	evMu.Lock()
	defer evMu.Unlock()
	return sim, result, err, events
}

func countEvents(events []PipelineEvent, typ PipelineEventType, nodeID string) int {
	n := 0
	for _, e := range events {
		if e.Type == typ && e.NodeID == nodeID {
			n++
		}
	}
	return n
}

// TestRestartScopesNaturalLoops pins the loop-set derivation: a header's loop
// is its natural loop (nodes that reach a back edge into the header without
// passing through it), so an inner fix loop is a strict subset of the
// enclosing milestone loop and the enclosing loop contains the inner header.
func TestRestartScopesNaturalLoops(t *testing.T) {
	g := nestedLoopGraph("5")
	rs := computeRestartScopes(g)

	want := map[string][]string{
		"pick": {"done", "fix", "impl", "test", "verify"},
		"test": {"fix"},
	}
	for header, inner := range want {
		got := rs.innerNodes(header)
		if fmt.Sprint(got) != fmt.Sprint(inner) {
			t.Errorf("loop(%s) inner = %v, want %v", header, got, inner)
		}
	}
	for _, id := range []string{"s", "impl", "fix", "verify", "done", "end"} {
		if got := rs.innerNodes(id); len(got) != 0 {
			t.Errorf("%s is not a loop header; inner = %v", id, got)
		}
	}
}

// TestRestartScopesSelfLoopAndIrreducible pins two edge shapes: a self-loop
// header has an empty inner set (nothing to reset), and an edge into a node
// that does NOT dominate its source (an irreducible re-entry) is not a back
// edge, so it never widens a loop set — the conservative run-wide behaviour.
func TestRestartScopesSelfLoopAndIrreducible(t *testing.T) {
	g := NewGraph("shapes")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "t", Shape: "box"})
	g.AddNode(&Node{ID: "u", Shape: "box"})
	g.AddNode(&Node{ID: "x", Shape: "box"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "t"})
	g.AddEdge(&Edge{From: "s", To: "x", Condition: "side=true"})
	g.AddEdge(&Edge{From: "t", To: "t", Condition: "again=true"})
	g.AddEdge(&Edge{From: "t", To: "u"})
	g.AddEdge(&Edge{From: "u", To: "t", Condition: "back=true"})
	g.AddEdge(&Edge{From: "u", To: "x"})
	g.AddEdge(&Edge{From: "x", To: "t", Condition: "reenter=true"})
	g.AddEdge(&Edge{From: "x", To: "end"})

	rs := computeRestartScopes(g)
	// t dominates u (only path to u is via t) → u->t is a back edge; t->t is
	// a self back edge contributing nothing; x is reachable from s directly so
	// t does not dominate x and x->t is an entry edge, not a back edge.
	if got := rs.innerNodes("t"); fmt.Sprint(got) != "[u]" {
		t.Errorf("loop(t) inner = %v, want [u]", got)
	}
	if got := rs.innerNodes("x"); len(got) != 0 {
		t.Errorf("x is not a loop header; inner = %v", got)
	}
}

// TestEngineInnerRestartBudgetResetsPerOuterIteration is the #643 repro: 60
// milestones each needing 2 fix passes (2 restarts of `test` per iteration)
// with a 100-restart ceiling. Without iteration scoping the shared
// RestartCounts[test] hits 100 on milestone 51; with scoping every outer
// restart of `pick` resets the inner budget, so the run completes.
func TestEngineInnerRestartBudgetResetsPerOuterIteration(t *testing.T) {
	const iterations = 60
	g := nestedLoopGraph("100")
	dir := t.TempDir()
	cpPath := filepath.Join(dir, "cp.json")

	sim, result, err, events := runMilestoneSim(t, g, iterations, 2, WithCheckpointPath(cpPath))
	if err != nil {
		t.Fatalf("run failed: %v (pick runs=%d, test runs=%d)", err, sim.pickRuns, sim.testRuns)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("status = %q, want success", result.Status)
	}
	if sim.pickRuns != iterations {
		t.Errorf("pick runs = %d, want %d", sim.pickRuns, iterations)
	}
	if sim.testRuns != iterations*3 {
		t.Errorf("test runs = %d, want %d (3 per iteration)", sim.testRuns, iterations*3)
	}
	if got := countEvents(events, EventLoopRestart, "test"); got != iterations*2 {
		t.Errorf("restarts of test = %d, want %d", got, iterations*2)
	}
	if got := countEvents(events, EventLoopRestart, "pick"); got != iterations-1 {
		t.Errorf("restarts of pick = %d, want %d", got, iterations-1)
	}
	// One reset of `test` per outer restart (its count was 2 each time).
	if got := countEvents(events, EventRestartBudgetReset, "test"); got != iterations-1 {
		t.Errorf("budget resets of test = %d, want %d", got, iterations-1)
	}
	for _, e := range events {
		if e.Type == EventRestartBudgetReset {
			if e.Decision == nil || e.Decision.RestartCount != 2 || e.Decision.ResetBy != "pick" {
				t.Fatalf("reset event payload = %+v, want RestartCount=2 ResetBy=pick", e.Decision)
			}
			break
		}
	}

	cp, err := LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	// The final iteration's 2 inner restarts are still on the books (no outer
	// restart followed them); the aggregate stays run-wide.
	if got := cp.RestartCountFor("test"); got != 2 {
		t.Errorf("RestartCounts[test] = %d, want 2 (last iteration only)", got)
	}
	if got := cp.RestartCountFor("pick"); got != iterations-1 {
		t.Errorf("RestartCounts[pick] = %d, want %d", got, iterations-1)
	}
	if want := iterations*2 + iterations - 1; cp.RestartCount != want {
		t.Errorf("aggregate RestartCount = %d, want %d", cp.RestartCount, want)
	}
}

// TestEngineOutermostLoopIsRunWideBound pins the documented semantic for the
// outermost loop: it has no enclosing header, so its count never resets and
// max_restarts is the run-wide bound the author must size. 60 clean
// iterations need 59 restarts of `pick`: max_restarts=59 completes,
// max_restarts=58 trips on the last iteration.
func TestEngineOutermostLoopIsRunWideBound(t *testing.T) {
	const iterations = 60
	sim, result, err, _ := runMilestoneSim(t, nestedLoopGraph("59"), iterations, 0)
	if err != nil {
		t.Fatalf("60 clean iterations with max_restarts=59 should complete: %v", err)
	}
	if result.Status != OutcomeSuccess || sim.pickRuns != iterations {
		t.Fatalf("status=%q pick runs=%d", result.Status, sim.pickRuns)
	}

	sim, _, err, _ = runMilestoneSim(t, nestedLoopGraph("58"), iterations, 0)
	if err == nil {
		t.Fatal("60 clean iterations with max_restarts=58 should trip the outermost bound")
	}
	if err.Error() != "max restarts (58) exceeded" {
		t.Errorf("err = %q", err)
	}
	if sim.pickRuns != iterations-1 {
		t.Errorf("pick runs before trip = %d, want %d", sim.pickRuns, iterations-1)
	}
}

// TestEngineInnerLoopStillTripsWithinOneIteration: a fix loop that never
// converges must still stop at max_restarts inside a single outer iteration —
// resets only happen when the ENCLOSING header restarts, never on the inner
// loop's own restarts.
func TestEngineInnerLoopStillTripsWithinOneIteration(t *testing.T) {
	sim, _, err, events := runMilestoneSim(t, nestedLoopGraph("3"), 10, 1000)
	if err == nil {
		t.Fatal("expected max-restarts failure on the non-converging inner loop")
	}
	if err.Error() != "max restarts (3) exceeded" {
		t.Errorf("err = %q", err)
	}
	if sim.pickRuns != 1 {
		t.Errorf("pick runs = %d, want 1 (tripped inside the first iteration)", sim.pickRuns)
	}
	if got := countEvents(events, EventLoopRestart, "test"); got != 3 {
		t.Errorf("restarts of test = %d, want 3", got)
	}
	if got := countEvents(events, EventRestartBudgetReset, "test"); got != 0 {
		t.Errorf("budget resets of test = %d, want 0", got)
	}
}

// threeDeepGraph nests a third loop inside the fix loop:
//
//	s -> outer -> mid -> inner -> innerCheck -(fail)-> inner
//	                                 innerCheck -(ok)-> midCheck -(fail)-> mid
//	                                 midCheck -(ok)-> outerCheck -(fail)-> outer
//	                                 outerCheck -(ok)-> end
func threeDeepGraph(maxRestarts string) *Graph {
	g := NewGraph("three_deep")
	g.Attrs["max_restarts"] = maxRestarts
	for _, id := range []string{"outer", "mid", "inner"} {
		g.AddNode(&Node{ID: id, Shape: "box"})
		g.AddNode(&Node{ID: id + "Check", Shape: "diamond"})
	}
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "outer"})
	g.AddEdge(&Edge{From: "outer", To: "mid"})
	g.AddEdge(&Edge{From: "mid", To: "inner"})
	g.AddEdge(&Edge{From: "inner", To: "innerCheck"})
	g.AddEdge(&Edge{From: "innerCheck", To: "inner", Condition: "outcome=fail"})
	g.AddEdge(&Edge{From: "innerCheck", To: "midCheck", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "midCheck", To: "mid", Condition: "outcome=fail"})
	g.AddEdge(&Edge{From: "midCheck", To: "outerCheck", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "outerCheck", To: "outer", Condition: "outcome=fail"})
	g.AddEdge(&Edge{From: "outerCheck", To: "end", Condition: "outcome=success"})
	return g
}

// TestEngineThreeDeepNestedLoopsReset: each level fails max_restarts times per
// enclosing iteration. Total restarts of `inner` = 2·3·3 = 18 ≫ 2, which only
// completes if the mid restart resets inner and the outer restart resets both.
func TestEngineThreeDeepNestedLoopsReset(t *testing.T) {
	g := threeDeepGraph("2")
	rs := computeRestartScopes(g)
	if got := rs.innerNodes("outer"); fmt.Sprint(got) != "[inner innerCheck mid midCheck outerCheck]" {
		t.Fatalf("loop(outer) inner = %v", got)
	}
	if got := rs.innerNodes("mid"); fmt.Sprint(got) != "[inner innerCheck midCheck]" {
		t.Fatalf("loop(mid) inner = %v", got)
	}
	if got := rs.innerNodes("inner"); fmt.Sprint(got) != "[innerCheck]" {
		t.Fatalf("loop(inner) inner = %v", got)
	}

	// Per-check fail budgets: each check fails exactly 2 times per fresh
	// enclosing iteration, then passes.
	var mu sync.Mutex
	fails := map[string]int{}
	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			defer mu.Unlock()
			if fails[node.ID] < 2 {
				fails[node.ID]++
				return Outcome{Status: OutcomeFail, ContextUpdates: map[string]string{"outcome": "fail"}}, nil
			}
			fails[node.ID] = 0
			// A passing check re-arms every check nested inside it for the
			// next enclosing iteration.
			switch node.ID {
			case "midCheck":
				fails["innerCheck"] = 0
			case "outerCheck":
				fails["innerCheck"], fails["midCheck"] = 0, 0
			}
			return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"outcome": "success"}}, nil
		},
	})
	restarts := map[string]int{}
	resets := map[string]int{}
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		mu.Lock()
		defer mu.Unlock()
		switch evt.Type {
		case EventLoopRestart:
			restarts[evt.NodeID]++
		case EventRestartBudgetReset:
			resets[evt.NodeID]++
		}
	})
	result, err := NewEngine(g, reg, WithPipelineEventHandler(handler)).Run(context.Background())
	if err != nil {
		t.Fatalf("three-deep run failed: %v (restarts=%v resets=%v)", err, restarts, resets)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("status = %q", result.Status)
	}
	mu.Lock()
	defer mu.Unlock()
	if restarts["outer"] != 2 || restarts["mid"] != 6 || restarts["inner"] != 18 {
		t.Errorf("restarts = %v, want outer=2 mid=6 inner=18", restarts)
	}
	// inner resets on every mid restart (6) and every outer restart (2); mid
	// resets on every outer restart (2); outer never resets.
	if resets["inner"] != 8 || resets["mid"] != 2 || resets["outer"] != 0 {
		t.Errorf("resets = %v, want inner=8 mid=2 outer=0", resets)
	}
}

// TestEngineRestartTargetAttrKeepsRunWideBudget: with the graph-level
// restart_target collapsing every restart onto one key, there is nothing
// nested to reset — the #603/#620 single-budget semantics are unchanged.
func TestEngineRestartTargetAttrKeepsRunWideBudget(t *testing.T) {
	g := nestedLoopGraph("5")
	g.Attrs["restart_target"] = "pick"
	// Each iteration's fix restart resolves to `pick` (re-running the whole
	// milestone), so the sim's fail counter re-arms on every pick and never
	// converges: 5 restarts of pick, then the breaker trips.
	sim, _, err, events := runMilestoneSim(t, g, 100, 1)
	if err == nil || err.Error() != "max restarts (5) exceeded" {
		t.Fatalf("err = %v, want max restarts (5) exceeded", err)
	}
	if got := countEvents(events, EventLoopRestart, "pick"); got != 5 {
		t.Errorf("restarts of pick = %d, want 5", got)
	}
	if got := countEvents(events, EventRestartBudgetReset, ""); got != 0 {
		t.Errorf("unexpected resets: %d", got)
	}
	if sim.pickRuns != 6 {
		t.Errorf("pick runs = %d, want 6", sim.pickRuns)
	}
}

// TestEngineRestartScopeResumeMidLoop: a checkpoint written mid-way through an
// inner loop resumes with its counts intact, and the reset still fires when
// the enclosing loop restarts after resume.
func TestEngineRestartScopeResumeMidLoop(t *testing.T) {
	g := nestedLoopGraph("3")
	dir := t.TempDir()
	cpPath := filepath.Join(dir, "cp.json")

	// Craft a checkpoint as if the run stopped right after `fix` on iteration
	// 1 with 2 inner restarts already spent: next node is `test`.
	cp := &Checkpoint{
		RunID:          "resume-643",
		CurrentNode:    "test",
		CompletedNodes: []string{"s", "pick", "impl", "fix"},
		RetryCounts:    map[string]int{},
		Context:        map[string]string{"iteration": "1"},
		RestartCount:   2,
		RestartCounts:  map[string]int{"test": 2},
	}
	if err := SaveCheckpoint(cp, cpPath); err != nil {
		t.Fatal(err)
	}

	// Resume: the sim's per-iteration fail counter starts at 0 (pick did not
	// run), so `test` passes immediately; verify routes to done -> pick, an
	// outer restart that must reset the RESUMED inner count (2 -> 0) — the
	// following iteration then spends 2 fresh inner restarts, which would
	// exceed max_restarts=3 had the resumed count survived.
	sim, result, err, events := runMilestoneSim(t, g, 1, 2, WithCheckpointPath(cpPath))
	if err != nil {
		t.Fatalf("resume failed: %v (pick runs=%d)", err, sim.pickRuns)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("status = %q", result.Status)
	}
	if got := countEvents(events, EventRestartBudgetReset, "test"); got != 1 {
		t.Errorf("resets of test after resume = %d, want 1", got)
	}
	for _, e := range events {
		if e.Type == EventRestartBudgetReset && (e.Decision == nil || e.Decision.RestartCount != 2) {
			t.Errorf("reset carried previous=%+v, want 2 (the resumed count)", e.Decision)
		}
	}
	loaded, err := LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.RestartCountFor("test"); got != 2 {
		t.Errorf("RestartCounts[test] after run = %d, want 2 (post-reset iteration only)", got)
	}
}

// TestEngineRestartScopeLegacyCheckpointLoads: a pre-#603 checkpoint (scalar
// restart_count only) still resumes; per-target budgets start fresh and the
// scoping code tolerates the nil map.
func TestEngineRestartScopeLegacyCheckpointLoads(t *testing.T) {
	g := nestedLoopGraph("3")
	dir := t.TempDir()
	cpPath := filepath.Join(dir, "cp.json")
	legacy := map[string]any{
		"run_id":          "legacy-643",
		"current_node":    "test",
		"completed_nodes": []string{"s", "pick", "impl", "fix"},
		"retry_counts":    map[string]int{},
		"context":         map[string]string{"iteration": "1"},
		"restart_count":   7,
	}
	raw, _ := json.Marshal(legacy)
	if err := os.WriteFile(cpPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, result, err, _ := runMilestoneSim(t, g, 2, 2, WithCheckpointPath(cpPath))
	if err != nil {
		t.Fatalf("legacy resume failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("status = %q", result.Status)
	}
}

// TestCheckpointResetRestartCount pins the checkpoint primitive: reset returns
// the previous per-target count, leaves the run-wide aggregate untouched, and
// is a no-op on a nil map / unknown target.
func TestCheckpointResetRestartCount(t *testing.T) {
	cp := &Checkpoint{}
	if got := cp.ResetRestartCount("x"); got != 0 {
		t.Errorf("nil-map reset = %d, want 0", got)
	}
	cp.IncrementRestart("a")
	cp.IncrementRestart("a")
	cp.IncrementRestart("b")
	if got := cp.ResetRestartCount("a"); got != 2 {
		t.Errorf("reset(a) = %d, want 2", got)
	}
	if got := cp.RestartCountFor("a"); got != 0 {
		t.Errorf("RestartCountFor(a) after reset = %d, want 0", got)
	}
	if got := cp.RestartCountFor("b"); got != 1 {
		t.Errorf("RestartCountFor(b) = %d, want 1 (untouched)", got)
	}
	if cp.RestartCount != 3 {
		t.Errorf("aggregate RestartCount = %d, want 3 (never reset)", cp.RestartCount)
	}
	keys := make([]string, 0, len(cp.RestartCounts))
	for k := range cp.RestartCounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if fmt.Sprint(keys) != "[b]" {
		t.Errorf("reset should drop the key: %v", keys)
	}
}

// TestCheckpointClearFallbackTaken pins the latch primitive (#643): clearing
// reports whether the latch was set, is a no-op without gate state, and
// leaves the rest of the node's GateState alone.
func TestCheckpointClearFallbackTaken(t *testing.T) {
	cp := &Checkpoint{}
	if cp.ClearFallbackTaken("x") {
		t.Error("clear on a node with no state should report false")
	}
	cp.MarkFallbackTaken("a")
	cp.SetGateOutcome("a", "fail")
	if !cp.ClearFallbackTaken("a") {
		t.Error("clear on a latched node should report true")
	}
	if cp.IsFallbackTaken("a") {
		t.Error("latch still set after clear")
	}
	if cp.ClearFallbackTaken("a") {
		t.Error("second clear should report false")
	}
	if got := cp.GateOutcome("a"); got != "fail" {
		t.Errorf("GateOutcome = %q, want fail (untouched by latch clear)", got)
	}
}
