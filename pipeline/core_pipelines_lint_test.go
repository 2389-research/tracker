// ABOUTME: Grade guard following the dippin v0.48 CLI bump — pins the harder
// ABOUTME: no-lint-errors floor in Go so a future dippin bump fails CI in-repo, not just via `make doctor`.
package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

// loadAskAndExecute loads the embedded-on-disk examples/ask_and_execute.dip
// the same way the binary embeds it. Mirrors loadBuildProduct.
func loadAskAndExecute(t *testing.T) *Graph {
	t.Helper()
	path := filepath.Join("..", "examples", "ask_and_execute.dip")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	g, _, err := LoadDippinWorkflow(string(source), "ask_and_execute.dip")
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v", err)
	}
	return g
}

// TestCorePipelinesLintClean guards that the three shipped core pipelines
// carry zero lint ERRORS. LoadDippinWorkflow already returns a fatal error
// for any DIP001-009 validation error; dippin-lang's DIP1xx lint checks are
// warnings-only and never fail the load, so a clean load (via loadBuildProduct
// / loadBuildProductSuperspec / loadAskAndExecute, which all t.Fatal on error)
// is itself the no-errors assertion. Lint warnings are allowed and logged for
// visibility; the grade floor (A) is enforced by `dippin doctor` in CI.
func TestCorePipelinesLintClean(t *testing.T) {
	cases := []struct {
		name string
		load func(t *testing.T) *Graph
	}{
		{"build_product.dip", loadBuildProduct},
		{"build_product_with_superspec.dip", loadBuildProductSuperspec},
		{"ask_and_execute.dip", loadAskAndExecute},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := c.load(t) // fatals on any lint/validation error
			if len(g.LintWarnings) > 0 {
				t.Logf("%s carries %d lint warnings: %v", c.name, len(g.LintWarnings), g.LintWarnings)
			}
		})
	}
}
