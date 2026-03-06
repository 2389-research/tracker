// ABOUTME: Evaluates boolean expressions for edge condition gating.
// ABOUTME: Supports =, !=, contains, startswith, endswith, in, not, &&, and || operators against pipeline context.
package pipeline

import (
	"fmt"
	"strings"
)

// EvaluateCondition evaluates a condition expression against the pipeline context.
// Empty or whitespace-only conditions always return true.
// Parsing priority: || (lowest) then && (higher) then individual clauses.
func EvaluateCondition(expr string, ctx *PipelineContext) (bool, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return true, nil
	}
	if ctx == nil {
		return false, fmt.Errorf("cannot evaluate condition %q: nil context", expr)
	}

	return evaluateOr(expr, ctx)
}

// evaluateOr splits on "||" and returns true if any branch is true (short-circuit).
func evaluateOr(expr string, ctx *PipelineContext) (bool, error) {
	branches := strings.Split(expr, "||")
	for _, branch := range branches {
		result, err := evaluateAnd(strings.TrimSpace(branch), ctx)
		if err != nil {
			return false, err
		}
		if result {
			return true, nil
		}
	}
	return false, nil
}

// evaluateAnd splits on "&&" and returns true only if all clauses are true (short-circuit).
func evaluateAnd(expr string, ctx *PipelineContext) (bool, error) {
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
	// Handle "not" prefix negation.
	if strings.HasPrefix(clause, "not ") {
		inner := strings.TrimSpace(clause[4:])
		result, err := evaluateClause(inner, ctx)
		if err != nil {
			return false, err
		}
		return !result, nil
	}

	// Try word-based operators first (contains, startswith, endswith, in).
	// These are checked before = and != to avoid false matches on the = character.
	for _, op := range []string{" contains ", " startswith ", " endswith ", " in "} {
		if idx := strings.Index(clause, op); idx >= 0 {
			key := strings.TrimSpace(clause[:idx])
			value := strings.TrimSpace(clause[idx+len(op):])
			actual := resolveVariable(key, ctx)
			switch strings.TrimSpace(op) {
			case "contains":
				return strings.Contains(actual, value), nil
			case "startswith":
				return strings.HasPrefix(actual, value), nil
			case "endswith":
				return strings.HasSuffix(actual, value), nil
			case "in":
				items := strings.Split(value, ",")
				for _, item := range items {
					if strings.TrimSpace(item) == actual {
						return true, nil
					}
				}
				return false, nil
			}
		}
	}

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
	if val, ok := ctx.Get(name); ok {
		return val
	}
	if strings.HasPrefix(name, "context.") {
		trimmed := strings.TrimPrefix(name, "context.")
		if val, ok := ctx.Get(trimmed); ok {
			return val
		}
	}
	return ""
}
