// ABOUTME: Regression guard for issue #301 — both shipped build_product workflows must
// ABOUTME: run the SpecLint coherence preflight strictly before any decomposition node.
package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadBuildProductSuperspec loads examples/build_product_with_superspec.dip the
// same way the binary embeds it. Mirrors loadBuildProduct.
func loadBuildProductSuperspec(t *testing.T) *Graph {
	t.Helper()
	path := filepath.Join("..", "examples", "build_product_with_superspec.dip")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	g, _, err := LoadDippinWorkflow(string(source), "build_product_with_superspec.dip")
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v", err)
	}
	return g
}

// reachesNodeAvoiding reports whether target is reachable from start without
// traversing the blocked node. Used to assert SpecLint is an unavoidable gate:
// if decomposition is reachable while SpecLint is blocked, a bypass path exists.
func reachesNodeAvoiding(g *Graph, start, target, blocked string) bool {
	visited := map[string]bool{}
	stack := []string{start}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == blocked {
			continue
		}
		if cur == target {
			return true
		}
		if visited[cur] {
			continue
		}
		visited[cur] = true
		for _, e := range g.OutgoingEdges(cur) {
			stack = append(stack, e.To)
		}
	}
	return false
}

// assertSpecLintGate pins the issue #301 invariants shared by both workflows:
// the SpecLint agent node sits on the Setup edge, routes success/fail
// exhaustively (no unconditional fallback — see CLAUDE.md edge-routing rules),
// fails closed via auto_status, and writes the spec-quality decision artifact.
func assertSpecLintGate(t *testing.T, g *Graph, successTarget, escalateTarget string) {
	t.Helper()

	n, ok := g.Nodes["SpecLint"]
	if !ok {
		t.Fatal("SpecLint node missing (issue #301 spec-coherence preflight)")
	}
	if n.Handler != "codergen" {
		t.Errorf("SpecLint handler = %q, want codergen (LLM agent gate, not a shell linter)", n.Handler)
	}
	if n.Attrs["auto_status"] != "true" {
		t.Error("SpecLint must set auto_status: true — without it the fail edge is dead (agent outcome is always success)")
	}
	if !strings.Contains(n.Attrs["prompt"], ".ai/decisions/spec-quality.md") {
		t.Error("SpecLint prompt must write the .ai/decisions/spec-quality.md artifact (issue #301 AC)")
	}
	if !strings.Contains(n.Attrs["prompt"], "STATUS:fail") {
		t.Error("SpecLint prompt must carry the fail-closed STATUS contract (STATUS:fail first line, last-line-wins override)")
	}

	if !hasUnconditionalEdgeTo(g, "Setup", "SpecLint") {
		t.Error("Setup must route to SpecLint (the preflight runs before any spec read/decomposition)")
	}
	if hasEdgeTo(g, "Setup", successTarget) {
		t.Errorf("Setup still routes directly to %s, bypassing SpecLint", successTarget)
	}
	if !hasEdgeWithCondition(g, "SpecLint", successTarget, "ctx.outcome = success") {
		t.Errorf("SpecLint must continue to %s only when ctx.outcome = success", successTarget)
	}
	if !hasEdgeWithCondition(g, "SpecLint", escalateTarget, "ctx.outcome = fail") {
		t.Errorf("SpecLint must route ctx.outcome = fail to %s (never silently to Done)", escalateTarget)
	}
	for _, e := range g.OutgoingEdges("SpecLint") {
		if e.Condition == "" {
			t.Errorf("SpecLint has an unconditional edge to %q — success/fail conditions are exhaustive, a fallback risks loops", e.To)
		}
	}
}

// TestBuildProductSpecLintPreflight pins SpecLint's placement and routing in
// build_product.dip: Setup -> SpecLint -> ReadSpec (success) / EscalateReview (fail).
func TestBuildProductSpecLintPreflight(t *testing.T) {
	g := loadBuildProduct(t)
	assertSpecLintGate(t, g, "ReadSpec", "CheckSpecForgeBudget")
}

// TestBuildProductSpecLintGatesDecomposition asserts ReadSpec and Decompose are
// unreachable from Setup without traversing SpecLint — no bypass path may let
// an agent build on an unchecked spec.
func TestBuildProductSpecLintGatesDecomposition(t *testing.T) {
	g := loadBuildProduct(t)
	for _, target := range []string{"ReadSpec", "Decompose"} {
		if reachesNodeAvoiding(g, "Setup", target, "SpecLint") {
			t.Errorf("%s is reachable from Setup without passing SpecLint (issue #301: preflight must gate all decomposition)", target)
		}
	}
}

// TestSuperspecSpecLintPreflight pins the same gate in
// build_product_with_superspec.dip: Setup -> SpecLint -> AnalyzeSpec (success) /
// EscalateToHuman (fail). The fail edge must be explicit — a node with a
// conditional success edge does not fall through to the graph-level on_failure
// cascade for an unmatched fail outcome.
func TestSuperspecSpecLintPreflight(t *testing.T) {
	g := loadBuildProductSuperspec(t)
	assertSpecLintGate(t, g, "AnalyzeSpec", "EscalateToHuman")
}

// TestSuperspecSpecLintGatesDecomposition asserts AnalyzeSpec is unreachable
// from Setup without traversing SpecLint in the superspec workflow.
func TestSuperspecSpecLintGatesDecomposition(t *testing.T) {
	g := loadBuildProductSuperspec(t)
	if reachesNodeAvoiding(g, "Setup", "AnalyzeSpec", "SpecLint") {
		t.Error("AnalyzeSpec is reachable from Setup without passing SpecLint (issue #301: preflight must gate all decomposition)")
	}
}
