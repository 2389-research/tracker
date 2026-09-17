// ABOUTME: Conformance test (#647): every `when` condition in examples/**/*.dip evaluates identically
// ABOUTME: under tracker's adapter-serialized text and a reference evaluator over dippin's AST.
package pipeline

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/2389-research/dippin-lang/ir"
	"github.com/2389-research/dippin-lang/parser"
	"github.com/2389-research/dippin-lang/simulate"
)

// refEvalCondition mirrors dippin's (unexported) simulator.evalCondition:
// string comparison only, `in` splits on commas and trims, `ctx.` is stripped
// from variables before lookup, unknown variables resolve to "".
func refEvalCondition(expr ir.ConditionExpr, ctx map[string]string) bool {
	switch e := expr.(type) {
	case ir.CondCompare:
		return refEvalCompare(e, ctx)
	case ir.CondAnd:
		return refEvalCondition(e.Left, ctx) && refEvalCondition(e.Right, ctx)
	case ir.CondOr:
		return refEvalCondition(e.Left, ctx) || refEvalCondition(e.Right, ctx)
	case ir.CondNot:
		return !refEvalCondition(e.Inner, ctx)
	}
	return false
}

func refEvalCompare(e ir.CondCompare, ctx map[string]string) bool {
	actual := ctx[strings.TrimPrefix(e.Variable, "ctx.")]
	switch e.Op {
	case "=", "==":
		return actual == e.Value
	case "!=":
		return actual != e.Value
	case "contains":
		return strings.Contains(actual, e.Value)
	case "startswith":
		return strings.HasPrefix(actual, e.Value)
	case "endswith":
		return strings.HasSuffix(actual, e.Value)
	case "in":
		for _, p := range strings.Split(e.Value, ",") {
			if actual == strings.TrimSpace(p) {
				return true
			}
		}
	}
	return false
}

// collectConditionLeaves returns the variables and literal values referenced
// by the AST, sorted for deterministic fixtures.
func collectConditionLeaves(expr ir.ConditionExpr, vars, vals map[string]bool) {
	switch e := expr.(type) {
	case ir.CondCompare:
		vars[strings.TrimPrefix(e.Variable, "ctx.")] = true
		vals[e.Value] = true
		for _, p := range strings.Split(e.Value, ",") {
			vals[strings.TrimSpace(p)] = true
		}
	case ir.CondAnd:
		collectConditionLeaves(e.Left, vars, vals)
		collectConditionLeaves(e.Right, vars, vals)
	case ir.CondOr:
		collectConditionLeaves(e.Left, vars, vals)
		collectConditionLeaves(e.Right, vars, vals)
	case ir.CondNot:
		collectConditionLeaves(e.Inner, vars, vals)
	}
}

// conditionFixtures derives a context set from the condition's own literals:
// every variable takes every literal that appears (plus a superstring of it for
// the substring operators), the empty string, and an unrelated value, over the
// full cross product (capped).
func conditionFixtures(expr ir.ConditionExpr) []map[string]string {
	varSet, valSet := map[string]bool{}, map[string]bool{}
	collectConditionLeaves(expr, varSet, valSet)
	vars := sortedKeys(varSet)
	values := []string{"", "zz-unrelated-value"}
	for _, v := range sortedKeys(valSet) {
		values = append(values, v, "pre-"+v+"-post")
	}
	fixtures := []map[string]string{{}}
	for _, name := range vars {
		var next []map[string]string
		for _, base := range fixtures {
			for _, val := range values {
				m := make(map[string]string, len(base)+1)
				for k, v := range base {
					m[k] = v
				}
				m[name] = val
				next = append(next, m)
			}
		}
		fixtures = next
		if len(fixtures) > 4096 {
			fixtures = fixtures[:4096]
		}
	}
	return fixtures
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type exampleCondition struct {
	file string
	edge *ir.Edge
}

// loadExampleConditions parses every .dip under examples/ (recursively) with
// dippin's parser and returns each conditional edge. File directives are not
// resolved — only the edge conditions matter here.
func loadExampleConditions(t *testing.T) []exampleCondition {
	t.Helper()
	root := filepath.Join("..", "examples")
	var out []exampleCondition
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".dip" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		w, err := parser.NewParser(string(src), path).Parse()
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		for _, e := range w.Edges {
			if e.Condition != nil && strings.TrimSpace(e.Condition.Raw) != "" {
				out = append(out, exampleCondition{file: path, edge: e})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk examples: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no conditional edges found under examples/ — wrong working directory?")
	}
	return out
}

// TestExampleConditions_RoundTripWithDippinAST is the #647 conformance gate:
// for every `when` condition shipped in examples/, the text the adapter hands
// the engine must evaluate exactly like dippin's own AST evaluator over a
// fixture set derived from the condition's literals. On main this fails for
// the 14 word-form `and` conditions (dotpowers `PickNextTask`/`RunFormat`).
func TestExampleConditions_RoundTripWithDippinAST(t *testing.T) {
	conds := loadExampleConditions(t)
	wordForms := 0
	for _, c := range conds {
		raw := c.edge.Condition.Raw
		expr, err := simulate.ParseCondition(raw)
		if err != nil {
			t.Errorf("%s: edge %s -> %s: dippin rejects %q: %v", c.file, c.edge.From, c.edge.To, raw, err)
			continue
		}
		if strings.Contains(raw, " and ") || strings.Contains(raw, " or ") || strings.HasPrefix(raw, "not ") {
			wordForms++
		}
		// The adapter path, exactly as LoadDippinWorkflow drives it: Parsed is
		// left nil so the test exercises dippinConditionText's own parse.
		edgeCopy := *c.edge
		edgeCopy.Condition = &ir.Condition{Raw: raw}
		gEdge, err := convertEdge(&edgeCopy)
		if err != nil {
			t.Errorf("%s: edge %s -> %s: convertEdge(%q): %v", c.file, c.edge.From, c.edge.To, raw, err)
			continue
		}
		for _, fixture := range conditionFixtures(expr) {
			pc := NewPipelineContext()
			for k, v := range fixture {
				pc.Set(k, v)
			}
			got, err := EvaluateCondition(gEdge.Condition, pc)
			if err != nil {
				t.Errorf("%s: edge %s -> %s: EvaluateCondition(%q): %v", c.file, c.edge.From, c.edge.To, gEdge.Condition, err)
				break
			}
			if want := refEvalCondition(expr, fixture); got != want {
				t.Errorf("%s: edge %s -> %s\n  source:     %q\n  serialized: %q\n  ctx %v: tracker=%v dippin=%v", c.file, c.edge.From, c.edge.To, raw, gEdge.Condition, fixture, got, want)
				break
			}
		}
	}
	if wordForms == 0 {
		t.Error("expected at least one word-form (and/or/not) condition in examples/ — the gate would not exercise #647")
	}
	t.Logf("checked %d example conditions (%d word-form)", len(conds), wordForms)
}
