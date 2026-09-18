// ABOUTME: Tests for #653 — a failed node whose conditional edges all miss consults
// ABOUTME: node fallback_target then graph on_failure (dippin's failure cascade) before halting.
package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// cascadeGraph builds `Start -> Build -> Done when ctx.outcome = success` plus a
// Cleanup safety node (Cleanup -> Done). With nodeFB / graphFB set, Build's
// failure has a fallback at the node / graph level respectively. Build carries
// ONLY a success-conditional edge, so a fail outcome matches nothing — the exact
// shape #653 dead-stopped on.
func cascadeGraph(nodeFB, graphFB string) *Graph {
	g := NewGraph("cascade_653")
	g.AddNode(&Node{ID: "Start", Shape: "Mdiamond", Handler: "start"})
	build := &Node{ID: "Build", Shape: "box", Handler: "tool", Attrs: map[string]string{}}
	if nodeFB != "" {
		build.Attrs["fallback_target"] = nodeFB
	}
	g.AddNode(build)
	g.AddNode(&Node{ID: "Cleanup", Shape: "box", Handler: "tool"})
	g.AddNode(&Node{ID: "Escalate", Shape: "box", Handler: "tool"})
	g.AddNode(&Node{ID: "Done", Shape: "Msquare", Handler: "exit"})
	g.AddEdge(&Edge{From: "Start", To: "Build"})
	g.AddEdge(&Edge{From: "Build", To: "Done", Condition: "ctx.outcome = success"})
	g.AddEdge(&Edge{From: "Cleanup", To: "Done"})
	g.AddEdge(&Edge{From: "Escalate", To: "Done"})
	if graphFB != "" {
		g.Attrs["fallback_target"] = graphFB // what the adapter writes for defaults.on_failure (#309)
	}
	return g
}

// runCascade runs g with per-node scripted outcomes (unscripted nodes succeed)
// and returns the visited path, captured events, and the run result/error.
func runCascade(t *testing.T, g *Graph, perNode map[string]func(visit int) Outcome) ([]string, []PipelineEvent, *EngineResult, error) {
	t.Helper()
	var mu sync.Mutex
	var events []PipelineEvent
	seen := map[string]int{}
	exec := func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		mu.Lock()
		defer mu.Unlock()
		n := seen[node.ID]
		seen[node.ID]++
		if fn, ok := perNode[node.ID]; ok {
			return fn(n), nil
		}
		return bpOK(""), nil
	}
	reg := NewHandlerRegistry()
	for _, name := range []string{"start", "exit", "tool", "codergen"} {
		reg.Register(&testHandler{name: name, executeFn: exec})
	}
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		mu.Lock()
		events = append(events, evt)
		mu.Unlock()
	})
	res, err := NewEngine(g, reg, WithPipelineEventHandler(handler)).Run(context.Background())
	var path []string
	if res != nil && res.Trace != nil {
		for _, e := range res.Trace.Entries {
			path = append(path, e.NodeID)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	return path, append([]PipelineEvent(nil), events...), res, err
}

func alwaysFail(int) Outcome { return bpFail("build broke") }

// withEdges replaces g's edge list and rebuilds the adjacency indexes (slicing
// g.Edges alone leaves Graph's outgoing index stale).
func withEdges(t *testing.T, g *Graph, edges ...*Edge) *Graph {
	t.Helper()
	g.Edges = edges
	g2, err := PrepareForExecution(g)
	if err != nil {
		t.Fatalf("PrepareForExecution: %v", err)
	}
	return g2
}

// decisionEdges returns the decision_edge events from->to with the given priority.
func decisionEdges(events []PipelineEvent, from, to, priority string) []PipelineEvent {
	var out []PipelineEvent
	for _, de := range findEvents(events, EventDecisionEdge) {
		if de.Decision != nil && de.Decision.EdgeFrom == from && de.Decision.EdgeTo == to && de.Decision.EdgePriority == priority {
			out = append(out, de)
		}
	}
	return out
}

// The concrete #653 case: `Build -> Done when ctx.outcome = success` with
// `defaults.on_failure: Cleanup`. A failed Build routes to Cleanup instead of
// dead-stopping with "no matching edges".
func TestEngine_FailureCascade_GraphOnFailureRescuesUnmatchedFail(t *testing.T) {
	g := cascadeGraph("", "Cleanup")
	path, events, res, err := runCascade(t, g, map[string]func(int) Outcome{"Build": alwaysFail})
	if err != nil {
		t.Fatalf("run: %v (path %v)", err, path)
	}
	if got, want := strings.Join(path, ","), "Start,Build,Cleanup,Done"; got != want {
		t.Fatalf("path = %s, want %s", got, want)
	}
	if res.Status != OutcomeSuccess {
		t.Errorf("status = %s, want success (Cleanup recovered the run)", res.Status)
	}

	fts := findEvents(events, EventConditionalFallthrough)
	if len(fts) != 1 {
		t.Fatalf("conditional_fallthrough events = %d, want 1", len(fts))
	}
	ft := fts[0]
	if ft.NodeID != "Build" || ft.Decision == nil || ft.Decision.EdgeTo != "Cleanup" {
		t.Fatalf("fallthrough malformed: %+v", ft)
	}
	if ft.Decision.EdgePriority != EdgePriorityFallback {
		t.Errorf("fallthrough EdgePriority = %q, want %q", ft.Decision.EdgePriority, EdgePriorityFallback)
	}
	if len(ft.Decision.ConditionsTried) != 1 || ft.Decision.ConditionsTried[0].Condition != "ctx.outcome = success" {
		t.Errorf("ConditionsTried = %+v, want the single missed success guard", ft.Decision.ConditionsTried)
	}
	if !strings.Contains(ft.Message, "on_failure") && !strings.Contains(ft.Message, "fallback") {
		t.Errorf("fallthrough message should name the fallback route: %q", ft.Message)
	}
	if n := len(decisionEdges(events, "Build", "Cleanup", EdgePriorityFallback)); n != 1 {
		t.Errorf("decision_edge Build -> Cleanup via fallback = %d, want 1", n)
	}
	for _, de := range findEvents(events, EventDecisionEdge) {
		if de.Decision != nil && de.Decision.EdgePriority == EdgePriorityElse {
			t.Errorf("else must never be involved in a failure route: %+v", de)
		}
	}
}

// Node-level fallback_target beats graph-level on_failure (cascade step 3 before 4).
func TestEngine_FailureCascade_NodeFallbackBeatsGraphOnFailure(t *testing.T) {
	g := cascadeGraph("Escalate", "Cleanup")
	path, events, _, err := runCascade(t, g, map[string]func(int) Outcome{"Build": alwaysFail})
	if err != nil {
		t.Fatalf("run: %v (path %v)", err, path)
	}
	if got, want := strings.Join(path, ","), "Start,Build,Escalate,Done"; got != want {
		t.Fatalf("path = %s, want %s", got, want)
	}
	if n := len(decisionEdges(events, "Build", "Escalate", EdgePriorityFallback)); n != 1 {
		t.Errorf("decision_edge Build -> Escalate via fallback = %d, want 1", n)
	}
}

// A fail outcome that an explicit `on fail` edge handles takes that edge
// (cascade step 1) — the fallback is never consulted. Unchanged behavior.
func TestEngine_FailureCascade_ExplicitFailEdgeWins(t *testing.T) {
	g := cascadeGraph("", "Cleanup")
	g.AddEdge(&Edge{From: "Build", To: "Escalate", Condition: "ctx.outcome = fail"})
	path, events, _, err := runCascade(t, g, map[string]func(int) Outcome{"Build": alwaysFail})
	if err != nil {
		t.Fatalf("run: %v (path %v)", err, path)
	}
	if got, want := strings.Join(path, ","), "Start,Build,Escalate,Done"; got != want {
		t.Fatalf("path = %s, want %s", got, want)
	}
	if n := len(findEvents(events, EventConditionalFallthrough)); n != 0 {
		t.Errorf("no fallthrough expected when the fail guard matched, got %d", n)
	}
	if n := len(decisionEdges(events, "Build", "Escalate", "condition")); n != 1 {
		t.Errorf("decision_edge Build -> Escalate via condition = %d, want 1", n)
	}
}

// Without any fallback the unmatched failure still halts with the
// no-matching-edges error — cascade step 5.
func TestEngine_FailureCascade_NoFallbackStillHalts(t *testing.T) {
	g := cascadeGraph("", "")
	path, events, _, err := runCascade(t, g, map[string]func(int) Outcome{"Build": alwaysFail})
	if err == nil || !strings.Contains(err.Error(), "no matching edges") {
		t.Fatalf("err = %v (path %v), want the no-matching-edges halt", err, path)
	}
	if n := len(findEvents(events, EventConditionalFallthrough)); n != 0 {
		t.Errorf("no fallthrough expected on a halt, got %d", n)
	}
}

// One-shot latch (#642): once Build's fallback routed to Cleanup, a loop that
// re-enters Build and fails again must hard-halt, not re-route forever.
func TestEngine_FailureCascade_SecondFailureAfterFallbackHalts(t *testing.T) {
	g := cascadeGraph("", "Cleanup")
	// Cleanup loops back into Build (a fix loop) instead of finishing.
	g.Attrs["max_restarts"] = "5"
	g = withEdges(t, g,
		&Edge{From: "Start", To: "Build"},
		&Edge{From: "Build", To: "Done", Condition: "ctx.outcome = success"},
		&Edge{From: "Cleanup", To: "Build"},
		&Edge{From: "Escalate", To: "Done"},
	)

	visits := 0
	path, events, _, err := runCascade(t, g, map[string]func(int) Outcome{
		"Build": func(int) Outcome { visits++; return bpFail("still broken") },
	})
	if err == nil {
		t.Fatalf("expected the second Build failure to halt, got path %v", path)
	}
	if !strings.Contains(err.Error(), "no matching edges") {
		t.Errorf("err = %v, want the no-matching-edges halt", err)
	}
	if visits != 2 {
		t.Errorf("Build ran %d times, want exactly 2 (fallback taken once, then latched)", visits)
	}
	if n := len(findEvents(events, EventFallbackLatched)); n != 1 {
		t.Errorf("fallback_latched events = %d, want 1", n)
	}
	if n := len(decisionEdges(events, "Build", "Cleanup", EdgePriorityFallback)); n != 1 {
		t.Errorf("decision_edge Build -> Cleanup via fallback = %d, want exactly 1", n)
	}
}

// Self-target (#650): an on_failure that resolves to the failing node itself is
// no fallback — the node halts plainly, no latch, no fallback_latched event.
func TestEngine_FailureCascade_SelfTargetIsNoop(t *testing.T) {
	g := cascadeGraph("", "Build")
	visits := 0
	path, events, _, err := runCascade(t, g, map[string]func(int) Outcome{
		"Build": func(int) Outcome { visits++; return bpFail("x") },
	})
	if err == nil || !strings.Contains(err.Error(), "no matching edges") {
		t.Fatalf("err = %v (path %v), want the plain no-matching-edges halt", err, path)
	}
	if visits != 1 {
		t.Errorf("Build ran %d times, want 1 (self-fallback must not re-enter)", visits)
	}
	if n := len(findEvents(events, EventFallbackLatched)); n != 0 {
		t.Errorf("self-target must not latch: fallback_latched events = %d", n)
	}
	if n := len(findEvents(events, EventConditionalFallthrough)); n != 0 {
		t.Errorf("self-target must not emit a fallthrough: got %d", n)
	}
}

// The fallback hop is recorded like any other edge selection so a resume
// replays Build -> Cleanup instead of re-evaluating Build's guards.
func TestEngine_FailureCascade_RecordsEdgeSelection(t *testing.T) {
	g := cascadeGraph("", "Cleanup")
	dir := t.TempDir()
	reg := newTestRegistryWithOutcomes(map[string]Outcome{"Build": bpFail("x")})
	engine := NewEngine(g, reg, WithCheckpointPath(dir+"/checkpoint.json"))
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	cp, err := LoadCheckpoint(dir + "/checkpoint.json")
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	if to, ok := cp.GetEdgeSelection("Build"); !ok || to != "Cleanup" {
		t.Errorf("EdgeSelections[Build] = %q,%v; want Cleanup", to, ok)
	}
	if !cp.IsFallbackTaken("Build") {
		t.Error("Build's one-shot fallback latch should be set")
	}
	if origin := cp.FallbackOrigin("Cleanup"); origin != "Build" {
		t.Errorf("FallbackOrigin(Cleanup) = %q, want Build", origin)
	}
}

// Pure strict failure (ALL edges unconditional) is untouched: the halt copy and
// the strict-failure fallback path behave exactly as before, with no
// conditional_fallthrough (there were no conditions to fall through).
func TestEngine_FailureCascade_PureStrictFailureUnchanged(t *testing.T) {
	g := withEdges(t, cascadeGraph("", "Cleanup"),
		&Edge{From: "Start", To: "Build"},
		&Edge{From: "Build", To: "Done"},
		&Edge{From: "Cleanup", To: "Done"},
		&Edge{From: "Escalate", To: "Done"},
	)
	path, events, _, err := runCascade(t, g, map[string]func(int) Outcome{"Build": alwaysFail})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := strings.Join(path, ","), "Start,Build,Cleanup,Done"; got != want {
		t.Fatalf("path = %s, want %s", got, want)
	}
	if n := len(findEvents(events, EventConditionalFallthrough)); n != 0 {
		t.Errorf("strict-failure fallback must not emit conditional_fallthrough, got %d", n)
	}
}

// #649 fixture extended: `else` never catches a failure even when it is the only
// default around (halt), while a graph on_failure DOES rescue the same failure —
// via the fallback priority, never the else one.
func TestEngine_FailureCascade_ElseNeverCatchesFailure_OnFailureDoes(t *testing.T) {
	fail := Outcome{Status: OutcomeFail, ContextUpdates: map[string]string{"outcome": "fail"}}

	// else only → halt (unchanged from #649).
	g, _ := loadElseFixture(t)
	path, events, err := runElseFixture(t, g, map[string]Outcome{"Classify": fail})
	if err == nil || !strings.Contains(err.Error(), "no matching edges") {
		t.Fatalf("else-only: err = %v (path %v), want the no-matching-edges halt", err, path)
	}
	if n := len(findEvents(events, EventConditionalFallthrough)); n != 0 {
		t.Fatalf("else-only: no fallthrough expected on a failure halt, got %d", n)
	}

	// else + on_failure → on_failure wins, else stays out of the failure path.
	g, _ = loadElseFixture(t)
	g.Attrs["fallback_target"] = "Passthrough"
	path, events, err = runElseFixture(t, g, map[string]Outcome{"Classify": fail})
	if err != nil {
		t.Fatalf("with on_failure: run: %v (path %v)", err, path)
	}
	if got, want := strings.Join(path, ","), "Setup,Classify,Passthrough,Done"; got != want {
		t.Fatalf("with on_failure: path = %s, want %s", got, want)
	}
	for _, id := range path {
		if id == "Fallback" {
			t.Fatalf("else intercepted a genuine failure: path = %v", path)
		}
	}
	var fts []PipelineEvent
	for _, ft := range findEvents(events, EventConditionalFallthrough) {
		if ft.NodeID == "Classify" {
			fts = append(fts, ft)
		}
	}
	if len(fts) != 1 || fts[0].Decision == nil || fts[0].Decision.EdgePriority != EdgePriorityFallback {
		t.Fatalf("with on_failure: want exactly one Classify fallthrough with priority %q, got %+v", EdgePriorityFallback, fts)
	}
	for _, de := range findEvents(events, EventDecisionEdge) {
		if de.Decision != nil && de.Decision.EdgePriority == EdgePriorityElse {
			t.Errorf("else must not appear on a failure route: %+v", de)
		}
	}
}

// Table test against dippin's documented cascade (docs/edges.md § Failure
// Handling, v0.73.0 lines 268-276 and 289-298): explicit fail edge → (bounded
// retry, exercised elsewhere) → node fallback_target → graph on_failure → halt;
// `else` is not in the path. dippin's simulator has no failure channel, so the
// doc table is the oracle.
func TestEngine_FailureCascade_MatchesDippinDocCascade(t *testing.T) {
	cases := []struct {
		name         string
		failEdge     bool
		nodeFB       string
		graphFB      string
		withElse     bool
		wantNext     string // "" = halt
		wantPriority string
	}{
		{name: "1 explicit fail edge beats everything", failEdge: true, nodeFB: "Cleanup", graphFB: "Cleanup", withElse: true, wantNext: "Escalate", wantPriority: "condition"},
		{name: "3 node fallback_target beats graph on_failure", nodeFB: "Escalate", graphFB: "Cleanup", withElse: true, wantNext: "Escalate", wantPriority: EdgePriorityFallback},
		{name: "4 graph on_failure when node has none", graphFB: "Cleanup", withElse: true, wantNext: "Cleanup", wantPriority: EdgePriorityFallback},
		{name: "5 halt when nothing routes the failure", wantNext: ""},
		{name: "else is not in the failure path", withElse: true, wantNext: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := cascadeGraph(tc.nodeFB, tc.graphFB)
			if tc.failEdge {
				g.AddEdge(&Edge{From: "Build", To: "Escalate", Condition: "ctx.outcome = fail"})
			}
			if tc.withElse {
				g.ElseTarget = "Cleanup"
			}
			path, events, _, err := runCascade(t, g, map[string]func(int) Outcome{"Build": alwaysFail})
			if tc.wantNext == "" {
				if err == nil || !strings.Contains(err.Error(), "no matching edges") {
					t.Fatalf("err = %v (path %v), want halt", err, path)
				}
				if len(path) > 2 {
					t.Fatalf("halt must not advance past Build: path %v", path)
				}
				return
			}
			if err != nil {
				t.Fatalf("run: %v (path %v)", err, path)
			}
			if len(path) < 3 || path[2] != tc.wantNext {
				t.Fatalf("path = %v, want Build -> %s", path, tc.wantNext)
			}
			if n := len(decisionEdges(events, "Build", tc.wantNext, tc.wantPriority)); n != 1 {
				t.Errorf("decision_edge Build -> %s via %s = %d, want 1", tc.wantNext, tc.wantPriority, n)
			}
		})
	}
}

// Real build_product.dip: every conditional-only node there already handles
// `fail` explicitly, so to exercise the cascade on the real graph we drop the
// `on fail` guard from CheckReviewsComplete (leaving only its success guard)
// and force it to fail. Pre-#653 that dead-stopped with "no matching edges";
// now `defaults.on_failure: AbortRun` catches it — and the run still never ships.
func TestBuildProduct653UnmatchedFailureRoutesToAbortRun(t *testing.T) {
	g := loadBuildProduct(t)
	const node = "CheckReviewsComplete"
	var kept []*Edge
	dropped := 0
	for _, e := range g.Edges {
		if e.From == node && strings.Contains(e.Condition, "fail") {
			dropped++
			continue
		}
		kept = append(kept, e)
	}
	if dropped != 1 {
		t.Fatalf("expected to drop exactly one fail edge from %s, dropped %d: %v", node, dropped, describeEdges(g, node))
	}
	g = withEdges(t, g, kept...)
	if fb := g.Attrs["fallback_target"]; fb != "AbortRun" {
		t.Fatalf("build_product defaults.on_failure = %q, want AbortRun", fb)
	}

	sim := &bp640Sim{script: map[string]func(int) Outcome{
		"PickNextMilestone": oneMilestone(),
		node:                func(int) Outcome { return bpFail("ERROR: reviews incomplete") },
	}, gate: "accept"}
	res, err := sim.run(t, g)
	if !sim.visited(node) {
		t.Fatalf("sim never reached %s: visits=%v", node, sim.visits)
	}
	if err == nil || res == nil || res.Status != OutcomeFail {
		t.Fatalf("run must end fail: err=%v status=%v visits=%v", err, statusOf(res), sim.visits)
	}
	if err != nil && strings.Contains(err.Error(), "no matching edges") {
		t.Errorf("failure dead-stopped instead of routing via on_failure: %v", err)
	}
	if !sim.visited("AbortRun") {
		t.Errorf("%s failure did not route to AbortRun via defaults.on_failure: visits=%v", node, sim.visits)
	}
	for _, shipped := range []string{"FinalCommit", "Done"} {
		if sim.visited(shipped) {
			t.Errorf("%s failure reached %s — shipped: visits=%v", node, shipped, sim.visits)
		}
	}
	if last := sim.visits[len(sim.visits)-1]; last != "AbortRun" {
		t.Errorf("run continued past the abort terminal: last visited = %s", last)
	}
}

// ─── #653 follow-up: cascade step 5 is a real terminal halt ────────────────

// A latched cascade halt (fallback consumed earlier, Build fails again) is a
// first-class dead stop: stage_failed with the reason and the consumed
// fallback named, HaltedAt persisted, an OutcomeFail result alongside the
// error, and the `no matching edges` diagnostic still in the error text.
func TestEngine_FailureCascade_LatchedHaltIsTerminal(t *testing.T) {
	g := cascadeGraph("", "Cleanup")
	g.Attrs["max_restarts"] = "5"
	g = withEdges(t, g,
		&Edge{From: "Start", To: "Build"},
		&Edge{From: "Build", To: "Done", Condition: "ctx.outcome = success"},
		&Edge{From: "Cleanup", To: "Build"},
		&Edge{From: "Escalate", To: "Done"},
	)
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	var events []PipelineEvent
	reg := newTestRegistryWithOutcomes(map[string]Outcome{"Build": {Status: OutcomeFail, FailureReason: "exit 2: build broke", ContextUpdates: map[string]string{"outcome": "fail"}}})
	res, err := NewEngine(g, reg, WithCheckpointPath(cpPath),
		WithPipelineEventHandler(PipelineEventHandlerFunc(func(evt PipelineEvent) { events = append(events, evt) }))).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no matching edges") || !strings.Contains(err.Error(), "already taken") {
		t.Fatalf("err = %v, want a halt naming the consumed fallback that still carries the no-matching-edges diagnostic", err)
	}
	if res == nil || res.Status != OutcomeFail {
		t.Fatalf("status = %s, want an OutcomeFail result (a recognized terminal, not a bare error)", statusOf(res))
	}
	halts := stageFailedFor(events, "Build")
	last := halts[len(halts)-1]
	if !strings.Contains(last.Message, "stopping pipeline") || !strings.Contains(last.Message, `"Cleanup"`) {
		t.Errorf("terminal stage_failed = %q, want the halt naming the consumed fallback", last.Message)
	}
	if last.Err == nil || !strings.Contains(last.Err.Error(), "exit 2") {
		t.Errorf("terminal stage_failed must carry the failure reason, got %v", last.Err)
	}
	cp := loadCP(t, cpPath)
	if cp.HaltedAt != "Build" {
		t.Errorf("HaltedAt = %q, want Build", cp.HaltedAt)
	}
	if !cp.IsFallbackTaken("Build") {
		t.Error("Build's one-shot latch must stay set across the halt")
	}
}

// The no-fallback cascade halt (`Build -> Done when success`, nothing routes
// fail) is the same terminal: stage_failed + HaltedAt + a result, and a
// resume re-enters Build in place (it was reached by an ordinary edge, so
// there is nothing to rewind to).
func TestEngine_FailureCascade_NoFallbackHaltIsTerminalAndResumes(t *testing.T) {
	g := cascadeGraph("", "")
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	var events []PipelineEvent
	handler := WithPipelineEventHandler(PipelineEventHandlerFunc(func(evt PipelineEvent) { events = append(events, evt) }))
	reg := newTestRegistryWithOutcomes(map[string]Outcome{"Build": bpFail("boom")})
	res, err := NewEngine(g, reg, WithCheckpointPath(cpPath), handler).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no matching edges") || res == nil || res.Status != OutcomeFail {
		t.Fatalf("err=%v status=%s, want the terminal no-matching-edges halt with a result", err, statusOf(res))
	}
	if n := len(stageFailedFor(events, "Build")); n != 2 {
		t.Errorf("stage_failed for Build = %d, want failed + terminal halt", n)
	}
	if cp := loadCP(t, cpPath); cp.HaltedAt != "Build" {
		t.Errorf("HaltedAt = %q, want Build", cp.HaltedAt)
	}
	// Resume with Build fixed: Build re-runs, then Done.
	visits, _, res2, err2 := runCascadeVisits(t, g, cpPath, nil)
	if err2 != nil || res2 == nil || res2.Status != OutcomeSuccess {
		t.Fatalf("resume: err=%v status=%s visits=%v", err2, statusOf(res2), visits)
	}
	if got := strings.Join(visits, ","); got != "Build,Done" {
		t.Errorf("resume visits = %s, want Build,Done (halted node re-run in place)", got)
	}
}

// runCascadeVisits runs g (resuming from cpPath) with scripted per-node
// outcomes and returns the handler visit order.
func runCascadeVisits(t *testing.T, g *Graph, cpPath string, perNode map[string]Outcome) ([]string, []PipelineEvent, *EngineResult, error) {
	t.Helper()
	var visits []string
	var events []PipelineEvent
	reg := NewHandlerRegistry()
	for _, name := range []string{"start", "exit", "tool", "codergen"} {
		reg.Register(&testHandler{name: name, executeFn: func(_ context.Context, node *Node, _ *PipelineContext) (Outcome, error) {
			visits = append(visits, node.ID)
			if o, ok := perNode[node.ID]; ok {
				return o, nil
			}
			return bpOK(""), nil
		}})
	}
	res, err := NewEngine(g, reg, WithCheckpointPath(cpPath),
		WithPipelineEventHandler(PipelineEventHandlerFunc(func(evt PipelineEvent) { events = append(events, evt) }))).Run(context.Background())
	return visits, events, res, err
}

// A cascade halt on a dirty working tree preserves the in-flight code to a
// WIP ref before the halt, exactly as the strict-failure halt does (#302/#488).
func TestEngine_FailureCascade_HaltPreservesWorkingTreeWIP(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	gitOrFail(t, dir, "init")
	gitOrFail(t, dir, "config", "user.email", "t@t")
	gitOrFail(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOrFail(t, dir, "add", "-A")
	gitOrFail(t, dir, "commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v2-inflight\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := cascadeGraph("", "")
	reg := newTestRegistryWithOutcomes(map[string]Outcome{"Build": bpFail("boom")})
	res, err := NewEngine(g, reg, WithWorkDir(dir)).Run(context.Background())
	if err == nil || res == nil || res.Status != OutcomeFail {
		t.Fatalf("want the cascade halt, got err=%v status=%s", err, statusOf(res))
	}
	ref := "refs/tracker/wip/" + res.RunID + "/Build"
	if _, err := runGitDir(dir, gitSafeEnv(), "rev-parse", "--verify", ref); err != nil {
		t.Fatalf("expected WIP ref %s after the cascade halt", ref)
	}
	if got := gitOrFail(t, dir, "show", ref+":a.txt"); got != "v2-inflight" {
		t.Errorf("snapshot a.txt = %q, want the in-flight version", got)
	}
}

// ─── #653 follow-up: strict-failure fallback parity on resume ─────────────

// A strict-failure fallback hop (Build has only an unconditional edge, graph
// on_failure: Cleanup) emits decision_edge with priority fallback and is
// recorded in EdgeSelections. The reviewer's consequence of the gap: a resume
// that re-walks the completed Build (resumeSkipNode) replays the stored
// selection; without it, selectEdge would re-run and take Build -> Done,
// silently skipping Cleanup.
func TestEngine_StrictFallback_RecordsHopAndReplaysOnResume(t *testing.T) {
	g := withEdges(t, cascadeGraph("", "Cleanup"),
		&Edge{From: "Start", To: "Build"},
		&Edge{From: "Build", To: "Done"},
		&Edge{From: "Cleanup", To: "Done"},
		&Edge{From: "Escalate", To: "Done"},
	)
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	visits, events, res, err := runCascadeVisits(t, g, cpPath, map[string]Outcome{"Build": bpFail("boom")})
	if err != nil || res == nil || res.Status != OutcomeSuccess || strings.Join(visits, ",") != "Start,Build,Cleanup,Done" {
		t.Fatalf("first run: err=%v status=%s visits=%v", err, statusOf(res), visits)
	}
	if n := len(decisionEdges(events, "Build", "Cleanup", EdgePriorityFallback)); n != 1 {
		t.Errorf("decision_edge Build -> Cleanup via fallback = %d, want exactly 1 (no double emit)", n)
	}
	if n := len(findEvents(events, EventConditionalFallthrough)); n != 0 {
		t.Errorf("pure strict failure tried no guards; conditional_fallthrough = %d, want 0", n)
	}
	cp := loadCP(t, cpPath)
	if to, ok := cp.GetEdgeSelection("Build"); !ok || to != "Cleanup" {
		t.Fatalf("EdgeSelections[Build] = %q,%v, want Cleanup", to, ok)
	}

	// Re-walk the completed Build on resume: CurrentNode = Build (completed),
	// Cleanup and Done not yet done. resumeSkipNode must replay Build -> Cleanup.
	cp.CurrentNode = "Build"
	cp.ClearCompleted("Cleanup")
	cp.ClearCompleted("Done")
	if err := SaveCheckpoint(cp, cpPath); err != nil {
		t.Fatal(err)
	}
	visits, _, res, err = runCascadeVisits(t, g, cpPath, nil)
	if err != nil || res == nil || res.Status != OutcomeSuccess {
		t.Fatalf("resume: err=%v status=%s visits=%v", err, statusOf(res), visits)
	}
	if got := strings.Join(visits, ","); got != "Cleanup,Done" {
		t.Errorf("resume visits = %s, want Cleanup,Done (replayed the recorded fallback hop, not Build -> Done)", got)
	}
}
