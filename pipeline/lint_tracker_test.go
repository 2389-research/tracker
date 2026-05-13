// ABOUTME: Tests for tracker-specific lint rules (TRK1XX). Pin both the
// ABOUTME: positive case (the #208 foot-gun shape fires) and every skip
// ABOUTME: condition (no false positives on well-structured pipelines).
package pipeline

import (
	"testing"
)

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
