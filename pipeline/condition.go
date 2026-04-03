// ABOUTME: Evaluates boolean expressions for edge condition gating.
// ABOUTME: Supports =, !=, ==, contains, startswith, endswith, in, not, &&, and || operators against pipeline context.

// Limitations:
//   - Operator splitting uses strings.Split on "||" and "&&" before clause parsing.
//     Values containing these literals will be misinterpreted even if quoted.
//   - No parentheses support for grouping. || is lowest precedence, && is higher.
//   - Both = and == are accepted for equality. Use = for consistency with .dip convention.
//   - Quote stripping (surrounding "") is applied only to =, ==, and != comparisons;
//     contains/startswith/endswith/in do not strip quotes.

package pipeline

import (
	"fmt"
	"log"
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

	// Try negated word-based operators first.
	if result, ok := tryNegatedWordOp(clause, ctx); ok {
		return result, nil
	}

	// Try word-based operators.
	if result, ok := tryWordOp(clause, ctx); ok {
		return result, nil
	}

	// Try != first since it contains = as a substring.
	if idx := strings.Index(clause, "!="); idx >= 0 {
		key := strings.TrimSpace(clause[:idx])
		expected := strings.Trim(strings.TrimSpace(clause[idx+2:]), `"`)
		actual := resolveAndWarnVar(key, ctx)
		return actual != expected, nil
	}

	// Check for == operator (space-delimited to avoid matching == inside values).
	if idx := strings.Index(clause, " == "); idx >= 0 {
		key := strings.TrimSpace(clause[:idx])
		expected := strings.Trim(strings.TrimSpace(clause[idx+4:]), `"`)
		actual := resolveAndWarnVar(key, ctx)
		return actual == expected, nil
	}

	if idx := strings.Index(clause, "="); idx >= 0 {
		key := strings.TrimSpace(clause[:idx])
		expected := strings.Trim(strings.TrimSpace(clause[idx+1:]), `"`)
		actual := resolveAndWarnVar(key, ctx)
		return actual == expected, nil
	}

	return false, fmt.Errorf("invalid condition clause: %q (expected key=value, key==value, key!=value, or word operator like contains/startswith/endswith/in)", clause)
}

// resolveAndWarnVar resolves a variable and logs a warning if not found.
func resolveAndWarnVar(name string, ctx *PipelineContext) string {
	val, found := resolveVariable(name, ctx)
	if !found {
		log.Printf("warning: unresolved condition variable %q (defaulting to empty string)", name)
	}
	return val
}

// tryNegatedWordOp checks for "key not contains/startswith/endswith/in value" operators.
// Returns (result, true) if matched, (false, false) if no match.
func tryNegatedWordOp(clause string, ctx *PipelineContext) (bool, bool) {
	for _, op := range []string{" not contains ", " not startswith ", " not endswith ", " not in "} {
		idx := strings.Index(clause, op)
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(clause[:idx])
		value := strings.TrimSpace(clause[idx+len(op):])
		actual := resolveAndWarnVar(key, ctx)
		positiveOp := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(op), "not "))
		result := evalWordOp(positiveOp, actual, value)
		return !result, true
	}
	return false, false
}

// tryWordOp checks for "key contains/startswith/endswith/in value" operators.
// Returns (result, true) if matched, (false, false) if no match.
func tryWordOp(clause string, ctx *PipelineContext) (bool, bool) {
	for _, op := range []string{" contains ", " startswith ", " endswith ", " in "} {
		idx := strings.Index(clause, op)
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(clause[:idx])
		value := strings.TrimSpace(clause[idx+len(op):])
		actual := resolveAndWarnVar(key, ctx)
		return evalWordOp(strings.TrimSpace(op), actual, value), true
	}
	return false, false
}

// evalWordOp evaluates a single word-based operator.
func evalWordOp(op, actual, value string) bool {
	switch op {
	case "contains":
		return strings.Contains(actual, value)
	case "startswith":
		return strings.HasPrefix(actual, value)
	case "endswith":
		return strings.HasSuffix(actual, value)
	case "in":
		for _, item := range strings.Split(value, ",") {
			if strings.TrimSpace(item) == actual {
				return true
			}
		}
		return false
	}
	return false
}

func resolveVariable(name string, ctx *PipelineContext) (string, bool) {
	if val, ok := ctx.Get(name); ok {
		return val, true
	}
	// Strip namespace prefixes: "ctx.outcome" → "outcome", "context.outcome" → "outcome"
	if strings.HasPrefix(name, "ctx.") {
		bare := strings.TrimPrefix(name, "ctx.")
		// Handle ctx.internal.* — delegate to the internal map.
		if strings.HasPrefix(bare, "internal.") {
			internalKey := strings.TrimPrefix(bare, "internal.")
			if val, ok := ctx.GetInternal(internalKey); ok {
				return val, true
			}
		}
		if val, ok := ctx.Get(bare); ok {
			return val, true
		}
	}
	if strings.HasPrefix(name, "context.") {
		bare := strings.TrimPrefix(name, "context.")
		if strings.HasPrefix(bare, "internal.") {
			internalKey := strings.TrimPrefix(bare, "internal.")
			if val, ok := ctx.GetInternal(internalKey); ok {
				return val, true
			}
		}
		if val, ok := ctx.Get(bare); ok {
			return val, true
		}
	}
	// Handle bare internal.* references.
	if strings.HasPrefix(name, "internal.") {
		internalKey := strings.TrimPrefix(name, "internal.")
		if val, ok := ctx.GetInternal(internalKey); ok {
			return val, true
		}
	}
	return "", false
}
