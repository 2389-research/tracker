// ABOUTME: Issue #640 group A (routing) + D11/E10 regression guards on the REAL build_product.dip graph.
// ABOUTME: Scripted engine sims prove each fix (A1–A6) at the routing layer, not just by string-pinning edges.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// errGateReached is the sentinel a bp640Sim human gate returns when the test
// did not script an answer — reaching a gate is then a hard stop the test can
// assert on (or a test failure if it was not expected).
var errGateReached = errors.New("sim: human gate reached")

// bp640Sim drives the real build_product graph through the engine with
// per-node scripted outcomes. `script` maps a node ID to a function of the
// node's prior visit count (0 on first visit); unscripted nodes succeed.
// Human gates answer `gate` (a label); with gate == "" they fail the run with
// errGateReached so the test can prove "this path never asks a human".
type bp640Sim struct {
	mu     sync.Mutex
	visits []string
	seen   map[string]int
	script map[string]func(n int) Outcome
	gate   string
}

func bpOK(stdout string) Outcome {
	return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"outcome": "success", "tool_stdout": stdout}}
}

func bpFail(stdout string) Outcome {
	return Outcome{Status: OutcomeFail, ContextUpdates: map[string]string{"outcome": "fail", "tool_stdout": stdout, "tool_stderr": "sim stderr"}}
}

func (s *bp640Sim) count(id string) int { return s.seen[id] }

func (s *bp640Sim) visited(id string) bool { return s.seen[id] > 0 }

func (s *bp640Sim) run(t *testing.T, g *Graph) (*EngineResult, error) {
	t.Helper()
	s.seen = map[string]int{}
	exec := func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		n := s.seen[node.ID]
		s.seen[node.ID]++
		s.visits = append(s.visits, node.ID)
		if fn, ok := s.script[node.ID]; ok {
			return fn(n), nil
		}
		switch node.ID {
		case "ApprovePlan":
			return Outcome{Status: OutcomeSuccess, PreferredLabel: "approve"}, nil
		case "EscalateMilestone", "EscalateReview", "OperatorDecision":
			if s.gate == "" {
				return bpFail(""), fmt.Errorf("%w: %s", errGateReached, node.ID)
			}
			return Outcome{Status: OutcomeSuccess, PreferredLabel: s.gate}, nil
		case "CheckMilestoneOutputs":
			return bpOK("outputs-present"), nil
		case "AbortRun", "SpecForgeFailed":
			// Both scripts `exit 1` unconditionally (fail-closed terminals).
			return bpFail("BUILD ABORTED"), nil
		}
		return bpOK(""), nil
	}
	reg := NewHandlerRegistry()
	for _, name := range []string{"start", "exit", "codergen", "wait.human", "conditional", "parallel", "parallel.fan_in", "tool"} {
		reg.Register(&testHandler{name: name, executeFn: exec})
	}
	res, err := NewEngine(g, reg).Run(context.Background())
	s.mu.Lock()
	defer s.mu.Unlock()
	return res, err
}

// oneMilestone scripts PickNextMilestone for exactly one milestone: the first
// pick yields milestone-1, every later pick reports all-done (the marker is
// printed LAST by the script, with no trailing newline).
func oneMilestone() func(n int) Outcome {
	return func(n int) Outcome {
		if n == 0 {
			return bpOK("milestone 1 of 1\nmilestone-1")
		}
		return bpOK("ALL_MILESTONES_COMPLETE\nall-done")
	}
}

// edgeIndex returns the declaration index of the first edge from->to whose
// condition matches exactly, or -1.
func edgeIndex(g *Graph, from, to, cond string) int {
	for i, e := range g.OutgoingEdges(from) {
		if e.To == to && e.Condition == cond {
			return i
		}
	}
	return -1
}

// ─── A1: PickNextMilestone failure must escalate, never Implement ──────────

// TestBuildProduct640A1PickFailureEscalates: an exit-1 PickNextMilestone (no
// headers / extraction failure / missing .ai/build) leaves an empty or
// missing current.md. Pre-#640 the `not contains all-done` edge had no
// outcome guard, so Implement (Opus, 50 turns) ran on nothing and the
// EscalateMilestone edge was dead code.
func TestBuildProduct640A1PickFailureEscalates(t *testing.T) {
	g := loadBuildProduct(t)

	// The .dip spells the guard with dippin's word conjunction
	// (`ctx.outcome = success and ctx.tool_stdout not endswith all-done`);
	// the adapter serializes the AST into tracker's `&&` dialect (#647), so
	// the graph carries the compound — assert on that, through the real
	// adapter, so a hand-built `&&` string can't pass for the wrong reason.
	failIdx := edgeIndex(g, "PickNextMilestone", "EscalateMilestone", "ctx.outcome = fail")
	implIdx := edgeIndex(g, "PickNextMilestone", "Implement", "ctx.outcome = success && ctx.tool_stdout not endswith all-done")
	if failIdx == -1 {
		t.Error("PickNextMilestone has no live `ctx.outcome = fail -> EscalateMilestone` edge (#640 A1)")
	}
	if implIdx == -1 {
		t.Errorf("PickNextMilestone -> Implement must be guarded by `ctx.outcome = success and ...` (#640 A1); edges=%v", describeEdges(g, "PickNextMilestone"))
	}
	if failIdx != -1 && implIdx != -1 && failIdx > implIdx {
		t.Errorf("the fail edge must be declared FIRST (first-match ordering is the guard): failIdx=%d implIdx=%d", failIdx, implIdx)
	}
	if hasEdgeWithCondition(g, "PickNextMilestone", "Implement", "ctx.tool_stdout not contains all-done") {
		t.Error("PickNextMilestone -> Implement still routes on the unguarded `not contains all-done` substring (#640 A1)")
	}

	sim := &bp640Sim{script: map[string]func(int) Outcome{
		"PickNextMilestone": func(int) Outcome {
			return bpFail("ERROR: no milestone headers found in .ai/decisions/milestones.md\nExpected format: ## Milestone N: Title")
		},
	}}
	_, err := sim.run(t, g)
	if !errors.Is(err, errGateReached) || !sim.visited("EscalateMilestone") {
		t.Fatalf("Pick failure did not reach EscalateMilestone: err=%v visits=%v", err, sim.visits)
	}
	if sim.visited("Implement") {
		t.Errorf("Implement ran on a failed pick (empty current.md): visits=%v", sim.visits)
	}
}

// ─── A2: mechanical failures abort the run; never the accept gate ──────────

// bp640StrictNodes are the tool nodes whose only pre-#640 failure route was
// the graph-level `on_failure: EscalateReview` — i.e. the post-build accept
// gate whose unattended default is "accept" -> Cleanup -> FinalCommit -> Done.
var bp640StrictNodes = []string{"Setup", "ShowPlan", "CommitIfDirty", "MarkMilestoneDone", "ClearStaleReviews", "ComputeReviewDiff", "ResetReviewBudget", "Cleanup"}

func TestBuildProduct640A2StrictFailureEdges(t *testing.T) {
	g := loadBuildProduct(t)
	// The graph-level catch-all is kept ONLY as a fail-closed backstop
	// pointed at the abort terminal (it is what dippin's DIP144 credits for
	// the passthrough Start and the parallel reviewer branches, whose own
	// route would be inert — #296). It must never name a human gate again.
	if fb := g.Attrs["fallback_target"]; fb != "AbortRun" {
		t.Errorf("graph-level on_failure/fallback_target = %q, want AbortRun: a mechanical failure must not fall back into a human gate whose unattended default ships (#640 A2)", fb)
	}
	abort, ok := g.Nodes["AbortRun"]
	if !ok {
		t.Fatal("AbortRun terminal node missing (#640 A2)")
	}
	if abort.Handler != "tool" {
		t.Errorf("AbortRun handler = %q, want tool (a gate's default would auto-advance headlessly)", abort.Handler)
	}
	if hasAnyConditionalEdge(g.OutgoingEdges("AbortRun")) || !hasUnconditionalEdgeTo(g, "AbortRun", "Done") {
		t.Error("AbortRun must have exactly the SpecForgeFailed shape: exit 1 + a single unconditional edge to Done (strict-failure halt)")
	}
	for _, id := range bp640StrictNodes {
		if !hasEdgeWithCondition(g, id, "AbortRun", "ctx.outcome = fail") {
			t.Errorf("%s has no `ctx.outcome = fail -> AbortRun` edge (#640 A2)", id)
		}
	}
	// Every non-terminal tool node must own its failure route: with no graph
	// fallback, an unrouted fail dead-stops — which is fine for the two
	// deliberate terminals (they exit 1 on purpose) and wrong for anything else.
	for id, n := range g.Nodes {
		if n.Handler != "tool" || id == "AbortRun" || id == "SpecForgeFailed" {
			continue
		}
		if !hasAnyConditionalEdge(g.OutgoingEdges(id)) {
			t.Errorf("tool node %s has only unconditional edges and no graph fallback — its failure would dead-stop with no route (#640 A2)", id)
		}
	}
}

// TestBuildProduct640A2StrictFailuresNeverShip drives each strict-failure node
// to OutcomeFail on the real graph and proves the run ends `fail` at AbortRun:
// EscalateReview is never asked, and Cleanup / FinalCommit / Done never run.
// The gate answers its unattended default (auto-approve semantics) so the
// proof holds headlessly — it never gets the chance to say "accept".
func TestBuildProduct640A2StrictFailuresNeverShip(t *testing.T) {
	g := loadBuildProduct(t)
	cases := []struct {
		node  string
		gate  string // label every gate answers if reached
		setup map[string]func(int) Outcome
	}{
		{node: "Setup", gate: "accept"},
		{node: "CommitIfDirty", gate: "accept"},
		{node: "MarkMilestoneDone", gate: "accept"},
		{node: "ClearStaleReviews", gate: "accept"},
		{node: "Cleanup", gate: "accept"},
		// ResetReviewBudget is only reachable through EscalateReview "retry".
		{node: "ResetReviewBudget", gate: "retry", setup: map[string]func(int) Outcome{
			"FinalBuild": func(int) Outcome { return bpFail("go test ./... FAIL") },
		}},
		// SpecForgeFailed is the forge loop's fail-closed terminal (exit 1).
		{node: "SpecForgeFailed", gate: "accept", setup: map[string]func(int) Outcome{
			"SpecLint":             func(int) Outcome { return bpFail("") },
			"CheckSpecForgeBudget": func(int) Outcome { return bpFail("spec-forge budget exhausted") },
		}},
	}
	for _, tc := range cases {
		t.Run(tc.node, func(t *testing.T) {
			script := map[string]func(int) Outcome{"PickNextMilestone": oneMilestone()}
			for k, v := range tc.setup {
				script[k] = v
			}
			script[tc.node] = func(int) Outcome { return bpFail("ERROR: " + tc.node + " failed") }
			sim := &bp640Sim{script: script, gate: tc.gate}
			res, err := sim.run(t, g)
			if !sim.visited(tc.node) {
				t.Fatalf("sim never reached %s: visits=%v", tc.node, sim.visits)
			}
			if err == nil || res == nil || res.Status != OutcomeFail {
				t.Fatalf("run must end fail after %s failed: err=%v status=%v visits=%v", tc.node, err, statusOf(res), sim.visits)
			}
			if !sim.visited("AbortRun") {
				t.Errorf("%s failure did not route to AbortRun: visits=%v", tc.node, sim.visits)
			}
			for _, shipped := range []string{"FinalCommit", "Done"} {
				if sim.visited(shipped) {
					t.Errorf("%s failure reached %s — a mechanical failure shipped (#640 A2): visits=%v", tc.node, shipped, sim.visits)
				}
			}
			if tc.node != "ResetReviewBudget" && sim.visited("EscalateReview") {
				t.Errorf("%s failure reached the post-build accept gate EscalateReview (#640 A2): visits=%v", tc.node, sim.visits)
			}
			if last := sim.visits[len(sim.visits)-1]; last != "AbortRun" {
				t.Errorf("run continued past the abort terminal: last visited = %s", last)
			}
			// AbortRun is its own graph-level fallback, so the engine re-enters
			// it once (idempotent echo + exit 1) before the one-shot latch halts
			// the run — never more than that.
			if n := sim.count("AbortRun"); n < 1 || n > 2 {
				t.Errorf("AbortRun ran %d times, want 1-2 (self-fallback then latch): visits=%v", n, sim.visits)
			}
		})
	}
}

func describeEdges(g *Graph, from string) []string {
	var out []string
	for _, e := range g.OutgoingEdges(from) {
		out = append(out, fmt.Sprintf("-> %s [%s]", e.To, e.Condition))
	}
	return out
}

func statusOf(r *EngineResult) string {
	if r == nil {
		return "<nil>"
	}
	return string(r.Status)
}

// ─── A3: exact end-of-stdout markers, not raw substrings ──────────────────

// TestBuildProduct640A3EscalateMarkerIsExact: `contains escalate` over the
// 64KB test-output tail sent a package named `escalate` (or any log line with
// the word) straight to EscalateMilestone, skipping the fix loop. The routing
// markers are printed LAST with no trailing newline, so `endswith` is exact.
func TestBuildProduct640A3EscalateMarkerIsExact(t *testing.T) {
	g := loadBuildProduct(t)
	for _, e := range g.Edges {
		if strings.Contains(e.Condition, " contains ") && !strings.Contains(e.Condition, "endswith") {
			t.Errorf("edge %s -> %s still routes on a raw substring: %q (#640 A3)", e.From, e.To, e.Condition)
		}
	}
	escIdx := edgeIndex(g, "TestMilestone", "EscalateMilestone", "ctx.tool_stdout endswith escalate")
	fixIdx := edgeIndex(g, "TestMilestone", "FixMilestone", "ctx.outcome = fail")
	if escIdx == -1 || fixIdx == -1 || escIdx > fixIdx {
		t.Errorf("TestMilestone must check `endswith escalate` BEFORE the generic fail edge: escIdx=%d fixIdx=%d", escIdx, fixIdx)
	}

	// (a) the word mid-output with an ordinary red run → fix loop, not the gate.
	redThenGreen := func(n int) Outcome {
		if n == 0 {
			return bpFail("=== RUN   TestEscalate\n--- FAIL: TestEscalate (0.00s)\nFAIL\tgithub.com/acme/escalate\t0.012s\nFAIL\n")
		}
		return bpOK("tests-pass")
	}
	sim := &bp640Sim{script: map[string]func(int) Outcome{
		"PickNextMilestone": oneMilestone(),
		"TestMilestone":     redThenGreen,
	}}
	res, err := sim.run(t, g)
	if err != nil || res.Status != OutcomeSuccess {
		t.Fatalf("mid-output 'escalate' run: err=%v status=%s visits=%v", err, statusOf(res), sim.visits)
	}
	if sim.count("FixMilestone") != 1 || sim.visited("EscalateMilestone") {
		t.Errorf("a package named 'escalate' skipped the fix loop: fix=%d escalated=%v visits=%v", sim.count("FixMilestone"), sim.visited("EscalateMilestone"), sim.visits)
	}

	// (b) the real marker, printed last → the gate.
	sim = &bp640Sim{script: map[string]func(int) Outcome{
		"PickNextMilestone": oneMilestone(),
		"TestMilestone": func(int) Outcome {
			return bpFail("--- attempt 3 of 3 ---\nESCALATE: milestone failed after 3 attempts\nescalate")
		},
	}}
	_, err = sim.run(t, g)
	if !errors.Is(err, errGateReached) || !sim.visited("EscalateMilestone") || sim.visited("FixMilestone") {
		t.Errorf("trailing escalate marker did not route to EscalateMilestone: err=%v visits=%v", err, sim.visits)
	}
}

// ─── A4: verify-fail loop breaker ──────────────────────────────────────────

// TestBuildProduct640A4VerifyFailBudget: VerifyMilestone -> FixMilestone had no
// script bound (fix_attempts resets on green), so a verifier that keeps
// failing a green tree ran to the engine ceiling (51× Fix + 51× Verify) and
// then a gate-less terminal. CheckVerifyFailBudget caps it at 3 fixes.
func TestBuildProduct640A4VerifyFailBudget(t *testing.T) {
	g := loadBuildProduct(t)
	if _, ok := g.Nodes["CheckVerifyFailBudget"]; !ok {
		t.Fatal("CheckVerifyFailBudget tool node missing (#640 A4)")
	}
	if hasEdgeTo(g, "VerifyMilestone", "FixMilestone") {
		t.Error("VerifyMilestone still routes straight to FixMilestone, bypassing the budget gate (#640 A4)")
	}
	if !hasEdgeWithCondition(g, "VerifyMilestone", "CheckVerifyFailBudget", "ctx.outcome = fail") {
		t.Error("VerifyMilestone has no `ctx.outcome = fail -> CheckVerifyFailBudget` edge (#640 A4)")
	}
	if !hasEdgeWithCondition(g, "CheckVerifyFailBudget", "FixMilestone", "ctx.outcome = success") {
		t.Error("CheckVerifyFailBudget has no success edge to FixMilestone (#640 A4)")
	}
	if !hasEdgeWithCondition(g, "CheckVerifyFailBudget", "EscalateMilestone", "ctx.outcome = fail") {
		t.Error("CheckVerifyFailBudget has no fail edge to EscalateMilestone (#640 A4)")
	}

	// Sim: the verifier never accepts; the budget gate mirrors the script's
	// gate-BEFORE-work `-gt 3` (exactly 3 fixes, the 4th entry escalates).
	sim := &bp640Sim{script: map[string]func(int) Outcome{
		"PickNextMilestone": oneMilestone(),
		"TestMilestone":     func(int) Outcome { return bpOK("tests-pass") },
		"VerifyMilestone":   func(int) Outcome { return bpFail("") },
		"CheckVerifyFailBudget": func(n int) Outcome {
			if n+1 > 3 {
				return bpFail("verify-budget-exhausted")
			}
			return bpOK("verify-budget-ok")
		},
	}}
	_, err := sim.run(t, g)
	if !errors.Is(err, errGateReached) || !sim.visited("EscalateMilestone") {
		t.Fatalf("exhausted verify budget did not escalate: err=%v visits=%v", err, sim.visits)
	}
	if got := sim.count("FixMilestone"); got != 3 {
		t.Errorf("FixMilestone ran %d times on a never-accepting verifier, want exactly 3 (#640 A4): visits=%v", got, sim.visits)
	}
}

// ─── A5: no unconditional duplicate of a conditional loop edge ─────────────

func TestBuildProduct640A5FixMilestoneEdgesExhaustive(t *testing.T) {
	g := loadBuildProduct(t)
	for _, e := range g.OutgoingEdges("FixMilestone") {
		if e.Condition == "" {
			t.Errorf("FixMilestone -> %s is unconditional alongside conditional edges to the same loop target — the pattern CLAUDE.md forbids (#640 A5)", e.To)
		}
	}
	if !hasEdgeAttr(g, "FixMilestone", "TestMilestone", "ctx.outcome = success", "restart", "true") {
		t.Error("FixMilestone needs an explicit `ctx.outcome = success -> TestMilestone restart: true` edge so success/fail stay exhaustive (#640 A5)")
	}
}

// ─── A6: the unattended "mark done" default is an audited override ─────────

// TestBuildProduct640A6MarkDoneIsAuditedOverride: under --auto-approve a
// never-green milestone is marked done. That stays the default (#407: never
// discard work; `retry` would burn 50 Opus re-implementations on a milestone
// that cannot go green and still end at a gate-less terminal) — but it must
// leave an audit trail: the run completes validation_overridden, not success.
func TestBuildProduct640A6MarkDoneIsAuditedOverride(t *testing.T) {
	g := loadBuildProduct(t)
	if def := g.Nodes["EscalateMilestone"].Attrs["default_choice"]; def != "mark done" {
		t.Errorf("EscalateMilestone default = %q, want \"mark done\" (#407 work-preserving default)", def)
	}
	var markDone *Edge
	for _, e := range g.OutgoingEdges("EscalateMilestone") {
		if e.Label == "mark done" {
			markDone = e
		}
	}
	if markDone == nil {
		t.Fatal("EscalateMilestone has no \"mark done\" edge")
	}
	if !markDone.Override {
		t.Error("EscalateMilestone \"mark done\" must carry override: true so the unattended default fires EventValidationOverridden (#640 A6)")
	}

	sim := &bp640Sim{gate: "mark done", script: map[string]func(int) Outcome{
		"PickNextMilestone": oneMilestone(),
		"TestMilestone":     func(int) Outcome { return bpFail("ESCALATE: milestone failed after 3 attempts\nescalate") },
	}}
	res, err := sim.run(t, g)
	if err != nil {
		t.Fatalf("auto-approved mark-done run failed: %v visits=%v", err, sim.visits)
	}
	if res.Status != OutcomeValidationOverridden {
		t.Errorf("status = %q, want %q — an unattended mark-done over a red milestone left no audit flag (#640 A6)", res.Status, OutcomeValidationOverridden)
	}
}

// ─── D11 / E10 / A2 prompt copy ────────────────────────────────────────────

func TestBuildProduct640D11FinalSpecCheckAllowlist(t *testing.T) {
	g := loadBuildProduct(t)
	p := promptOf(t, g, "FinalSpecCheck")
	for _, f := range []string{
		"verify.sh", "ci-probe.sh", "iface-reachability-rubric.md",
		"declared-files.raw", "declared-files.list", "scoped-milestones.md",
		"milestone-start-sha", "run-base-sha", "build-context.md",
		"spec_forge_attempts", "review_fix_attempts",
		"review-diff.md", "review-claude.md", "review-codex.md", "review-gemini.md",
	} {
		if !strings.Contains(p, f) {
			t.Errorf("FinalSpecCheck .ai/build allowlist omits %q — the goal gate would flag a workflow-written file as an extra and fail every run (#640 D11)", f)
		}
	}
}

func TestBuildProduct640GatePromptsShowDiagnostics(t *testing.T) {
	g := loadBuildProduct(t)
	review := promptOf(t, g, "EscalateReview")
	for _, v := range []string{"${ctx.tool_stdout}", "${ctx.tool_stderr}", "${ctx.last_response}"} {
		if !strings.Contains(review, v) {
			t.Errorf("EscalateReview prompt does not interpolate %s — the operator cannot see why they are at the gate (#640 A2)", v)
		}
	}
	if strings.Contains(review, "Post-build review or verification flagged problems.") {
		t.Error("EscalateReview copy still claims a post-build review flagged problems; it is reached from planning failures too (#640 A2)")
	}
	milestone := promptOf(t, g, "EscalateMilestone")
	if strings.Contains(milestone, "## Verify currently") {
		t.Error("EscalateMilestone still labels ${ctx.tool_stdout} as the live verify state; it is whichever tool ran last (#640 E10)")
	}
	if !strings.Contains(strings.ToLower(milestone), "most recent tool output") {
		t.Error("EscalateMilestone must label the interpolated block honestly as the most recent tool output (#640 E10)")
	}
}
