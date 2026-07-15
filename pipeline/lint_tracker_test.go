// ABOUTME: Tests for tracker-specific lint rules (TRK1XX). Pin both the
// ABOUTME: positive case (the #208 foot-gun shape fires) and every skip
// ABOUTME: condition (no false positives on well-structured pipelines).
package pipeline

import (
	"strings"
	"testing"
)

// containsWarning reports whether warnings contains a message matching the
// given lint code, optionally requiring that the message also mention nodeID.
// Pass nodeID="" to match the code alone.
func containsWarning(warnings []string, code, nodeID string) bool {
	for _, w := range warnings {
		if !strings.Contains(w, code) {
			continue
		}
		if nodeID == "" || strings.Contains(w, nodeID) {
			return true
		}
	}
	return false
}

// getNode returns the node with the given ID, or nil if not found.
// Test-local helper; Graph has no public GetNode method.
func getNode(g *Graph, id string) *Node {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

// buildTRK101DangerousGraph constructs the canonical #208 foot-gun
// shape: tool node with volume-emitting command, single tool_stdout
// conditional edge, unconditional fallback, no marker_grep, no
// output_limit. Used as the positive test fixture and as the
// starting point that the negative tests each weaken in one
// dimension.
func buildTRK101DangerousGraph() *Graph {
	g := NewGraph("test")
	g.AddNode(&Node{
		ID:      "RunTests",
		Handler: "tool",
		Attrs: map[string]string{
			"tool_command": "go test ./... 2>&1; printf 'tests-pass'",
		},
	})
	g.AddNode(&Node{ID: "Pass", Handler: "tool", Attrs: map[string]string{"tool_command": "true"}})
	g.AddNode(&Node{ID: "Fail", Handler: "tool", Attrs: map[string]string{"tool_command": "true"}})
	g.AddEdge(&Edge{From: "RunTests", To: "Pass", Condition: "ctx.tool_stdout = tests-pass"})
	g.AddEdge(&Edge{From: "RunTests", To: "Fail"}) // unconditional fallback
	return g
}

func TestLintTRK101_FiresOnDangerousShape(t *testing.T) {
	warnings := LintTrackerRules(buildTRK101DangerousGraph())
	if !containsWarning(warnings, "TRK101", "RunTests") {
		t.Errorf("expected TRK101 warning on RunTests, got: %v", warnings)
	}
}

// Skip condition 1: marker_grep declared.
func TestLintTRK101_SkipsWhenMarkerGrepDeclared(t *testing.T) {
	g := buildTRK101DangerousGraph()
	getNode(g, "RunTests").Attrs["marker_grep"] = `^tests-(pass|fail)$`
	warnings := LintTrackerRules(g)
	if containsWarning(warnings, "TRK101", "") {
		t.Errorf("unexpected TRK101: marker_grep should suppress: %v", warnings)
	}
}

// Skip condition 2: explicit output_limit.
func TestLintTRK101_SkipsWhenOutputLimitSet(t *testing.T) {
	g := buildTRK101DangerousGraph()
	getNode(g, "RunTests").Attrs["output_limit"] = "262144"
	warnings := LintTrackerRules(g)
	if containsWarning(warnings, "TRK101", "") {
		t.Errorf("unexpected TRK101: output_limit should suppress: %v", warnings)
	}
}

// Skip condition 3: node also routes on ctx.outcome (exit-code primary signal).
func TestLintTRK101_SkipsWhenAlsoRoutingOnOutcome(t *testing.T) {
	// Build a fresh graph rather than mutating the dangerous-shape
	// fixture — Graph maintains an outgoing-adjacency index that
	// AddEdge updates but slicing g.Edges does not clear, so reusing
	// the fixture and adding edges would silently union with the
	// original tool_stdout conditional + unconditional fallback.
	g := NewGraph("test")
	g.AddNode(&Node{
		ID:      "RunTests",
		Handler: "tool",
		Attrs:   map[string]string{"tool_command": "go test ./... 2>&1; printf 'tests-pass'"},
	})
	g.AddNode(&Node{ID: "Pass", Handler: "tool", Attrs: map[string]string{"tool_command": "true"}})
	g.AddNode(&Node{ID: "Fail", Handler: "tool", Attrs: map[string]string{"tool_command": "true"}})
	g.AddEdge(&Edge{From: "RunTests", To: "Pass", Condition: "ctx.tool_stdout = tests-pass"})
	g.AddEdge(&Edge{From: "RunTests", To: "Fail", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "RunTests", To: "Fail"}) // unconditional fallback — present, but outcome routing also present
	warnings := LintTrackerRules(g)
	if containsWarning(warnings, "TRK101", "") {
		t.Errorf("unexpected TRK101: outcome routing should suppress: %v", warnings)
	}
}

// Skip condition 4: 2+ conditional edges on tool_stdout (exhaustive enumeration).
func TestLintTRK101_SkipsWhenMultipleStdoutConditionals(t *testing.T) {
	g := buildTRK101DangerousGraph()
	// Add a second conditional naming the failure marker.
	g.AddEdge(&Edge{From: "RunTests", To: "Fail", Condition: "ctx.tool_stdout = tests-fail"})
	warnings := LintTrackerRules(g)
	if containsWarning(warnings, "TRK101", "") {
		t.Errorf("unexpected TRK101: 2+ conditional edges (exhaustive enumeration) should suppress: %v", warnings)
	}
}

// Skip condition 5: no unconditional fallback (all edges conditional).
func TestLintTRK101_SkipsWhenNoUnconditionalFallback(t *testing.T) {
	// Build a fresh graph — see TestLintTRK101_SkipsWhenAlsoRoutingOnOutcome
	// for why mutating the fixture via g.Edges = g.Edges[:0] doesn't work
	// (Graph's outgoing-adjacency index is not cleared by slicing).
	g := NewGraph("test")
	g.AddNode(&Node{
		ID:      "RunTests",
		Handler: "tool",
		Attrs:   map[string]string{"tool_command": "go test ./... 2>&1; printf 'tests-pass'"},
	})
	g.AddNode(&Node{ID: "Pass", Handler: "tool", Attrs: map[string]string{"tool_command": "true"}})
	g.AddEdge(&Edge{From: "RunTests", To: "Pass", Condition: "ctx.tool_stdout = tests-pass"})
	// No unconditional fallback — every edge is conditional.
	warnings := LintTrackerRules(g)
	if containsWarning(warnings, "TRK101", "") {
		t.Errorf("unexpected TRK101: no unconditional fallback should suppress: %v", warnings)
	}
}

// Skip condition 6: command body has no volume-emitting indicator.
func TestLintTRK101_SkipsWhenCommandLowVolume(t *testing.T) {
	g := buildTRK101DangerousGraph()
	// Replace with a low-volume command: just a printf.
	getNode(g, "RunTests").Attrs["tool_command"] = "printf 'tests-pass'"
	warnings := LintTrackerRules(g)
	if containsWarning(warnings, "TRK101", "") {
		t.Errorf("unexpected TRK101: low-volume command should suppress: %v", warnings)
	}
}

// buildTRK102FiringGraph constructs the canonical override-shape
// foot-gun: a wait.human gate reachable via an upstream outcome=fail
// edge, with an "accept" label routing to a forward-progress node
// that reaches the exit without another gate, and the edge is NOT
// marked override:true. Mirrors the EscalateMilestone -> Cleanup
// shape from build_product.dip.
func buildTRK102FiringGraph() *Graph {
	return buildTRK102FiringGraphWithLabel("accept")
}

// buildTRK102FiringGraphWithLabel is the same as buildTRK102FiringGraph
// but parameterizes the edge label so case-insensitive matching can be
// exercised.
func buildTRK102FiringGraphWithLabel(label string) *Graph {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "Start", Shape: "Mdiamond", Handler: "start"})
	g.AddNode(&Node{
		ID:      "Build",
		Handler: "tool",
		Attrs:   map[string]string{"tool_command": "make"},
	})
	g.AddNode(&Node{
		ID:      "EscalateMilestone",
		Handler: "wait.human",
		Attrs:   map[string]string{"prompt": "Build failed. Accept or retry?"},
	})
	g.AddNode(&Node{
		ID:      "Cleanup",
		Handler: "tool",
		Attrs:   map[string]string{"tool_command": "true"},
	})
	g.AddNode(&Node{ID: "Done", Shape: "Msquare", Handler: "exit"})

	g.AddEdge(&Edge{From: "Start", To: "Build"})
	g.AddEdge(&Edge{From: "Build", To: "EscalateMilestone", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "Build", To: "Cleanup", Condition: "ctx.outcome = success"})
	// The override-shape edge under test: wait.human -> forward-progress
	// reachable from exit, labeled "accept", NOT marked override:true.
	g.AddEdge(&Edge{From: "EscalateMilestone", To: "Cleanup", Label: label})
	// Retry path (omitted from the heuristic check — only the "accept"
	// label is suspicious; "retry" routes back into the failure loop).
	g.AddEdge(&Edge{From: "EscalateMilestone", To: "Build", Label: "retry"})
	g.AddEdge(&Edge{From: "Cleanup", To: "Done"})
	return g
}

// buildApprovePlanGraph constructs the ApprovePlan shape: a wait.human
// gate with an "approve" label, but NO upstream outcome=fail edge —
// the gate sits on the success path right after plan generation.
// Predicate 4 should suppress TRK102.
func buildApprovePlanGraph() *Graph {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "Start", Shape: "Mdiamond", Handler: "start"})
	g.AddNode(&Node{
		ID:      "GeneratePlan",
		Handler: "codergen",
		Attrs:   map[string]string{"prompt": "Generate plan"},
	})
	g.AddNode(&Node{
		ID:      "ApprovePlan",
		Handler: "wait.human",
		Attrs:   map[string]string{"prompt": "Approve the plan?"},
	})
	g.AddNode(&Node{
		ID:      "Execute",
		Handler: "tool",
		Attrs:   map[string]string{"tool_command": "true"},
	})
	g.AddNode(&Node{ID: "Done", Shape: "Msquare", Handler: "exit"})

	g.AddEdge(&Edge{From: "Start", To: "GeneratePlan"})
	// No upstream outcome=fail edge — GeneratePlan -> ApprovePlan is
	// the natural success path.
	g.AddEdge(&Edge{From: "GeneratePlan", To: "ApprovePlan"})
	g.AddEdge(&Edge{From: "ApprovePlan", To: "Execute", Label: "approve"})
	g.AddEdge(&Edge{From: "ApprovePlan", To: "GeneratePlan", Label: "revise"})
	g.AddEdge(&Edge{From: "Execute", To: "Done"})
	return g
}

// buildAbandonGraph constructs the firing-shape graph but with the
// label "abandon" instead of "accept". Predicate 2 (label match)
// should miss; no warning.
func buildAbandonGraph() *Graph {
	g := buildTRK102FiringGraphWithLabel("abandon")
	return g
}

// buildOverrideMarkedGraph constructs the firing-shape graph but
// with the suspect edge already marked Override:true. Predicate 0
// (already marked) should skip the rule.
func buildOverrideMarkedGraph() *Graph {
	g := buildTRK102FiringGraph()
	for _, e := range g.Edges {
		if e.From == "EscalateMilestone" && e.To == "Cleanup" {
			e.Override = true
		}
	}
	return g
}

// buildToolNodeGraph constructs a graph where the source of the
// "accept"-labeled edge is a tool node, not a wait.human node.
// Predicate 1 (source handler must be wait.human) should skip.
func buildToolNodeGraph() *Graph {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "Start", Shape: "Mdiamond", Handler: "start"})
	g.AddNode(&Node{
		ID:      "Build",
		Handler: "tool",
		Attrs:   map[string]string{"tool_command": "make"},
	})
	g.AddNode(&Node{
		ID:      "MaybeAccept",
		Handler: "tool",
		Attrs:   map[string]string{"tool_command": "true"},
	})
	g.AddNode(&Node{
		ID:      "Cleanup",
		Handler: "tool",
		Attrs:   map[string]string{"tool_command": "true"},
	})
	g.AddNode(&Node{ID: "Done", Shape: "Msquare", Handler: "exit"})

	g.AddEdge(&Edge{From: "Start", To: "Build"})
	g.AddEdge(&Edge{From: "Build", To: "MaybeAccept", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "MaybeAccept", To: "Cleanup", Label: "accept"})
	g.AddEdge(&Edge{From: "Cleanup", To: "Done"})
	return g
}

// buildAcceptToGateGraph constructs the firing-shape graph but the
// "accept" edge routes to another wait.human node (not forward-progress).
// Predicate 3 (target reachable from exit without another gate)
// should miss.
func buildAcceptToGateGraph() *Graph {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "Start", Shape: "Mdiamond", Handler: "start"})
	g.AddNode(&Node{
		ID:      "Build",
		Handler: "tool",
		Attrs:   map[string]string{"tool_command": "make"},
	})
	g.AddNode(&Node{
		ID:      "EscalateMilestone",
		Handler: "wait.human",
		Attrs:   map[string]string{"prompt": "Build failed."},
	})
	g.AddNode(&Node{
		ID:      "SecondGate",
		Handler: "wait.human",
		Attrs:   map[string]string{"prompt": "Are you sure?"},
	})
	g.AddNode(&Node{
		ID:      "Cleanup",
		Handler: "tool",
		Attrs:   map[string]string{"tool_command": "true"},
	})
	g.AddNode(&Node{ID: "Done", Shape: "Msquare", Handler: "exit"})

	g.AddEdge(&Edge{From: "Start", To: "Build"})
	g.AddEdge(&Edge{From: "Build", To: "EscalateMilestone", Condition: "ctx.outcome = fail"})
	// "accept" routes to ANOTHER gate, not forward-progress.
	g.AddEdge(&Edge{From: "EscalateMilestone", To: "SecondGate", Label: "accept"})
	g.AddEdge(&Edge{From: "SecondGate", To: "Cleanup"})
	g.AddEdge(&Edge{From: "Cleanup", To: "Done"})
	return g
}

func TestLintTRK102_FiresOnUnmarkedOverrideShape(t *testing.T) {
	warnings := LintTrackerRules(buildTRK102FiringGraph())
	if !containsWarning(warnings, "TRK102", "EscalateMilestone") {
		t.Errorf("expected TRK102 warning on EscalateMilestone, got: %v", warnings)
	}
}

func TestLintTRK102_DoesNotFireOnApprovePlan(t *testing.T) {
	warnings := LintTrackerRules(buildApprovePlanGraph())
	if containsWarning(warnings, "TRK102", "") {
		t.Errorf("did not expect TRK102 on plan-approval shape, got: %v", warnings)
	}
}

func TestLintTRK102_DoesNotFireOnAbandonLabel(t *testing.T) {
	warnings := LintTrackerRules(buildAbandonGraph())
	if containsWarning(warnings, "TRK102", "") {
		t.Errorf("did not expect TRK102 on abandon label, got: %v", warnings)
	}
}

func TestLintTRK102_DoesNotFireOnAlreadyMarked(t *testing.T) {
	warnings := LintTrackerRules(buildOverrideMarkedGraph())
	if containsWarning(warnings, "TRK102", "") {
		t.Errorf("did not expect TRK102 on already-marked edge, got: %v", warnings)
	}
}

func TestLintTRK102_DoesNotFireOnNonWaitHumanSource(t *testing.T) {
	warnings := LintTrackerRules(buildToolNodeGraph())
	if containsWarning(warnings, "TRK102", "") {
		t.Errorf("did not expect TRK102 on non-wait.human source, got: %v", warnings)
	}
}

func TestLintTRK102_DoesNotFireOnAcceptToGate(t *testing.T) {
	warnings := LintTrackerRules(buildAcceptToGateGraph())
	if containsWarning(warnings, "TRK102", "") {
		t.Errorf("did not expect TRK102 when accept routes to another gate, got: %v", warnings)
	}
}

func TestLintTRK102_CaseInsensitiveLabel(t *testing.T) {
	cases := []string{"Accept", "ACCEPT", "Approve", "Mark Done", "mark done", "MARK DONE"}
	for _, label := range cases {
		t.Run(label, func(t *testing.T) {
			g := buildTRK102FiringGraphWithLabel(label)
			warnings := LintTrackerRules(g)
			if !containsWarning(warnings, "TRK102", "EscalateMilestone") {
				t.Errorf("expected TRK102 for label %q, got: %v", label, warnings)
			}
		})
	}
}

// commandHasVolumeIndicator: word-boundary check on `tee` should not
// false-positive on "guarantee" / "committee".
func TestLintTRK101_TeeWordBoundary(t *testing.T) {
	cases := []struct {
		name    string
		cmd     string
		want    bool
		comment string
	}{
		{"tee_arg", "go test 2>&1 | tee out.log; printf done", true, "tee as a standalone command"},
		{"tee_followed_by_arg", "tee /tmp/out; printf done", true, "tee at start of command"},
		{"guarantee", "echo guarantee; printf done", false, "guarantee should not fire"},
		{"committee", "echo committee; printf done", false, "committee should not fire"},
		{"2>&1_alone", "go build 2>&1; printf done", true, "2>&1 alone fires"},
		{"low_volume_printf", "printf 'tests-pass'", false, "no indicator"},
		{"single_pipe_filter", "ls | wc -l; printf done", false, "single pipe to small filter — not flagged"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := commandHasVolumeIndicator(tc.cmd)
			if got != tc.want {
				t.Errorf("commandHasVolumeIndicator(%q) = %v, want %v (%s)", tc.cmd, got, tc.want, tc.comment)
			}
		})
	}
}

// buildApprovePlanCyclicGraph models build_product's real structure: a
// plan-approval gate entered via forward/unconditional flow (ShowPlan ->
// ApprovePlan), while an unrelated fail edge lives elsewhere on the shared
// forward spine (a SpecLint/ForgeSpec remediation loop). ApprovePlan is
// transitively reachable-backward from that fail edge but is NOT a direct
// escalation target of it. The old transitive reverse-DFS predicate mis-flagged
// this shape (TRK102 false positive on plan-approval gates in cyclic graphs);
// the direct predicate must suppress it.
func buildApprovePlanCyclicGraph() *Graph {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "Start", Shape: "Mdiamond", Handler: "start"})
	g.AddNode(&Node{ID: "SpecLint", Handler: "tool", Attrs: map[string]string{"tool_command": "true"}})
	g.AddNode(&Node{ID: "ForgeSpec", Handler: "codergen", Attrs: map[string]string{"prompt": "forge"}})
	g.AddNode(&Node{ID: "ShowPlan", Handler: "tool", Attrs: map[string]string{"tool_command": "true"}})
	g.AddNode(&Node{ID: "ApprovePlan", Handler: "wait.human", Attrs: map[string]string{"prompt": "Approve the plan?"}})
	g.AddNode(&Node{ID: "Execute", Handler: "tool", Attrs: map[string]string{"tool_command": "true"}})
	g.AddNode(&Node{ID: "Done", Shape: "Msquare", Handler: "exit"})

	g.AddEdge(&Edge{From: "Start", To: "SpecLint"})
	// Remediation loop with a fail edge — the "unrelated upstream failure" the
	// transitive reverse walk wrongly latched onto.
	g.AddEdge(&Edge{From: "SpecLint", To: "ForgeSpec", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "ForgeSpec", To: "SpecLint"})
	g.AddEdge(&Edge{From: "SpecLint", To: "ShowPlan", Condition: "ctx.outcome = success"})
	// Forward/unconditional entry into the plan gate — NOT a fail edge.
	g.AddEdge(&Edge{From: "ShowPlan", To: "ApprovePlan"})
	g.AddEdge(&Edge{From: "ApprovePlan", To: "Execute", Label: "approve"})
	g.AddEdge(&Edge{From: "ApprovePlan", To: "Start", Label: "reject"})
	g.AddEdge(&Edge{From: "Execute", To: "Done"})
	return g
}

func TestLintTRK102_SuppressesPlanGateWithTransitiveUpstreamFail(t *testing.T) {
	warnings := LintTrackerRules(buildApprovePlanCyclicGraph())
	if containsWarning(warnings, "TRK102", "ApprovePlan") {
		t.Errorf("TRK102 false-positive: fired on a plan-approval gate reachable only "+
			"transitively (not directly) from an upstream fail: %v", warnings)
	}
}

// buildFallbackOnlyEscalationGraph reaches a wait.human escalation gate ONLY via
// a node's fallback_target (no direct fail edge into the gate). It is still a
// failure escalation, so an unmarked "accept" edge should fire TRK102.
func buildFallbackOnlyEscalationGraph() *Graph {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "Start", Shape: "Mdiamond", Handler: "start"})
	g.AddNode(&Node{ID: "Work", Handler: "codergen", Attrs: map[string]string{"prompt": "x", "fallback_target": "Escalate"}})
	g.AddNode(&Node{ID: "Escalate", Handler: "wait.human", Attrs: map[string]string{"prompt": "Accept?"}})
	g.AddNode(&Node{ID: "Cleanup", Handler: "tool", Attrs: map[string]string{"tool_command": "true"}})
	g.AddNode(&Node{ID: "Done", Shape: "Msquare", Handler: "exit"})

	g.AddEdge(&Edge{From: "Start", To: "Work"})
	g.AddEdge(&Edge{From: "Work", To: "Cleanup", Condition: "ctx.outcome = success"})
	// Escalate reached only via Work's fallback_target — no direct edge into it.
	g.AddEdge(&Edge{From: "Escalate", To: "Cleanup", Label: "accept"})
	g.AddEdge(&Edge{From: "Cleanup", To: "Done"})
	return g
}

func TestLintTRK102_FiresOnFallbackTargetEscalation(t *testing.T) {
	warnings := LintTrackerRules(buildFallbackOnlyEscalationGraph())
	if !containsWarning(warnings, "TRK102", "Escalate") {
		t.Errorf("TRK102 should fire on a fallback_target-only escalation gate with an "+
			"unmarked accept edge: %v", warnings)
	}
}
