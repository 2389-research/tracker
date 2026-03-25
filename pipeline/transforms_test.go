// ABOUTME: Tests for prompt variable expansion and pipeline context injection.
// ABOUTME: Verifies that human responses and prior node outputs are appended to LLM prompts.
package pipeline

import (
	"strings"
	"testing"
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

	result := InjectPipelineContext("Do the task.", ctx)
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

	result := InjectPipelineContext("Continue.", ctx)
	if !strings.Contains(result, "Previous node did X") {
		t.Fatalf("expected last response in output, got %q", result)
	}
}

func TestInjectPipelineContext_NoContext(t *testing.T) {
	ctx := NewPipelineContext()
	result := InjectPipelineContext("Plain prompt.", ctx)
	if result != "Plain prompt." {
		t.Fatalf("expected unchanged prompt with empty context, got %q", result)
	}
}

func TestInjectPipelineContext_NilContext(t *testing.T) {
	result := InjectPipelineContext("Plain prompt.", nil)
	if result != "Plain prompt." {
		t.Fatalf("expected unchanged prompt with nil context, got %q", result)
	}
}

func TestInjectPipelineContext_BothKeys(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set(ContextKeyHumanResponse, "user said this")
	ctx.Set(ContextKeyLastResponse, "node said that")

	result := InjectPipelineContext("Do work.", ctx)
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
