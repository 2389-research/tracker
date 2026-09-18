// ABOUTME: Tests for the section-level `else -> Node` default (#649): the adapter
// ABOUTME: stores ir.Workflow.ElseTarget and edge selection routes to it exactly as dippin simulate does.
package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/2389-research/dippin-lang/parser"
	"github.com/2389-research/dippin-lang/simulate"
)

const elseFixturePath = "testdata/else_target.dip"

func loadElseFixture(t *testing.T) (*Graph, string) {
	t.Helper()
	src, err := os.ReadFile(elseFixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	g, diags, err := LoadDippinWorkflow(string(src), elseFixturePath)
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v\n%v", err, diags)
	}
	return g, string(src)
}

// elseRegistry stubs every handler as a success whose ContextUpdates come from
// perNode; Classify's tool_marker is what flips the fixture's only guard.
func elseRegistry(perNode map[string]Outcome) *HandlerRegistry {
	return newTestRegistryWithOutcomes(perNode)
}

// runElseFixture runs the fixture through the engine and returns the visited
// node path, the captured events, and the run error.
func runElseFixture(t *testing.T, g *Graph, perNode map[string]Outcome) ([]string, []PipelineEvent, error) {
	t.Helper()
	var mu sync.Mutex
	var events []PipelineEvent
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		mu.Lock()
		events = append(events, evt)
		mu.Unlock()
	})
	engine := NewEngine(g, elseRegistry(perNode), WithPipelineEventHandler(handler))
	res, err := engine.Run(context.Background())
	var path []string
	if res != nil && res.Trace != nil {
		for _, e := range res.Trace.Entries {
			path = append(path, e.NodeID)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	return path, append([]PipelineEvent(nil), events...), err
}

func markerOutcome(marker string) Outcome {
	return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"outcome": "success", "tool_marker": marker}}
}

func findEvents(events []PipelineEvent, typ PipelineEventType) []PipelineEvent {
	var out []PipelineEvent
	for _, e := range events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func TestFromDippinIR_StoresElseTarget(t *testing.T) {
	g, _ := loadElseFixture(t)
	if g.ElseTarget != "Fallback" {
		t.Fatalf("Graph.ElseTarget = %q, want %q", g.ElseTarget, "Fallback")
	}
	// The else default is NOT materialized as a graph edge: the edge list stays
	// exactly what the source declared (dippin keeps ElseTarget outside Edges too).
	for _, e := range g.Edges {
		if e.From == "Classify" && e.To == "Fallback" {
			t.Fatalf("adapter synthesized an explicit Classify -> Fallback edge; else must stay graph-level")
		}
	}
}

func TestGraph_ElseRoute_QualifyingNodes(t *testing.T) {
	g, _ := loadElseFixture(t)
	cases := map[string]bool{
		"Classify":    true,  // ≥1 conditional edge, no unconditional edge
		"Passthrough": false, // has an unconditional edge of its own
		"Setup":       false, // single unconditional edge
		"Fallback":    false, // single unconditional edge
		"Done":        false, // exit node, no outgoing edges — a dead end, not an else route
	}
	for id, want := range cases {
		target, ok := g.ElseRoute(id)
		if ok != want {
			t.Errorf("ElseRoute(%q) covered=%v, want %v", id, ok, want)
		}
		if ok && target != "Fallback" {
			t.Errorf("ElseRoute(%q) = %q, want Fallback", id, target)
		}
	}
	// No else declared → nothing is covered, regardless of edge shape.
	g.ElseTarget = ""
	if _, ok := g.ElseRoute("Classify"); ok {
		t.Fatal("ElseRoute must be false when the graph declares no else target")
	}
}

func TestEngine_ElseTarget_RoutesUnmatchedGuard(t *testing.T) {
	g, _ := loadElseFixture(t)
	path, events, err := runElseFixture(t, g, map[string]Outcome{"Classify": markerOutcome("weird")})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []string{"Setup", "Classify", "Fallback", "Done"}
	if strings.Join(path, ",") != strings.Join(want, ",") {
		t.Fatalf("path = %v, want %v", path, want)
	}

	fts := findEvents(events, EventConditionalFallthrough)
	if len(fts) != 1 {
		t.Fatalf("conditional_fallthrough events = %d, want 1", len(fts))
	}
	ft := fts[0]
	if ft.NodeID != "Classify" || ft.Decision == nil {
		t.Fatalf("fallthrough event malformed: %+v", ft)
	}
	if ft.Decision.EdgePriority != EdgePriorityElse {
		t.Errorf("fallthrough EdgePriority = %q, want %q", ft.Decision.EdgePriority, EdgePriorityElse)
	}
	if ft.Decision.EdgeTo != "Fallback" {
		t.Errorf("fallthrough EdgeTo = %q, want Fallback", ft.Decision.EdgeTo)
	}
	if len(ft.Decision.ConditionsTried) != 1 || ft.Decision.ConditionsTried[0].EdgeTo != "Passthrough" {
		t.Errorf("ConditionsTried = %+v, want the single missed Classify -> Passthrough guard", ft.Decision.ConditionsTried)
	}
	if !strings.Contains(ft.Message, "else") {
		t.Errorf("fallthrough message should name the else route: %q", ft.Message)
	}

	// The decision_edge event for the traversal carries the else priority too.
	var sawElseEdge bool
	for _, de := range findEvents(events, EventDecisionEdge) {
		if de.Decision != nil && de.Decision.EdgeFrom == "Classify" && de.Decision.EdgeTo == "Fallback" {
			sawElseEdge = de.Decision.EdgePriority == EdgePriorityElse
		}
	}
	if !sawElseEdge {
		t.Error("no decision_edge Classify -> Fallback with edge_priority=else")
	}
}

func TestEngine_ElseTarget_GuardMatchTakesPrecedence(t *testing.T) {
	g, _ := loadElseFixture(t)
	path, events, err := runElseFixture(t, g, map[string]Outcome{"Classify": markerOutcome("ok")})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "Setup,Classify,Passthrough,Done"
	if got := strings.Join(path, ","); got != want {
		t.Fatalf("path = %s, want %s", got, want)
	}
	for _, ft := range findEvents(events, EventConditionalFallthrough) {
		if ft.Decision != nil && ft.Decision.EdgePriority == EdgePriorityElse {
			t.Fatalf("else must not fire when a guard matched: %+v", ft)
		}
	}
}

// A node with an unconditional edge of its own is never routed by else — its
// own fallback wins (weight/lexical), exactly as dippin's firstUnconditional does.
func TestEngine_ElseTarget_NodeWithUnconditionalEdgeUnaffected(t *testing.T) {
	g, _ := loadElseFixture(t)
	path, events, err := runElseFixture(t, g, map[string]Outcome{"Classify": markerOutcome("ok")})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(strings.Join(path, ","), "Passthrough,Done") {
		t.Fatalf("Passthrough must take its own unconditional edge to Done; path = %v", path)
	}
	for _, ft := range findEvents(events, EventConditionalFallthrough) {
		if ft.NodeID == "Passthrough" && ft.Decision != nil && ft.Decision.EdgePriority == EdgePriorityElse {
			t.Fatalf("Passthrough fell through via else despite having an unconditional edge: %+v", ft)
		}
	}
}

// Success-side only (dippin docs/edges.md § Section-level default): a genuine
// node failure never routes via else. Classify failing with only a success guard
// hits the existing no-matching-edge halt, not Fallback.
func TestEngine_ElseTarget_DoesNotInterceptFailure(t *testing.T) {
	g, _ := loadElseFixture(t)
	fail := Outcome{Status: OutcomeFail, ContextUpdates: map[string]string{"outcome": "fail"}}
	path, events, err := runElseFixture(t, g, map[string]Outcome{"Classify": fail})
	if err == nil {
		t.Fatalf("expected the failed Classify to halt the run, got path %v", path)
	}
	if !strings.Contains(err.Error(), "no matching edges") {
		t.Fatalf("error = %v, want the no-matching-edges halt", err)
	}
	for _, id := range path {
		if id == "Fallback" {
			t.Fatalf("else intercepted a genuine failure: path = %v", path)
		}
	}
	if fts := findEvents(events, EventConditionalFallthrough); len(fts) != 0 {
		t.Fatalf("no fallthrough event expected on a failure halt, got %d", len(fts))
	}
}

// A workflow without else keeps today's behavior: a node whose guards all miss
// and which has no unconditional edge halts with the no-matching-edge error.
func TestEngine_NoElseTarget_StillHalts(t *testing.T) {
	g, _ := loadElseFixture(t)
	g.ElseTarget = ""
	path, _, err := runElseFixture(t, g, map[string]Outcome{"Classify": markerOutcome("weird")})
	if err == nil || !strings.Contains(err.Error(), "no matching edges") {
		t.Fatalf("err = %v (path %v), want the no-matching-edges halt", err, path)
	}
}

// Round-trip in the #647 style: dippin's own simulator and tracker's engine must
// walk the same node path on the same fixture for every scenario.
func TestEngine_ElseTarget_RoundTripWithDippinSimulate(t *testing.T) {
	g, src := loadElseFixture(t)
	w, err := parser.NewParser(src, elseFixturePath).Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, marker := range []string{"weird", "ok"} {
		simRes, err := simulate.Run(w, simulate.Options{Scenario: map[string]string{"Classify.tool_marker": marker}})
		if err != nil {
			t.Fatalf("dippin simulate (%s): %v", marker, err)
		}
		path, _, err := runElseFixture(t, g, map[string]Outcome{"Classify": markerOutcome(marker)})
		if err != nil {
			t.Fatalf("tracker run (%s): %v", marker, err)
		}
		if got, want := strings.Join(path, " → "), strings.Join(simRes.Path, " → "); got != want {
			t.Errorf("scenario tool_marker=%s: tracker path %s, dippin simulate path %s", marker, got, want)
		}
	}
}

// Validation and tracker's static simulate must accept an else workflow: the
// else target is reachable (via the section default), not an orphan.
func TestValidate_ElseTargetWorkflow(t *testing.T) {
	g, _ := loadElseFixture(t)
	if ve := ValidateAll(g); ve != nil && ve.hasErrors() {
		t.Fatalf("ValidateAll errors: %v", ve.Errors)
	}
	// The variable-availability reachability walk follows the else route even
	// when it is the ONLY way into the target: drop the explicit
	// Passthrough -> Fallback guard so Fallback is else-only, then rebuild.
	kept := g.Edges[:0:0]
	for _, e := range g.Edges {
		if !(e.From == "Passthrough" && e.To == "Fallback") {
			kept = append(kept, e)
		}
	}
	g.Edges = kept
	g2, err := PrepareForExecution(g)
	if err != nil {
		t.Fatalf("PrepareForExecution: %v", err)
	}
	if g2.ElseTarget != "Fallback" {
		t.Fatal("PrepareForExecution must carry ElseTarget onto the execution clone")
	}
	r := newVarReach(g2)
	if !r.canReach("Classify", "Fallback") {
		t.Fatal("varReach must follow the section-level else route Classify -> Fallback")
	}
	if !r.canReach("Setup", "Fallback") {
		t.Fatal("Setup reaches Fallback transitively through Classify's else route")
	}
	if r.canReach("Passthrough", "Fallback") {
		t.Fatal("Passthrough has an unconditional edge; it must not gain an else route")
	}
}

// Guard: no shipped example uses `else ->` yet. When one does, add it to the
// round-trip above so simulate/run agreement is pinned for it too.
func TestExamples_NoUnpinnedElseTarget(t *testing.T) {
	err := filepath.WalkDir(filepath.Join("..", "examples"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".dip" {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		w, err := parser.NewParser(string(src), path).Parse()
		if err != nil {
			return nil // other tests cover parse failures
		}
		if w.ElseTarget != "" {
			t.Errorf("%s declares `else -> %s`; add it to TestEngine_ElseTarget_RoundTripWithDippinSimulate", path, w.ElseTarget)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk examples: %v", err)
	}
}

// elseRestartGraph is the reviewer's repro for #649's graph-walk gap:
//
//	s -> Classify
//	Classify -> Fix   when tool_marker = fixme
//	Classify -> Done  when tool_marker = ok
//	Fix      -> Classify   (restart)
//	Escalate -> Classify   (restart)
//	else -> Escalate
//
// Escalate is reachable ONLY through the else route. A restart of Classify must
// clear it (clearDownstream) so the next else hop into it is a fresh visit, not
// a spurious loop_restart; and dominance must see Classify -> Escalate so the
// Escalate -> Classify edge is a real back edge.
func elseRestartGraph() *Graph {
	g := NewGraph("else_restart")
	g.Attrs["max_restarts"] = "5"
	g.StartNode, g.ExitNode = "s", "Done"
	g.ElseTarget = "Escalate"
	for _, n := range []*Node{
		{ID: "s", Shape: "Mdiamond", Handler: "start"},
		{ID: "Classify", Shape: "parallelogram", Handler: "tool"},
		{ID: "Fix", Shape: "parallelogram", Handler: "tool"},
		{ID: "Escalate", Shape: "parallelogram", Handler: "tool"},
		{ID: "Done", Shape: "Msquare", Handler: "exit"},
	} {
		g.AddNode(n)
	}
	g.AddEdge(&Edge{From: "s", To: "Classify"})
	g.AddEdge(&Edge{From: "Classify", To: "Fix", Condition: "ctx.tool_marker = fixme"})
	g.AddEdge(&Edge{From: "Classify", To: "Done", Condition: "ctx.tool_marker = ok"})
	g.AddEdge(&Edge{From: "Fix", To: "Classify", Attrs: map[string]string{"restart": "true"}})
	g.AddEdge(&Edge{From: "Escalate", To: "Classify", Attrs: map[string]string{"restart": "true"}})
	return g
}

func TestRestartScopes_ElseRouteIsABackEdge(t *testing.T) {
	g := elseRestartGraph()
	rs := computeRestartScopes(g)
	if !rs.isBackEdge("Escalate", "Classify") {
		t.Fatal("Escalate -> Classify must be a back edge: Classify reaches Escalate via else, so it dominates it")
	}
	if !rs.inner["Classify"]["Escalate"] {
		t.Fatalf("Escalate must lie inside Classify's natural loop: %+v", rs.inner["Classify"])
	}
	if got := downstreamNodes(g, "Classify"); !containsString(got, "Escalate") {
		t.Fatalf("downstreamNodes(Classify) = %v, must include the else-only target", got)
	}
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Outcomes weird, weird, ok: two genuine restarts of Classify (each via the
// else hop into Escalate), then exit. Escalate must never be reported as a
// loop_restart target, and the run-wide restart aggregate must be exactly 2.
func TestEngine_ElseTarget_RestartClearsElseOnlyTarget(t *testing.T) {
	g := elseRestartGraph()
	markers := []string{"weird", "weird", "ok"}
	var mu sync.Mutex
	calls := 0
	reg := newTestRegistry()
	reg.Register(&testHandler{name: "tool", executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		if node.ID != "Classify" {
			return Outcome{Status: OutcomeSuccess}, nil
		}
		mu.Lock()
		defer mu.Unlock()
		m := markers[calls]
		calls++
		return markerOutcome(m), nil
	}})
	var events []PipelineEvent
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		mu.Lock()
		events = append(events, evt)
		mu.Unlock()
	})
	res, err := NewEngine(g, reg, WithPipelineEventHandler(handler)).Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var path []string
	for _, e := range res.Trace.Entries {
		path = append(path, e.NodeID)
	}
	want := "s,Classify,Escalate,Classify,Escalate,Classify,Done"
	if got := strings.Join(path, ","); got != want {
		t.Fatalf("path = %s, want %s", got, want)
	}

	mu.Lock()
	defer mu.Unlock()
	restarts := findEvents(events, EventLoopRestart)
	if len(restarts) != 2 {
		t.Fatalf("loop_restart events = %d, want 2 (both genuine Classify restarts): %+v", len(restarts), restarts)
	}
	for _, r := range restarts {
		if r.NodeID != "Classify" {
			t.Errorf("spurious loop_restart on %q (else-only target re-entered without being cleared)", r.NodeID)
		}
	}
	var maxCount int
	for _, d := range findEvents(events, EventDecisionRestart) {
		if d.Decision != nil {
			if d.Decision.RestartCount > maxCount {
				maxCount = d.Decision.RestartCount
			}
			if !containsString(d.Decision.ClearedNodes, "Escalate") {
				t.Errorf("restart of Classify must clear the else-only target Escalate; cleared = %v", d.Decision.ClearedNodes)
			}
		}
	}
	if maxCount != 2 {
		t.Errorf("restart count reached %d, want exactly 2 (not doubled by the else re-entry)", maxCount)
	}
	if calls != 3 {
		t.Errorf("Classify executed %d times, want 3", calls)
	}
}
