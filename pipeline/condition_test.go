// ABOUTME: Tests for the condition expression evaluator used in edge gating.
// ABOUTME: Validates equality, inequality, AND/OR logic, string operators, negation, and variable resolution.
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

// --- contains operator ---

func TestConditionContainsMatch(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("message", "hello world")
	result, err := EvaluateCondition("message contains world", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected 'hello world' contains 'world' to be true")
	}
}

func TestConditionContainsNoMatch(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("message", "hello world")
	result, err := EvaluateCondition("message contains planet", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected 'hello world' contains 'planet' to be false")
	}
}

// --- startswith operator ---

func TestConditionStartswithMatch(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("path", "/api/v1/users")
	result, err := EvaluateCondition("path startswith /api", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected '/api/v1/users' startswith '/api' to be true")
	}
}

func TestConditionStartswithNoMatch(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("path", "/web/home")
	result, err := EvaluateCondition("path startswith /api", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected '/web/home' startswith '/api' to be false")
	}
}

// --- endswith operator ---

func TestConditionEndswithMatch(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("file", "report.pdf")
	result, err := EvaluateCondition("file endswith .pdf", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected 'report.pdf' endswith '.pdf' to be true")
	}
}

func TestConditionEndswithNoMatch(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("file", "report.pdf")
	result, err := EvaluateCondition("file endswith .csv", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected 'report.pdf' endswith '.csv' to be false")
	}
}

// --- in operator ---

func TestConditionInMatch(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("env", "staging")
	result, err := EvaluateCondition("env in dev,staging,prod", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected 'staging' in 'dev,staging,prod' to be true")
	}
}

func TestConditionInNoMatch(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("env", "test")
	result, err := EvaluateCondition("env in dev,staging,prod", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected 'test' in 'dev,staging,prod' to be false")
	}
}

func TestConditionInSingleValue(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("env", "prod")
	result, err := EvaluateCondition("env in prod", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected 'prod' in 'prod' to be true")
	}
}

// --- || OR operator ---

func TestConditionORTrueOrFalse(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("a", "1")
	result, err := EvaluateCondition("a=1 || a=2", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected true || false to be true")
	}
}

func TestConditionORFalseOrFalse(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("a", "3")
	result, err := EvaluateCondition("a=1 || a=2", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected false || false to be false")
	}
}

// --- not negation ---

func TestConditionNotNegation(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("outcome", "fail")
	result, err := EvaluateCondition("not outcome=success", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected 'not outcome=success' to be true when outcome is 'fail'")
	}
}

func TestConditionNotNegationFalse(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("outcome", "success")
	result, err := EvaluateCondition("not outcome=success", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected 'not outcome=success' to be false when outcome is 'success'")
	}
}

// --- combined operators ---

func TestConditionORWithContains(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("key", "value")
	ctx.Set("key2", "something interesting")
	result, err := EvaluateCondition("key=value || key2 contains something", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected combined OR with contains to be true")
	}
}

func TestConditionANDORPrecedence(t *testing.T) {
	// a=1 && b=2 || c=3 should parse as (a=1 && b=2) || (c=3)
	ctx := NewPipelineContext()
	ctx.Set("a", "1")
	ctx.Set("b", "wrong")
	ctx.Set("c", "3")
	result, err := EvaluateCondition("a=1 && b=2 || c=3", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected (a=1 && b=2) || c=3 to be true when c=3 (AND has higher precedence)")
	}
}

// --- ctx. namespace prefix ---

func TestConditionCtxDotPrefix(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("outcome", "success")
	result, err := EvaluateCondition("ctx.outcome = success", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected 'ctx.outcome = success' to match when outcome=success")
	}
}

func TestConditionCtxDotPrefixFail(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("outcome", "fail")
	result, err := EvaluateCondition("ctx.outcome = success", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected 'ctx.outcome = success' to be false when outcome=fail")
	}
}

func TestConditionCtxDotPrefixContains(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("tool_stdout", "all-done")
	result, err := EvaluateCondition("ctx.tool_stdout contains all-done", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected 'ctx.tool_stdout contains all-done' to match")
	}
}

func TestConditionCtxDotNotContains(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("tool_stdout", "milestone-1")
	result, err := EvaluateCondition("ctx.tool_stdout not contains all-done", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected 'ctx.tool_stdout not contains all-done' to be true when stdout=milestone-1")
	}
}

func TestConditionCtxDotNotContainsFalse(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("tool_stdout", "all-done")
	result, err := EvaluateCondition("ctx.tool_stdout not contains all-done", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected 'ctx.tool_stdout not contains all-done' to be false when stdout=all-done")
	}
}

func TestConditionNotStartswith(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("status", "error: something")
	result, err := EvaluateCondition("status not startswith success", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected negated startswith to be true")
	}
}

func TestConditionNotIn(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("env", "test")
	result, err := EvaluateCondition("env not in dev,staging,prod", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected 'test' not in 'dev,staging,prod' to be true")
	}
}

func TestConditionANDORPrecedenceAllFalse(t *testing.T) {
	// When both OR branches are false
	ctx := NewPipelineContext()
	ctx.Set("a", "1")
	ctx.Set("b", "wrong")
	ctx.Set("c", "wrong")
	result, err := EvaluateCondition("a=1 && b=2 || c=3", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected (a=1 && b=wrong) || c=wrong to be false")
	}
}
