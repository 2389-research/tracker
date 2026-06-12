// ABOUTME: Tests for prompt variable expansion and pipeline context injection.
// ABOUTME: Verifies that human responses and prior node outputs are appended to LLM prompts.
package pipeline

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExpandPromptVariables_Goal(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set(ContextKeyGoal, "build a CLI tool")

	result := ExpandPromptVariables("Achieve $goal now", ctx)
	if result != "Achieve build a CLI tool now" {
		t.Fatalf("expected goal substitution, got %q", result)
	}
}

func TestExpandPromptVariables_NilContext(t *testing.T) {
	result := ExpandPromptVariables("no context $goal", nil)
	if result != "no context $goal" {
		t.Fatalf("expected no substitution with nil context, got %q", result)
	}
}

func TestInjectPipelineContext_HumanResponse(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set(ContextKeyHumanResponse, "Build me a todo app")

	result := InjectPipelineContext("Do the task.", ctx, 0)
	if !strings.Contains(result, "Build me a todo app") {
		t.Fatalf("expected human response in output, got %q", result)
	}
	if !strings.Contains(result, "Human Response") {
		t.Fatalf("expected Human Response header, got %q", result)
	}
}

func TestInjectPipelineContext_LastResponse(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set(ContextKeyLastResponse, "Previous node did X")

	result := InjectPipelineContext("Continue.", ctx, 0)
	if !strings.Contains(result, "Previous node did X") {
		t.Fatalf("expected last response in output, got %q", result)
	}
}

func TestInjectPipelineContext_NoContext(t *testing.T) {
	ctx := NewPipelineContext()
	result := InjectPipelineContext("Plain prompt.", ctx, 0)
	if result != "Plain prompt." {
		t.Fatalf("expected unchanged prompt with empty context, got %q", result)
	}
}

func TestInjectPipelineContext_NilContext(t *testing.T) {
	result := InjectPipelineContext("Plain prompt.", nil, 0)
	if result != "Plain prompt." {
		t.Fatalf("expected unchanged prompt with nil context, got %q", result)
	}
}

func TestInjectPipelineContext_BothKeys(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set(ContextKeyHumanResponse, "user said this")
	ctx.Set(ContextKeyLastResponse, "node said that")

	result := InjectPipelineContext("Do work.", ctx, 0)
	if !strings.Contains(result, "user said this") {
		t.Fatalf("expected human response, got %q", result)
	}
	if !strings.Contains(result, "node said that") {
		t.Fatalf("expected last response, got %q", result)
	}
}

func TestExpandGraphVariables_Basic(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("graph.target_name", "myapp")
	ctx.Set("graph.source_ref", "main")

	vars := GraphVarMap(ctx)
	result := ExpandGraphVariables("build $target_name from $source_ref", vars)
	if result != "build myapp from main" {
		t.Fatalf("expected graph variable expansion, got %q", result)
	}
}

func TestExpandGraphVariables_NoDollarSign(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("graph.target_name", "myapp")

	vars := GraphVarMap(ctx)
	result := ExpandGraphVariables("no variables here", vars)
	if result != "no variables here" {
		t.Fatalf("expected unchanged text, got %q", result)
	}
}

func TestExpandGraphVariables_GoalKey(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("graph.goal", "build a CLI tool")

	// $goal should be expanded via graph.goal.
	vars := GraphVarMap(ctx)
	result := ExpandGraphVariables("achieve $goal", vars)
	if result != "achieve build a CLI tool" {
		t.Fatalf("expected $goal expansion from graph.goal, got %q", result)
	}
}

func TestExpandGraphVariables_NilVars(t *testing.T) {
	result := ExpandGraphVariables("text with $var", nil)
	if result != "text with $var" {
		t.Fatalf("expected unchanged text with nil vars, got %q", result)
	}
}

func TestExpandGraphVariables_EmptyText(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("graph.foo", "bar")
	vars := GraphVarMap(ctx)
	result := ExpandGraphVariables("", vars)
	if result != "" {
		t.Fatalf("expected empty string, got %q", result)
	}
}

func TestExpandGraphVariables_IgnoresNonGraphKeys(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("outcome", "success")
	ctx.Set("graph.target", "foo")

	// $outcome should NOT be in the vars map (not a graph.* key).
	vars := GraphVarMap(ctx)
	result := ExpandGraphVariables("status=$outcome target=$target", vars)
	if result != "status=$outcome target=foo" {
		t.Fatalf("expected only graph vars expanded, got %q", result)
	}
}

func TestGraphVarMap_NilContext(t *testing.T) {
	vars := GraphVarMap(nil)
	if vars != nil {
		t.Fatalf("expected nil for nil context, got %v", vars)
	}
}

func TestExpandGraphVariablesPrefixCollision(t *testing.T) {
	// $target must never clobber the prefix of $target_name; map iteration
	// order is random, so run repeatedly to defeat lucky orderings.
	vars := map[string]string{
		"$target":      "prod",
		"$target_name": "api-service",
	}
	want := "deploy api-service to prod"
	for i := 0; i < 100; i++ {
		got := ExpandGraphVariables("deploy $target_name to $target", vars)
		if got != want {
			t.Fatalf("iteration %d: got %q, want %q", i, got, want)
		}
	}
}

func TestExpandGraphVariablesSinglePass(t *testing.T) {
	// Expansion is single-pass — a variable reference appearing inside a
	// substituted VALUE must never be re-expanded (CLAUDE.md: "Variable
	// expansion is single-pass — never re-scan resolved values"). Before
	// the single-pass rewrite this depended on replacement order: the
	// longest-first sequential ReplaceAll deterministically re-scanned
	// $long's substituted value for $a.
	vars := map[string]string{
		"$long": "literal $a inside",
		"$a":    "EXPANDED",
	}
	got := ExpandGraphVariables("value=$long flag=$a", vars)
	want := "value=literal $a inside flag=EXPANDED"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// --- #352 item 1: cap "Previous Node Output" at injection time ---

func TestInjectPipelineContext_CapsLargeLastResponse(t *testing.T) {
	ctx := NewPipelineContext()
	head := strings.Repeat("H", 3000)
	middle := strings.Repeat("M", 6000)
	tail := strings.Repeat("T", 3000)
	ctx.Set(ContextKeyLastResponse, head+middle+tail)

	result := InjectPipelineContext("Continue.", ctx, 0)

	// Head and tail of the prior output survive; the middle is elided.
	if !strings.Contains(result, strings.Repeat("H", 1000)) {
		t.Errorf("expected head of last_response to survive capping")
	}
	if !strings.Contains(result, strings.Repeat("T", 1000)) {
		t.Errorf("expected tail of last_response to survive capping")
	}
	if strings.Contains(result, strings.Repeat("M", 100)) {
		t.Errorf("expected middle of last_response to be elided")
	}
	if !strings.Contains(result, "elided") {
		t.Errorf("expected explicit elision marker, got %q", result[:min(len(result), 200)])
	}
	// The injected section is bounded: prompt + headers + cap + marker.
	if len(result) > DefaultInjectedResponseCap+500 {
		t.Errorf("injected prompt too large: %d bytes (cap %d)", len(result), DefaultInjectedResponseCap)
	}
}

func TestInjectPipelineContext_SmallLastResponseUncapped(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set(ContextKeyLastResponse, "short output")

	result := InjectPipelineContext("Continue.", ctx, 0)
	if !strings.Contains(result, "short output") {
		t.Fatalf("expected full short value, got %q", result)
	}
	if strings.Contains(result, "elided") {
		t.Fatalf("unexpected elision marker on small value: %q", result)
	}
}

func TestInjectPipelineContext_ExplicitCap(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set(ContextKeyLastResponse, strings.Repeat("x", 1000))

	result := InjectPipelineContext("Continue.", ctx, 100)
	if !strings.Contains(result, "elided") {
		t.Fatalf("expected elision at explicit cap 100")
	}
	if len(result) > 400 {
		t.Fatalf("expected result bounded by cap 100 plus marker/headers, got %d bytes", len(result))
	}
}

func TestInjectPipelineContext_NegativeCapUnlimited(t *testing.T) {
	ctx := NewPipelineContext()
	big := strings.Repeat("y", DefaultInjectedResponseCap*3)
	ctx.Set(ContextKeyLastResponse, big)

	result := InjectPipelineContext("Continue.", ctx, -1)
	if !strings.Contains(result, big) {
		t.Fatalf("expected full value with negative (unlimited) cap")
	}
}

func TestInjectPipelineContext_HumanResponseNotCapped(t *testing.T) {
	// The cap targets last_response (transcript paste); human responses are
	// human-typed and injected whole.
	ctx := NewPipelineContext()
	big := strings.Repeat("h", DefaultInjectedResponseCap*2)
	ctx.Set(ContextKeyHumanResponse, big)

	result := InjectPipelineContext("Continue.", ctx, 0)
	if !strings.Contains(result, big) {
		t.Fatalf("expected human_response injected whole")
	}
}

func TestInjectPipelineContext_CapDoesNotMutateContext(t *testing.T) {
	// The cap applies at prompt-injection time only — the stored context value
	// (and therefore node.<id>.last_response scoping and checkpoints) keeps
	// the full value.
	ctx := NewPipelineContext()
	full := strings.Repeat("z", DefaultInjectedResponseCap*2)
	ctx.Set(ContextKeyLastResponse, full)

	_ = InjectPipelineContext("Continue.", ctx, 0)

	got, _ := ctx.Get(ContextKeyLastResponse)
	if got != full {
		t.Fatalf("injection mutated stored context: len %d, want %d", len(got), len(full))
	}
}

func TestInjectPipelineContext_CapRespectsUTF8Boundaries(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set(ContextKeyLastResponse, strings.Repeat("é", 2000))

	result := InjectPipelineContext("Continue.", ctx, 101)
	if !utf8.ValidString(result) {
		t.Fatalf("capped injection produced invalid UTF-8")
	}
}
