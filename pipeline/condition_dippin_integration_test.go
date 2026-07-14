// ABOUTME: Real dippin parser-to-tracker regressions for quoted edge conditions.
// ABOUTME: Proves escaped quotes/backslashes survive adapter loading and malformed quotes retain source locations.
package pipeline

import (
	"strings"
	"testing"
)

func TestLoadDippinWorkflowPreservesEscapedQuotesInCondition(t *testing.T) {
	source := `workflow condition_quotes
  start: Emit
  exit: Done

  tool Emit
    command: true

  tool Done
    command: true

  edges
    Emit -> Done when ctx.tool_stdout = "say \"alpha||beta\""
`
	graph, diagnostics, err := LoadDippinWorkflow(source, "escaped_condition.dip")
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v\ndiagnostics: %v", err, diagnostics)
	}
	if len(graph.Edges) != 1 {
		t.Fatalf("edge count = %d, want 1", len(graph.Edges))
	}
	ctx := NewPipelineContext()
	ctx.Set("tool_stdout", `say "alpha||beta"`)
	got, err := EvaluateCondition(graph.Edges[0].Condition, ctx)
	if err != nil {
		t.Fatalf("EvaluateCondition(%q): %v", graph.Edges[0].Condition, err)
	}
	if !got {
		t.Fatalf("loaded condition %q did not match literal quoted tool output", graph.Edges[0].Condition)
	}
}

func TestLoadDippinWorkflowPreservesEscapedBackslashInCondition(t *testing.T) {
	source := `workflow condition_backslash
  start: Emit
  exit: Done

  tool Emit
    command: true

  tool Done
    command: true

  edges
    Emit -> Done when ctx.path = "C:\\"
`
	graph, diagnostics, err := LoadDippinWorkflow(source, "escaped_backslash_condition.dip")
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v\ndiagnostics: %v", err, diagnostics)
	}
	if len(graph.Edges) != 1 {
		t.Fatalf("edge count = %d, want 1", len(graph.Edges))
	}
	const wantCondition = `ctx.path = "C:\\"`
	if got := graph.Edges[0].Condition; got != wantCondition {
		t.Fatalf("adapted condition = %q, want exact raw form %q", got, wantCondition)
	}
	ctx := NewPipelineContext()
	ctx.Set("path", `C:\`)
	got, err := EvaluateCondition(graph.Edges[0].Condition, ctx)
	if err != nil {
		t.Fatalf("EvaluateCondition(%q): %v", graph.Edges[0].Condition, err)
	}
	if !got {
		t.Fatalf("loaded condition %q did not match path ending in one literal backslash", graph.Edges[0].Condition)
	}
}

func TestLoadDippinWorkflowUnmatchedDoubleQuoteIncludesSourceLocation(t *testing.T) {
	source := `workflow condition_quotes
  start: Emit
  exit: Done

  tool Emit
    command: true

  tool Done
    command: true

  edges
    Emit -> Done when ctx.tool_stdout = "unterminated
`
	const filename = "unmatched_condition.dip"
	_, diagnostics, err := LoadDippinWorkflow(source, filename)
	if err == nil {
		t.Fatal("LoadDippinWorkflow error = nil, want unmatched double quote error")
	}
	if !strings.Contains(err.Error(), " at 12:") {
		t.Fatalf("error %q does not include the condition's line and column in %s\ndiagnostics: %v", err, filename, diagnostics)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unterminated string literal") {
		t.Fatalf("error %q does not identify the unmatched double quote", err)
	}
}
