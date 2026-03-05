// ABOUTME: Evaluates simple boolean expressions for edge condition gating.
// ABOUTME: Supports = (equals), != (not equals), and && (AND) operators against pipeline context.
package pipeline

import (
	"fmt"
	"strings"
)

// EvaluateCondition evaluates a condition expression against the pipeline context.
// Empty or whitespace-only conditions always return true.
// Supported syntax: "key=value", "key!=value", "expr1 && expr2".
func EvaluateCondition(expr string, ctx *PipelineContext) (bool, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return true, nil
	}

	clauses := strings.Split(expr, "&&")
	for _, clause := range clauses {
		result, err := evaluateClause(strings.TrimSpace(clause), ctx)
		if err != nil {
			return false, err
		}
		if !result {
			return false, nil
		}
	}
	return true, nil
}

func evaluateClause(clause string, ctx *PipelineContext) (bool, error) {
	// Try != first since it contains = as a substring.
	if idx := strings.Index(clause, "!="); idx >= 0 {
		key := strings.TrimSpace(clause[:idx])
		expected := strings.TrimSpace(clause[idx+2:])
		actual := resolveVariable(key, ctx)
		return actual != expected, nil
	}

	if idx := strings.Index(clause, "="); idx >= 0 {
		key := strings.TrimSpace(clause[:idx])
		expected := strings.TrimSpace(clause[idx+1:])
		actual := resolveVariable(key, ctx)
		return actual == expected, nil
	}

	return false, fmt.Errorf("invalid condition clause: %q (expected key=value or key!=value)", clause)
}

func resolveVariable(name string, ctx *PipelineContext) string {
	val, _ := ctx.Get(name)
	return val
}
