// ABOUTME: Evaluates boolean expressions for edge condition gating.
// ABOUTME: Supports =, !=, ==, contains, startswith, endswith, in, not, &&, and || operators against pipeline context.

// Limitations:
//   - Logical splitting and operator discovery are double-quote-aware. Escaped
//     quotes do not close a value; unmatched double quotes return an error.
//   - No parentheses support for grouping. || is lowest precedence, && is higher.
//   - Both = and == are accepted for equality. Use = for consistency with .dip convention.
//   - One surrounding double-quote pair is removed uniformly from every RHS;
//     escaped quotes and backslashes are decoded inside that pair.

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

// scanOutsideDoubleQuotes marks bytes outside double-quoted spans. A quote is
// escaped only when preceded by an odd run of backslashes; even runs leave the
// quote free to close the span.
func scanOutsideDoubleQuotes(s string) ([]bool, error) {
	outside := make([]bool, len(s))
	inQuote := false
	backslashes := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			outside[i] = !inQuote
			backslashes++
			continue
		}
		if s[i] == '"' && backslashes%2 == 0 {
			inQuote = !inQuote
			backslashes = 0
			continue
		}
		outside[i] = !inQuote
		backslashes = 0
	}
	if inQuote {
		return nil, fmt.Errorf("unmatched double quote in condition: %s", s)
	}
	return outside, nil
}

// splitOutsideQuotes splits s on sep only where the delimiter is outside a
// double-quoted span.
func splitOutsideQuotes(s, sep string) ([]string, error) {
	outside, err := scanOutsideDoubleQuotes(s)
	if err != nil {
		return nil, err
	}
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if outside[i] && strings.HasPrefix(s[i:], sep) {
			parts = append(parts, s[start:i])
			i += len(sep) - 1
			start = i + 1
		}
	}
	return append(parts, s[start:]), nil
}

// evaluateOr splits on "||" and returns true if any branch is true (short-circuit).
func evaluateOr(expr string, ctx *PipelineContext) (bool, error) {
	branches, err := splitOutsideQuotes(expr, "||")
	if err != nil {
		return false, err
	}
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
	clauses, err := splitOutsideQuotes(expr, "&&")
	if err != nil {
		return false, err
	}
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

	return evaluateComparisonClause(clause, ctx)
}

type conditionOperator struct {
	raw      string
	word     string
	index    int
	negated  bool
	equality bool
}

var conditionOperators = []conditionOperator{
	{raw: " not contains ", word: "contains", negated: true},
	{raw: " not startswith ", word: "startswith", negated: true},
	{raw: " not endswith ", word: "endswith", negated: true},
	{raw: " not in ", word: "in", negated: true},
	{raw: " contains ", word: "contains"},
	{raw: " startswith ", word: "startswith"},
	{raw: " endswith ", word: "endswith"},
	{raw: " in ", word: "in"},
	{raw: "!=", equality: true},
	{raw: " == ", equality: true},
	{raw: "=", equality: true},
}

// findConditionOperator finds the first priority-ordered operator outside quotes.
func findConditionOperator(clause string) (conditionOperator, bool, error) {
	outside, err := scanOutsideDoubleQuotes(clause)
	if err != nil {
		return conditionOperator{}, false, err
	}
	for _, candidate := range conditionOperators {
		for i := 0; i+len(candidate.raw) <= len(clause); i++ {
			if outside[i] && strings.HasPrefix(clause[i:], candidate.raw) {
				candidate.index = i
				return candidate, true, nil
			}
		}
	}
	return conditionOperator{}, false, nil
}

func evaluateComparisonClause(clause string, ctx *PipelineContext) (bool, error) {
	op, ok, err := findConditionOperator(clause)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("invalid condition clause: %q (expected key=value, key==value, key!=value, or word operator like contains/startswith/endswith/in)", clause)
	}
	actual := resolveAndWarnVar(strings.TrimSpace(clause[:op.index]), ctx)
	expected := normalizeConditionOperand(clause[op.index+len(op.raw):])
	if !op.equality {
		result := evalWordOp(op.word, actual, expected)
		if op.negated {
			return !result, nil
		}
		return result, nil
	}
	switch op.raw {
	case "!=":
		return actual != expected, nil
	default:
		return actual == expected, nil
	}
}

// normalizeConditionOperand removes one surrounding quote pair and decodes
// dippin's supported double-quoted escapes in parser order.
func normalizeConditionOperand(raw string) string {
	value := strings.TrimSpace(raw)
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return value
	}
	value = value[1 : len(value)-1]
	value = strings.ReplaceAll(value, `\"`, `"`)
	return strings.ReplaceAll(value, `\\`, `\`)
}

// resolveAndWarnVar resolves a variable and logs a warning if not found.
func resolveAndWarnVar(name string, ctx *PipelineContext) string {
	val, found := resolveVariable(name, ctx)
	if !found {
		log.Printf("warning: unresolved condition variable %q (defaulting to empty string)", name)
	}
	return val
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
		return resolveCtxNamespace(strings.TrimPrefix(name, "ctx."), ctx)
	}
	if strings.HasPrefix(name, "context.") {
		return resolveCtxNamespace(strings.TrimPrefix(name, "context."), ctx)
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

// resolveCtxNamespace resolves a bare key (after stripping "ctx." or "context." prefix).
// Handles the "internal.*" sub-namespace and falls back to plain context lookup.
func resolveCtxNamespace(bare string, ctx *PipelineContext) (string, bool) {
	if strings.HasPrefix(bare, "internal.") {
		internalKey := strings.TrimPrefix(bare, "internal.")
		if val, ok := ctx.GetInternal(internalKey); ok {
			return val, true
		}
	}
	if val, ok := ctx.Get(bare); ok {
		return val, true
	}
	return "", false
}
