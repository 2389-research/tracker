// ABOUTME: Regression guard for #646 items 8 and 11 in the dotpowers-family
// ABOUTME: workflows: the RunScenarios and ValidateBuild edges route on the
// ABOUTME: LAST line of multi-line tool stdout (endswith), and "no build
// ABOUTME: system" goes to the help/recover node instead of a rework loop.
package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

func loadExampleWorkflow(t *testing.T, name string) *Graph {
	t.Helper()
	path := filepath.Join("..", "examples", name)
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	g, _, err := LoadDippinWorkflow(string(source), path)
	if err != nil {
		t.Fatalf("LoadDippinWorkflow(%s): %v", name, err)
	}
	SeedWorkflowDir(g, path)
	return g
}

// routeFrom runs the engine's edge selection for node `from` against a tool
// outcome (ctx.outcome + ctx.tool_stdout) and returns the chosen target.
func routeFrom(t *testing.T, g *Graph, from, outcome, stdout string) string {
	t.Helper()
	engine := NewEngine(g, newTestRegistryWithOutcomes(nil))
	pctx := NewPipelineContextFrom(map[string]string{"outcome": outcome, "tool_stdout": stdout})
	edge, err := engine.selectEdge("run", g.OutgoingEdges(from), pctx)
	if err != nil {
		t.Fatalf("selectEdge(%s): %v", from, err)
	}
	if edge == nil {
		t.Fatalf("selectEdge(%s): no edge selected", from)
	}
	return edge.To
}

// TestDotpowersFamilyRunScenariosRoutesOnLastLine pins #646 item 11: the
// RunScenarios scripts print per-scenario log lines before the marker, so an
// exact `= scenarios_pass` never matched once a scenario ran. The edges now
// use endswith, and zero scenario files is a failure marker.
func TestDotpowersFamilyRunScenariosRoutesOnLastLine(t *testing.T) {
	cases := []struct {
		dip    string
		stdout string
		want   string
	}{
		{"scenario-testing.dip", "=== Running: .scratch/scenario_a.sh ===\nPASS: .scratch/scenario_a.sh\nResults: 1 passed, 0 failed\nscenarios_pass", "ExtractScenarioPatterns"},
		{"scenario-testing.dip", "=== Running: .scratch/scenario_a.sh ===\nFAIL: .scratch/scenario_a.sh\nResults: 0 passed, 1 failed\nscenarios_fail", "FixScenarioFailures"},
		{"scenario-testing.dip", "ERROR: no .scratch/scenario_* files — WriteScenarios produced nothing to validate\nscenarios_fail", "FixScenarioFailures"},
		{"kitchen-sink.dip", "=== Running .scratch/scenario_a.py ===\nResults: 1 passed, 0 failed\nscenarios_pass", "ExtractScenarioPatterns"},
		{"kitchen-sink.dip", "=== Running .scratch/scenario_a.py ===\nscenarios_fail_1_of_2", "FixScenarioFailures"},
		{"kitchen-sink.dip", "ERROR: no .scratch/scenario_* files — WriteScenarios produced nothing to validate\nscenarios_fail_none_found", "FixScenarioFailures"},
	}
	for _, tc := range cases {
		g := loadExampleWorkflow(t, tc.dip)
		if got := routeFrom(t, g, "RunScenarios", "success", tc.stdout); got != tc.want {
			t.Errorf("%s RunScenarios stdout %q routed to %s, want %s", tc.dip, tc.stdout, got, tc.want)
		}
	}
}

// TestDotpowersFamilyValidateBuildRouting pins #646 items 4/8: a green build
// reaches the reviews, a red build the rework budget, and "no known build
// system" (exit 1 + `validation-unknown` marker) the help/recover node —
// never the reviews (the old `validation-unknown` exit 0 shipped) and not the
// agent-driven rework loop (nothing for an agent to fix).
func TestDotpowersFamilyValidateBuildRouting(t *testing.T) {
	cases := []struct {
		dip, pass, help string
	}{
		{"dotpowers.dip", "ReviewFanOut", "HumanHelp"},
		{"dotpowers-auto.dip", "ReviewFanOut", "AutoRecover"},
		{"dotpowers-simple.dip", "FinalReview", "HumanHelp"},
		{"dotpowers-simple-auto.dip", "FinalReview", "AutoRecover"},
		{"test-kitchen.dip", "ReviewFanOut", "HumanHelp"},
		{"scenario-testing.dip", "ReviewFanOut", "HumanHelp"},
		{"kitchen-sink.dip", "ReviewFanOut", "AutoRecover"},
	}
	const noStack = "=== Running full validation ===\nERROR: no known build system (pyproject.toml / package.json / go.mod / Cargo.toml) — nothing can be validated; fix the project layout or add a build system\nvalidation-unknown"
	const red = "=== Running full validation ===\ndetected stacks: go \n--- go vet ---\na.go:1: unreachable\nVET_FAIL\nvalidation-fail"
	const green = "=== Running full validation ===\ndetected stacks: go \n--- go test ---\nok\nvalidation-pass"
	for _, tc := range cases {
		g := loadExampleWorkflow(t, tc.dip)
		if got := routeFrom(t, g, "ValidateBuild", "success", green); got != tc.pass {
			t.Errorf("%s green ValidateBuild routed to %s, want %s", tc.dip, got, tc.pass)
		}
		if got := routeFrom(t, g, "ValidateBuild", "fail", red); got != "CheckReworkBudget" {
			t.Errorf("%s red ValidateBuild routed to %s, want CheckReworkBudget", tc.dip, got)
		}
		if got := routeFrom(t, g, "ValidateBuild", "fail", noStack); got != tc.help {
			t.Errorf("%s no-build-system ValidateBuild routed to %s, want %s", tc.dip, got, tc.help)
		}
		for _, node := range []string{"ValidateBuild", "VerifyTestsFinal"} {
			if to := g.Nodes[node].Attrs["timeout"]; to != "5m0s" {
				t.Errorf("%s %s timeout = %q, want 5m0s (runs every detected stack)", tc.dip, node, to)
			}
		}
	}
}
