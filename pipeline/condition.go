// ABOUTME: Evaluates boolean expressions for edge condition gating.
// ABOUTME: Supports =, !=, ==, <, <=, >, >=, contains, startswith, endswith, in, matches (regex), not, &&, and || operators against pipeline context.

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
	"regexp"
	"strconv"
	"strings"

	"github.com/2389-research/tracker/internal/diag"
)

// EvaluateCondition evaluates a condition expression against the pipeline context.
// Empty or whitespace-only conditions always return true.
// Parsing priority: || (lowest) then && (higher) then individual clauses.
//
// Parsing and OR/AND splitting go through the single ParseCondition model so the
// runtime evaluator, syntax validation, and variable analysis share one
// precedence model and one paren policy (see condition_ast.go).
func EvaluateCondition(expr string, ctx *PipelineContext) (bool, error) {
	cc, err := ParseCondition(expr)
	if err != nil {
		return false, err
	}
	if len(cc.Branches) == 0 {
		return true, nil
	}
	if ctx == nil {
		return false, fmt.Errorf("cannot evaluate condition %q: nil context", cc.Raw)
	}
	return cc.evaluate(ctx)
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

// looksLikeUnspacedNumericTypo reports whether an equality clause is really a
// mistyped unspaced numeric comparison — `x>=5` matches the bare "=" inside
// ">=", leaving left text ending in ">" or "<" (#504). Numeric operators are
// spaced, so this is surfaced as a loud error rather than a silently-dead edge.
func looksLikeUnspacedNumericTypo(op conditionOperator, leftText string) bool {
	if !op.equality || op.raw != "=" {
		return false
	}
	return strings.HasSuffix(leftText, ">") || strings.HasSuffix(leftText, "<")
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
	{raw: " not matches ", word: "matches", negated: true},
	{raw: " contains ", word: "contains"},
	{raw: " startswith ", word: "startswith"},
	{raw: " endswith ", word: "endswith"},
	{raw: " in ", word: "in"},
	{raw: " matches ", word: "matches"},
	// Numeric comparisons (#504). Spaced like ==/contains — an unspaced ">"
	// would mis-parse an unquoted value containing ">". Ordered so ">="/"<="
	// win over ">"/"<", and all four before the equality group so the "=" in
	// ">="/"<=" isn't grabbed by the bare "=" operator.
	{raw: " >= ", word: "gte"},
	{raw: " <= ", word: "lte"},
	{raw: " > ", word: "gt"},
	{raw: " < ", word: "lt"},
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
	leftText := strings.TrimSpace(clause[:op.index])
	if looksLikeUnspacedNumericTypo(op, leftText) {
		return false, fmt.Errorf("invalid condition clause %q: numeric comparison operators (>, <, >=, <=) require surrounding spaces, e.g. `ctx.count >= 5`", clause)
	}
	actual := resolveAndWarnVar(leftText, ctx)
	expected := normalizeConditionOperand(clause[op.index+len(op.raw):])
	return applyOperator(op, actual, expected)
}

// applyOperator evaluates the resolved operands against op — word/numeric/regex
// operators via evalWordOp (honoring negation), or string equality for =/==/!=.
func applyOperator(op conditionOperator, actual, expected string) (bool, error) {
	if !op.equality {
		result, err := evalWordOp(op.word, actual, expected)
		if err != nil {
			return false, err
		}
		if op.negated {
			return !result, nil
		}
		return result, nil
	}
	if op.raw == "!=" {
		return actual != expected, nil
	}
	return actual == expected, nil
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
		diag.Warnf("warning: unresolved condition variable %q (defaulting to empty string)", name)
	}
	return val
}

// evalWordOp evaluates a single non-equality operator. It returns an error only
// for an author mistake in the right-hand operand — a malformed regex or a
// non-numeric numeric-comparison literal — so validate-time syntax checks catch
// those. A runtime data mismatch (e.g. a non-numeric context value on the left
// of a numeric comparison) warns and yields false, never an error.
func evalWordOp(op, actual, value string) (bool, error) {
	switch op {
	case "contains", "startswith", "endswith", "in":
		return evalStringOp(op, actual, value), nil
	case "matches":
		re, err := regexp.Compile(value)
		if err != nil {
			return false, fmt.Errorf("invalid regex %q in condition: %w", value, err)
		}
		return re.MatchString(actual), nil
	case "gt", "lt", "gte", "lte":
		return evalNumericOp(op, actual, value)
	}
	return false, nil
}

// evalStringOp evaluates the string-matching word operators (no error path).
func evalStringOp(op, actual, value string) bool {
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
	}
	return false
}

// evalNumericOp compares actual and value as float64. A non-numeric RHS literal
// is an author error (returns an error). A non-numeric LHS is runtime data —
// it warns and returns false, mirroring the unresolved-variable behavior, so a
// valid numeric condition still passes validation against an empty context.
func evalNumericOp(op, actual, value string) (bool, error) {
	want, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return false, fmt.Errorf("numeric comparison requires a numeric literal, got %q", value)
	}
	got, err := strconv.ParseFloat(strings.TrimSpace(actual), 64)
	if err != nil {
		diag.Warnf("warning: numeric comparison on non-numeric value %q (condition is false)", actual)
		return false, nil
	}
	switch op {
	case "gt":
		return got > want, nil
	case "lt":
		return got < want, nil
	case "gte":
		return got >= want, nil
	default: // "lte"
		return got <= want, nil
	}
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
