// ABOUTME: Regression tests for SIFT-SUB-03-01 — one parsed condition model shared
// ABOUTME: by runtime eval, syntax validation, and variable analysis; typed==raw.
package pipeline

import (
	"errors"
	"testing"
)

// TestParseCondition_Structure pins the OR/AND/clause split produced by the
// single shared parser. Every text consumer walks this structure, so the split
// is asserted here once instead of in each consumer's suite.
func TestParseCondition_Structure(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want [][]string // branches -> clauses
	}{
		{"empty", "", nil},
		{"whitespace", "   ", nil},
		{"single clause", "outcome = success", [][]string{{"outcome = success"}}},
		{"and only", "a=1 && b=2 && c=3", [][]string{{"a=1", "b=2", "c=3"}}},
		{"or only", "a=1 || b=2", [][]string{{"a=1"}, {"b=2"}}},
		{"mixed precedence", "a=1 || b=2 && c=3", [][]string{{"a=1"}, {"b=2", "c=3"}}},
		{"quoted delimiters ignored", `msg = "a || b && c"`, [][]string{{`msg = "a || b && c"`}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cc, err := ParseCondition(tc.expr)
			if err != nil {
				t.Fatalf("ParseCondition(%q) error: %v", tc.expr, err)
			}
			if len(cc.Branches) != len(tc.want) {
				t.Fatalf("branches = %d, want %d (%+v)", len(cc.Branches), len(tc.want), cc.Branches)
			}
			for i, wantClauses := range tc.want {
				got := cc.Branches[i].Clauses
				if len(got) != len(wantClauses) {
					t.Fatalf("branch %d clauses = %v, want %v", i, got, wantClauses)
				}
				for j, wc := range wantClauses {
					if got[j] != wc {
						t.Errorf("branch %d clause %d = %q, want %q", i, j, got[j], wc)
					}
				}
			}
		})
	}
}

// TestParseCondition_RejectsParens pins the single paren policy. The runtime
// dialect has no grouping parentheses; a parenthesized expression is rejected at
// parse time rather than silently mis-routed.
func TestParseCondition_RejectsParens(t *testing.T) {
	for _, expr := range []string{
		"a=1 || (b=2 && c=3)",
		"(a=1 || b=2) && c=3",
		"(a=1)",
	} {
		if _, err := ParseCondition(expr); !errors.Is(err, ErrParenthesizedCondition) {
			t.Errorf("ParseCondition(%q) error = %v, want ErrParenthesizedCondition", expr, err)
		}
	}
	// Parentheses inside a quoted literal are data, not grouping — accepted.
	if _, err := ParseCondition(`msg = "(hi)"`); err != nil {
		t.Errorf("ParseCondition with quoted parens error = %v, want nil", err)
	}
}

// TestParseCondition_UnmatchedQuote surfaces the shared quote scanner's error.
func TestParseCondition_UnmatchedQuote(t *testing.T) {
	if _, err := ParseCondition(`msg = "oops`); err == nil {
		t.Error("ParseCondition with unmatched quote: expected error, got nil")
	}
}

// TestConditionConsumers_ShareOneModel is the truth table the finding asks for:
// for each condition + context, the runtime evaluator, the syntax validator, and
// the variable-reference extractor must all agree because they read the same
// parsed model. A drift between any two consumers fails here.
func TestConditionConsumers_ShareOneModel(t *testing.T) {
	cases := []struct {
		name     string
		cond     string
		ctx      map[string]string
		wantEval bool
		wantVars []string // left operands, in order
	}{
		{
			name:     "single equality true",
			cond:     "ctx.outcome = success",
			ctx:      map[string]string{"outcome": "success"},
			wantEval: true,
			wantVars: []string{"outcome"},
		},
		{
			name:     "and both true",
			cond:     "outcome = success && tests_passed = true",
			ctx:      map[string]string{"outcome": "success", "tests_passed": "true"},
			wantEval: true,
			wantVars: []string{"outcome", "tests_passed"},
		},
		{
			name:     "or short-circuits",
			cond:     "outcome = success || status = running",
			ctx:      map[string]string{"outcome": "fail", "status": "running"},
			wantEval: true,
			wantVars: []string{"outcome", "status"},
		},
		{
			name:     "mixed precedence and binds tighter",
			cond:     "outcome = success || status = running && ready = yes",
			ctx:      map[string]string{"outcome": "fail", "status": "running", "ready": "no"},
			wantEval: false, // (status=running && ready=no) is false, outcome=success is false
			wantVars: []string{"outcome", "status", "ready"},
		},
		{
			name:     "not negation",
			cond:     "not outcome = success",
			ctx:      map[string]string{"outcome": "fail"},
			wantEval: true,
			wantVars: []string{"outcome"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := NewPipelineContextFrom(tc.ctx)

			// Consumer 1: runtime evaluator.
			got, err := EvaluateCondition(tc.cond, ctx)
			if err != nil {
				t.Fatalf("EvaluateCondition error: %v", err)
			}
			if got != tc.wantEval {
				t.Errorf("EvaluateCondition(%q) = %v, want %v", tc.cond, got, tc.wantEval)
			}

			// Consumer 2: syntax validator must accept a well-formed condition.
			if err := checkConditionSyntax(tc.cond, NewPipelineContext()); err != nil {
				t.Errorf("checkConditionSyntax(%q) rejected a valid condition: %v", tc.cond, err)
			}

			// Consumer 3: variable-reference extractor sees the same clauses.
			operands := conditionVarOperands(tc.cond)
			if len(operands) != len(tc.wantVars) {
				t.Fatalf("conditionVarOperands(%q) = %d operands, want %d (%+v)",
					tc.cond, len(operands), len(tc.wantVars), operands)
			}
			for i, want := range tc.wantVars {
				if operands[i].key != want {
					t.Errorf("operand %d = %q, want %q", i, operands[i].key, want)
				}
			}
		})
	}
}

// TestParenCondition_RejectedByEveryConsumer proves the divergence in the
// finding is closed: a parenthesized condition is now rejected consistently by
// the runtime evaluator and the syntax validator, not silently mis-evaluated.
// (convertEdge rejection — for both Raw and Parsed IR sources — is pinned in
// TestConvertEdge_RawParens_RejectedConsistently and
// TestConvertEdge_ParsedFallback_MixedPrecedenceRejected.)
func TestParenCondition_RejectedByEveryConsumer(t *testing.T) {
	const cond = "a=1 || (b=2 && c=3)"
	ctx := NewPipelineContextFrom(map[string]string{"a": "1"})

	if _, err := EvaluateCondition(cond, ctx); !errors.Is(err, ErrParenthesizedCondition) {
		t.Errorf("EvaluateCondition(%q) error = %v, want ErrParenthesizedCondition", cond, err)
	}
	if err := checkConditionSyntax(cond, NewPipelineContext()); !errors.Is(err, ErrParenthesizedCondition) {
		t.Errorf("checkConditionSyntax(%q) error = %v, want ErrParenthesizedCondition", cond, err)
	}
}

// TestSelectEdge_RoutesByCompiledCondition pins runtime edge selection through
// the unified model: the matching conditional edge is chosen, and when it does
// not match routing falls back to the unconditional edge — the existing routing
// semantics the refactor must preserve.
func TestSelectEdge_RoutesByCompiledCondition(t *testing.T) {
	g := NewGraph("route")
	g.AddNode(&Node{ID: "branch", Handler: "tool"})
	g.AddNode(&Node{ID: "A", Handler: "tool"})
	g.AddNode(&Node{ID: "B", Handler: "tool"})
	g.AddEdge(&Edge{From: "branch", To: "A", Condition: "ctx.outcome = success"})
	g.AddEdge(&Edge{From: "branch", To: "B"}) // unconditional fallback
	engine := NewEngine(g, newTestRegistryWithOutcomes(nil))
	edges := g.OutgoingEdges("branch")

	success := NewPipelineContextFrom(map[string]string{"outcome": "success"})
	edge, err := engine.selectEdge("run", edges, success)
	if err != nil {
		t.Fatalf("selectEdge error: %v", err)
	}
	if edge == nil || edge.To != "A" {
		t.Fatalf("selectEdge picked %v, want edge to A", edge)
	}

	fail := NewPipelineContextFrom(map[string]string{"outcome": "fail"})
	edge, err = engine.selectEdge("run", edges, fail)
	if err != nil {
		t.Fatalf("selectEdge error: %v", err)
	}
	if edge == nil || edge.To != "B" {
		t.Fatalf("selectEdge picked %v, want fallback edge to B", edge)
	}
}
