// ABOUTME: Regression tests for #647 — dippin word conjunctions (and/or/not) in edge conditions.
// ABOUTME: Covers the adapter's AST serialization, the parser's word-form synonyms, and a runtime loop-exit proof.
package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/2389-research/dippin-lang/ir"
	"github.com/2389-research/dippin-lang/simulate"
)

// The exact repro from #647: with tool_stdout=all_complete the "has task" edge
// must be FALSE. On main the word form was parsed as one clause comparing
// against the literal `all_complete and ctx.tool_stdout != no_tasks_found`.
func TestEvaluateCondition_WordAnd_IssueRepro(t *testing.T) {
	pc := NewPipelineContext()
	pc.Set("tool_stdout", "all_complete")

	for _, cond := range []string{
		"ctx.tool_stdout != all_complete and ctx.tool_stdout != no_tasks_found",
		"ctx.tool_stdout != all_complete && ctx.tool_stdout != no_tasks_found",
	} {
		got, err := EvaluateCondition(cond, pc)
		if err != nil {
			t.Fatalf("EvaluateCondition(%q): %v", cond, err)
		}
		if got {
			t.Errorf("EvaluateCondition(%q) with tool_stdout=all_complete = true, want false", cond)
		}
	}

	pc.Set("tool_stdout", "task-3")
	got, err := EvaluateCondition("ctx.tool_stdout != all_complete and ctx.tool_stdout != no_tasks_found", pc)
	if err != nil || !got {
		t.Errorf("with a real task the has-task edge must be true (got %v, err %v)", got, err)
	}
}

func TestParseCondition_WordForms(t *testing.T) {
	cases := []struct {
		name   string
		expr   string
		ctx    map[string]string
		want   bool
		errSub string
	}{
		{name: "or word", expr: "ctx.a = 1 or ctx.b = 2", ctx: map[string]string{"a": "9", "b": "2"}, want: true},
		{name: "or/and precedence", expr: "ctx.a = 1 or ctx.b = 2 and ctx.c = 3", ctx: map[string]string{"a": "0", "b": "2", "c": "3"}, want: true},
		{name: "mixed symbolic and word", expr: "ctx.a = 1 || ctx.b = 2 and ctx.c = 0", ctx: map[string]string{"a": "0", "b": "2", "c": "3"}, want: false},
		{name: "quoted value containing and", expr: `ctx.msg = "rock and roll"`, ctx: map[string]string{"msg": "rock and roll"}, want: true},
		{name: "quoted value containing or", expr: `ctx.msg = "this or that" and ctx.x = 1`, ctx: map[string]string{"msg": "this or that", "x": "1"}, want: true},
		{name: "keyword inside identifier is not split", expr: "ctx.error = boring", ctx: map[string]string{"error": "boring"}, want: true},
		{name: "not prefix with word and", expr: "not ctx.a = 1 and ctx.b = 2", ctx: map[string]string{"a": "1", "b": "2"}, want: false},
		{name: "not contains", expr: "ctx.tool_stdout not contains all-done", ctx: map[string]string{"tool_stdout": "still going"}, want: true},
		{name: "not contains negative", expr: "ctx.tool_stdout not contains all-done", ctx: map[string]string{"tool_stdout": "all-done now"}, want: false},
		{name: "tab-delimited word", expr: "ctx.a = 1\tand\tctx.b = 2", ctx: map[string]string{"a": "1", "b": "2"}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pc := NewPipelineContext()
			for k, v := range tc.ctx {
				pc.Set(k, v)
			}
			got, err := EvaluateCondition(tc.expr, pc)
			if tc.errSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errSub) {
					t.Fatalf("EvaluateCondition(%q) err = %v, want containing %q", tc.expr, err, tc.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("EvaluateCondition(%q): %v", tc.expr, err)
			}
			if got != tc.want {
				t.Errorf("EvaluateCondition(%q) = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

// A dangling or bare keyword can never be routed on as a literal: validation
// (tracker validate) and the adapter both reject it, and the runtime errors
// when it reaches the empty clause.
func TestWordConjunction_DanglingKeywordIsLoud(t *testing.T) {
	for _, expr := range []string{"ctx.a = 1 and", "ctx.a = and", "or ctx.a = 1", "ctx.a = 1 or ctx.b = 2 and"} {
		if err := checkConditionSyntax(expr, NewPipelineContext()); err == nil {
			t.Errorf("checkConditionSyntax(%q) = nil, want error", expr)
		}
	}
	for _, expr := range []string{"ctx.a = 1 and", "or ctx.a = 1", "ctx.a = 1 or ctx.b = 2 and"} {
		_, err := convertEdge(&ir.Edge{From: "a", To: "b", Condition: &ir.Condition{Raw: expr}})
		if err == nil {
			t.Errorf("convertEdge(%q) = nil error, want loud rejection", expr)
		}
	}
	// dippin reads `ctx.a = and` as a comparison against the literal token
	// "and"; the adapter quotes it so tracker compares the same literal.
	got, err := convertEdge(&ir.Edge{From: "a", To: "b", Condition: &ir.Condition{Raw: "ctx.a = and"}})
	if err != nil {
		t.Fatalf("convertEdge(ctx.a = and): %v", err)
	}
	if got.Condition != `ctx.a = "and"` {
		t.Errorf("convertEdge(ctx.a = and) = %q, want ctx.a = \"and\"", got.Condition)
	}
	pc := NewPipelineContext()
	pc.Set("a", "1")
	if _, err := EvaluateCondition("ctx.a = 1 and", pc); err == nil || !strings.Contains(err.Error(), "invalid condition clause") {
		t.Errorf("EvaluateCondition(dangling and) err = %v, want invalid condition clause", err)
	}
}

func cmp(v, op, val string) ir.CondCompare { return ir.CondCompare{Variable: v, Op: op, Value: val} }

func TestSerializeDippinCondition(t *testing.T) {
	cases := []struct {
		name string
		expr ir.ConditionExpr
		want string
	}{
		{
			name: "issue repro",
			expr: ir.CondAnd{Left: cmp("ctx.tool_stdout", "!=", "all_complete"), Right: cmp("ctx.tool_stdout", "!=", "no_tasks_found")},
			want: "ctx.tool_stdout != all_complete && ctx.tool_stdout != no_tasks_found",
		},
		{
			name: "infix not contains",
			expr: ir.CondNot{Inner: cmp("ctx.tool_stdout", "contains", "all-done")},
			want: "ctx.tool_stdout not contains all-done",
		},
		{
			name: "a and (b or c) distributes",
			expr: ir.CondAnd{Left: cmp("ctx.a", "=", "1"), Right: ir.CondOr{Left: cmp("ctx.b", "=", "2"), Right: cmp("ctx.c", "=", "3")}},
			want: "ctx.a = 1 && ctx.b = 2 || ctx.a = 1 && ctx.c = 3",
		},
		{
			name: "not (a and b) De Morgan",
			expr: ir.CondNot{Inner: ir.CondAnd{Left: cmp("ctx.a", "=", "1"), Right: cmp("ctx.b", "in", "x,y")}},
			want: "ctx.a != 1 || ctx.b not in x,y",
		},
		{
			name: "not (a or b) De Morgan",
			expr: ir.CondNot{Inner: ir.CondOr{Left: cmp("ctx.a", "==", "1"), Right: cmp("ctx.b", "!=", "2")}},
			want: "ctx.a != 1 && ctx.b = 2",
		},
		{
			name: "double negation",
			expr: ir.CondNot{Inner: ir.CondNot{Inner: cmp("ctx.a", "startswith", "x")}},
			want: "ctx.a startswith x",
		},
		{
			name: "quoted value containing and",
			expr: cmp("ctx.msg", "=", "rock and roll"),
			want: `ctx.msg = "rock and roll"`,
		},
		{
			name: "bare keyword value is quoted",
			expr: cmp("ctx.msg", "=", "and"),
			want: `ctx.msg = "and"`,
		},
		{
			name: "empty value is quoted",
			expr: cmp("ctx.msg", "=", ""),
			want: `ctx.msg = ""`,
		},
		{
			name: "quote and backslash escaped",
			expr: cmp("ctx.msg", "=", `say "hi\"`),
			want: `ctx.msg = "say \"hi\\\""`,
		},
		{
			name: "== spelling preserved",
			expr: cmp("ctx.status", "==", "success"),
			want: "ctx.status == success",
		},
		{
			name: "pointer nodes",
			expr: &ir.CondAnd{Left: &ir.CondCompare{Variable: "ctx.a", Op: "=", Value: "1"}, Right: &ir.CondNot{Inner: &ir.CondCompare{Variable: "ctx.b", Op: "=", Value: "2"}}},
			want: "ctx.a = 1 && ctx.b != 2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SerializeDippinCondition(tc.expr)
			if err != nil {
				t.Fatalf("SerializeDippinCondition: %v", err)
			}
			if got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
			// Every serialization must be accepted by tracker's parser.
			if _, err := ParseCondition(got); err != nil {
				t.Errorf("ParseCondition(%q): %v", got, err)
			}
		})
	}
}

// Every value that gets quoted must decode back to the original literal
// through tracker's own operand normalizer.
func TestQuoteConditionValue_RoundTrip(t *testing.T) {
	for _, v := range []string{"", "and", "a b", `q"uote`, `back\slash`, `\"`, `\\`, `a = b`, `x && y`, `(paren)`, `a, b, c`, `plain`} {
		spelled := quoteConditionValue(v)
		if got := normalizeConditionOperand(spelled); got != v {
			t.Errorf("value %q spelled %q decoded to %q", v, spelled, got)
		}
		if _, err := ParseCondition("ctx.k = " + spelled); err != nil {
			t.Errorf("ParseCondition(ctx.k = %s): %v", spelled, err)
		}
	}
}

func TestSerializeDippinCondition_TooComplex(t *testing.T) {
	// (a1 or a2) and (b1 or b2) and ... — each AND of a 2-way OR doubles the
	// DNF; 7 factors → 128 branches > MaxConditionDNFBranches.
	var expr ir.ConditionExpr
	for i := 0; i < 7; i++ {
		factor := ir.CondOr{Left: cmp("ctx.x", "=", "1"), Right: cmp("ctx.y", "=", "2")}
		if expr == nil {
			expr = factor
		} else {
			expr = ir.CondAnd{Left: expr, Right: factor}
		}
	}
	_, err := SerializeDippinCondition(expr)
	if !errors.Is(err, ErrConditionTooComplex) {
		t.Fatalf("err = %v, want ErrConditionTooComplex", err)
	}
	// And the adapter names the edge.
	_, err = convertEdge(&ir.Edge{From: "src", To: "dst", Condition: &ir.Condition{Raw: "big", Parsed: expr}})
	if !errors.Is(err, ErrConditionTooComplex) || !strings.Contains(err.Error(), "src -> dst") {
		t.Fatalf("convertEdge err = %v, want ErrConditionTooComplex naming src -> dst", err)
	}
}

func TestSerializeDippinCondition_UnsupportedOp(t *testing.T) {
	_, err := SerializeDippinCondition(cmp("ctx.a", "~=", "x"))
	if !errors.Is(err, ErrUnsupportedConditionOp) {
		t.Fatalf("err = %v, want ErrUnsupportedConditionOp", err)
	}
}

// convertEdge parses a Raw-only word-form condition with dippin's parser
// rather than handing the text to tracker's parser verbatim.
func TestConvertEdge_RawWordForm_ParsedWithDippin(t *testing.T) {
	edge := &ir.Edge{From: "a", To: "b", Condition: &ir.Condition{
		Raw: "ctx.tool_stdout != all_complete and ctx.tool_stdout != no_tasks_found",
	}}
	got, err := convertEdge(edge)
	if err != nil {
		t.Fatalf("convertEdge: %v", err)
	}
	if want := "ctx.tool_stdout != all_complete && ctx.tool_stdout != no_tasks_found"; got.Condition != want {
		t.Errorf("edge.Condition = %q, want %q", got.Condition, want)
	}
}

// A Raw-only condition dippin's parser rejects (tracker's superset dialect)
// still falls back to Raw and is validated by tracker's parser.
func TestConvertEdge_RawFallback_TrackerSuperset(t *testing.T) {
	edge := &ir.Edge{From: "a", To: "b", Condition: &ir.Condition{Raw: "ctx.count >= 5 and ctx.name matches ^x"}}
	if _, err := simulate.ParseCondition(edge.Condition.Raw); err == nil {
		t.Fatal("precondition: dippin must reject the tracker-only operators for this test to exercise the fallback")
	}
	got, err := convertEdge(edge)
	if err != nil {
		t.Fatalf("convertEdge: %v", err)
	}
	pc := NewPipelineContext()
	pc.Set("count", "7")
	pc.Set("name", "xyz")
	ok, err := EvaluateCondition(got.Condition, pc)
	if err != nil || !ok {
		t.Fatalf("EvaluateCondition(%q) = %v, %v; want true", got.Condition, ok, err)
	}
}

// The loaded .dip path: dippin's validator populates Parsed, and the adapter
// serializes it. This is the load-time shape of the dotpowers task loop.
const pickNextTaskDip = `workflow pick_next_task
  start: PickNextTask
  exit: Done

  tool PickNextTask
    command: "true"
    timeout: 10s

  tool ImplementTask
    command: "true"
    timeout: 10s

  tool ValidateBuild
    command: "true"
    timeout: 10s

  tool Done
    command: "true"
    timeout: 10s

  edges
    PickNextTask -> ImplementTask  when ctx.tool_stdout != all_complete and ctx.tool_stdout != no_tasks_found  label: "has task"
    PickNextTask -> ValidateBuild  when ctx.tool_stdout = all_complete  label: "all done"
    PickNextTask -> Done  label: fallback
    ImplementTask -> PickNextTask  loop
    ValidateBuild -> Done
`

func TestLoadDippinWorkflow_WordAnd_SerializedFromAST(t *testing.T) {
	g, diags, err := LoadDippinWorkflow(pickNextTaskDip, "pick_next_task.dip")
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v\n%v", err, diags)
	}
	var hasTask *Edge
	for _, e := range g.Edges {
		if e.From == "PickNextTask" && e.To == "ImplementTask" {
			hasTask = e
		}
	}
	if hasTask == nil {
		t.Fatal("has-task edge missing")
	}
	if want := "ctx.tool_stdout != all_complete && ctx.tool_stdout != no_tasks_found"; hasTask.Condition != want {
		t.Errorf("has-task condition = %q, want %q", hasTask.Condition, want)
	}
}

// Runtime proof: a PickNextTask-shaped loop exits on all_complete. The stub
// PickNextTask handler emits one real task, then all_complete; on main the
// engine re-entered ImplementTask forever (the test would trip the visit cap).
func TestEngine_WordAndCondition_ExitsTaskLoop(t *testing.T) {
	g, _, err := LoadDippinWorkflow(pickNextTaskDip, "pick_next_task.dip")
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v", err)
	}

	reg := NewHandlerRegistry()
	picks := 0
	reg.Register(&testHandler{name: "tool", executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		if node.ID != "PickNextTask" {
			return Outcome{Status: OutcomeSuccess}, nil
		}
		picks++
		if picks > 5 {
			return Outcome{Status: OutcomeFail}, errors.New("PickNextTask visited more than 5 times — the task loop never exits")
		}
		stdout := "task-1"
		if picks >= 2 {
			stdout = "all_complete"
		}
		return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"tool_stdout": stdout}}, nil
	}})

	result, err := NewEngine(g, reg).Run(context.Background())
	if err != nil {
		t.Fatalf("engine run: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("status = %s, want success", result.Status)
	}
	var path []string
	for _, e := range result.Trace.Entries {
		path = append(path, e.NodeID)
	}
	want := "PickNextTask ImplementTask PickNextTask ValidateBuild Done"
	if got := strings.Join(path, " "); got != want {
		t.Errorf("trace path = %q, want %q", got, want)
	}
}
