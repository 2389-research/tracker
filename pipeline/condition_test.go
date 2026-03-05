// ABOUTME: Tests for the condition expression evaluator used in edge gating.
// ABOUTME: Validates equality, inequality, AND logic, empty conditions, and variable resolution.
package pipeline

import (
	"testing"
)

func TestConditionEmptyAlwaysTrue(t *testing.T) {
	ctx := NewPipelineContext()
	result, err := EvaluateCondition("", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("empty condition should evaluate to true")
	}
}

func TestConditionWhitespaceAlwaysTrue(t *testing.T) {
	ctx := NewPipelineContext()
	result, err := EvaluateCondition("   ", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("whitespace-only condition should evaluate to true")
	}
}

func TestConditionSimpleEquals(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("outcome", "success")
	result, err := EvaluateCondition("outcome=success", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected outcome=success to be true")
	}
}

func TestConditionSimpleEqualsFailure(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("outcome", "fail")
	result, err := EvaluateCondition("outcome=success", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected outcome=success to be false when outcome is 'fail'")
	}
}

func TestConditionNotEquals(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("outcome", "fail")
	result, err := EvaluateCondition("outcome!=success", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected outcome!=success to be true when outcome is 'fail'")
	}
}

func TestConditionNotEqualsFailure(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("outcome", "success")
	result, err := EvaluateCondition("outcome!=success", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected outcome!=success to be false when outcome is 'success'")
	}
}

func TestConditionAND(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("outcome", "success")
	ctx.Set("tests_passed", "true")
	result, err := EvaluateCondition("outcome=success && tests_passed=true", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected AND condition to be true")
	}
}

func TestConditionANDPartialFail(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("outcome", "success")
	ctx.Set("tests_passed", "false")
	result, err := EvaluateCondition("outcome=success && tests_passed=true", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected AND condition to be false when one clause fails")
	}
}

func TestConditionMissingVariable(t *testing.T) {
	ctx := NewPipelineContext()
	result, err := EvaluateCondition("outcome=success", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected missing variable to evaluate to false for equality")
	}
}

func TestConditionMissingVariableNotEquals(t *testing.T) {
	ctx := NewPipelineContext()
	result, err := EvaluateCondition("outcome!=success", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected missing variable != 'success' to be true (empty string != success)")
	}
}

func TestConditionInvalidExpression(t *testing.T) {
	ctx := NewPipelineContext()
	_, err := EvaluateCondition("this has no operator", ctx)
	if err == nil {
		t.Error("expected error for invalid expression")
	}
}

func TestConditionTripleAND(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("a", "1")
	ctx.Set("b", "2")
	ctx.Set("c", "3")
	result, err := EvaluateCondition("a=1 && b=2 && c=3", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected triple AND to be true")
	}
}

func TestConditionNilContext(t *testing.T) {
	_, err := EvaluateCondition("outcome=success", nil)
	if err == nil {
		t.Fatal("expected error for nil context")
	}
}

func TestConditionNilContextEmptyExpr(t *testing.T) {
	result, err := EvaluateCondition("", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("empty condition with nil context should still be true")
	}
}

func TestConditionWithSpaces(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("outcome", "success")
	result, err := EvaluateCondition("  outcome = success  ", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected condition with extra spaces to work")
	}
}
