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
