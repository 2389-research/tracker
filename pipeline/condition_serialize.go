// ABOUTME: Serializes a dippin ir.ConditionExpr AST into tracker's flat condition dialect (#647).
// ABOUTME: Lowers the tree to bounded DNF so word conjunctions, nested groups, and negation route correctly.
package pipeline

import (
	"errors"
	"fmt"
	"strings"

	"github.com/2389-research/dippin-lang/ir"
)

// MaxConditionDNFBranches bounds the disjunctive-normal-form expansion of a
// dippin condition AST. Tracker's dialect has no grouping parentheses, so a
// nested `a and (b or c)` must be distributed to `a && b || a && c`; the
// expansion is exponential in the worst case, so an AST that would exceed this
// many ||-branches is refused with ErrConditionTooComplex naming the edge
// rather than silently truncated.
const MaxConditionDNFBranches = 64

// ErrConditionTooComplex is returned when serializing a dippin condition AST
// would exceed MaxConditionDNFBranches.
var ErrConditionTooComplex = errors.New("condition too complex to flatten (exceeds DNF branch limit)")

// ErrUnsupportedConditionOp is returned when a dippin AST leaf carries an
// operator tracker cannot spell (or negate) in its own dialect.
var ErrUnsupportedConditionOp = errors.New("unsupported condition operator")

// SerializeDippinCondition renders a dippin ir.ConditionExpr in tracker's
// condition dialect — an `||` of `&&`-joined leaf clauses with no parentheses
// (see condition_ast.go). The AST is authoritative: dippin's parser already
// resolved `and` / `or` / `not` precedence, so emitting from the tree cannot
// drift from what `dippin simulate` evaluates (#647).
//
// Rules:
//   - CondAnd(a, b)  → `a && b`; CondOr(a, b) → `a || b`
//   - a CondOr nested under a CondAnd is distributed to DNF (bounded by
//     MaxConditionDNFBranches)
//   - CondNot over a leaf becomes the negated operator (`=`/`==`→`!=`, `!=`→`=`,
//     `contains`→`not contains`, …); the four numeric operators (tracker-only,
//     never produced by dippin's parser) keep tracker's `not ` clause prefix
//     instead, because `not ctx.n > 5` is true for a non-numeric/empty n while
//     `ctx.n <= 5` is false; CondNot over And/Or applies De Morgan
//   - leaf values are double-quoted (with `\"` / `\\` escapes) whenever a bare
//     spelling would be re-tokenized differently by tracker's parser, including
//     a `${...}` reference whose expansion could contain a bare `and` / `or`
//
// For every operator dippin's parser can produce (string comparison only), the
// emitted text evaluates under ParseCondition/EvaluateCondition with the same
// truth table as dippin's own evaluator. The nested-group (DNF / De Morgan)
// path is unreachable from `.dip` source — dippin's grammar has no
// parentheses — and exists for hand-built ASTs.
func SerializeDippinCondition(expr ir.ConditionExpr) (string, error) {
	if expr == nil {
		return "", nil
	}
	branches, err := conditionToDNF(expr, false)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(branches))
	for _, branch := range branches {
		clauses := make([]string, 0, len(branch))
		for _, lit := range branch {
			text, err := lit.text()
			if err != nil {
				return "", err
			}
			clauses = append(clauses, text)
		}
		parts = append(parts, strings.Join(clauses, " && "))
	}
	return strings.Join(parts, " || "), nil
}

// conditionLiteral is one DNF leaf: a comparison and whether it is negated.
type conditionLiteral struct {
	cmp     ir.CondCompare
	negated bool
}

// dnf is an OR of AND-groups of literals.
type dnf [][]conditionLiteral

// conditionToDNF lowers expr to disjunctive normal form, pushing negation down
// to the leaves (De Morgan) as it goes. negated flips the polarity of the
// subtree being visited.
func conditionToDNF(expr ir.ConditionExpr, negated bool) (dnf, error) {
	switch e := derefConditionExpr(expr).(type) {
	case ir.CondCompare:
		return dnf{{{cmp: e, negated: negated}}}, nil
	case ir.CondNot:
		return conditionToDNF(e.Inner, !negated)
	case ir.CondAnd:
		return dnfBinary(e.Left, e.Right, !negated, negated)
	case ir.CondOr:
		return dnfBinary(e.Left, e.Right, negated, negated)
	default:
		return nil, fmt.Errorf("unknown condition expression type %T", expr)
	}
}

// derefConditionExpr normalizes pointer-typed AST nodes (which hand-built
// trees may use) to the value types dippin's parser produces.
func derefConditionExpr(expr ir.ConditionExpr) ir.ConditionExpr {
	switch e := expr.(type) {
	case *ir.CondCompare:
		return *e
	case *ir.CondNot:
		return *e
	case *ir.CondAnd:
		return *e
	case *ir.CondOr:
		return *e
	}
	return expr
}

// dnfBinary lowers both operands and combines them. conjoin selects the
// product (an AND under positive polarity, or a negated OR) versus the union
// (an OR, or a negated AND — De Morgan).
func dnfBinary(left, right ir.ConditionExpr, conjoin, negated bool) (dnf, error) {
	l, err := conditionToDNF(left, negated)
	if err != nil {
		return nil, err
	}
	r, err := conditionToDNF(right, negated)
	if err != nil {
		return nil, err
	}
	if !conjoin {
		return boundDNF(append(l, r...))
	}
	if len(l)*len(r) > MaxConditionDNFBranches {
		return nil, fmt.Errorf("%w: %d branches > %d", ErrConditionTooComplex, len(l)*len(r), MaxConditionDNFBranches)
	}
	product := make(dnf, 0, len(l)*len(r))
	for _, a := range l {
		for _, b := range r {
			branch := make([]conditionLiteral, 0, len(a)+len(b))
			branch = append(branch, a...)
			branch = append(branch, b...)
			product = append(product, branch)
		}
	}
	return product, nil
}

func boundDNF(d dnf) (dnf, error) {
	if len(d) > MaxConditionDNFBranches {
		return nil, fmt.Errorf("%w: %d branches > %d", ErrConditionTooComplex, len(d), MaxConditionDNFBranches)
	}
	return d, nil
}

// negatedLeafOperators maps each dippin/tracker comparison operator to the
// exact operator text tracker's clause parser recognizes for its negation (see
// conditionOperators in condition.go). Every operator has a native negated
// spelling, so no `not ` clause prefix or parenthesis is ever needed.
var negatedLeafOperators = map[string]string{
	"=": "!=", "==": "!=", "!=": "=",
	"contains": "not contains", "startswith": "not startswith", "endswith": "not endswith",
	"in": "not in", "matches": "not matches",
}

// numericLeafOperators are tracker-only comparisons with no complementary
// operator: evalNumericOp yields false (not an error) on a non-numeric or
// empty left-hand value, so `not n > 5` and `n <= 5` differ for n="". Their
// negation is spelled with tracker's `not ` clause prefix.
var numericLeafOperators = map[string]bool{">": true, "<": true, ">=": true, "<=": true}

// leafOperatorText returns the operator text and clause prefix for op under
// the given polarity. A positive leaf keeps the author's spelling (`=` stays
// `=`, `==` stays `==`).
func leafOperatorText(op string, negated bool) (prefix, opText string, err error) {
	if numericLeafOperators[op] {
		if negated {
			return "not ", op, nil
		}
		return "", op, nil
	}
	neg, ok := negatedLeafOperators[op]
	if !ok {
		return "", "", fmt.Errorf("%w: %q", ErrUnsupportedConditionOp, op)
	}
	if negated {
		return "", neg, nil
	}
	return "", op, nil
}

func (l conditionLiteral) text() (string, error) {
	prefix, op, err := leafOperatorText(l.cmp.Op, l.negated)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(l.cmp.Variable) == "" {
		return "", fmt.Errorf("condition leaf has empty variable (op %q, value %q)", l.cmp.Op, l.cmp.Value)
	}
	return prefix + l.cmp.Variable + " " + op + " " + quoteConditionValue(l.cmp.Value), nil
}

// quoteConditionValue returns value spelled so tracker's clause parser reads
// it back verbatim: bare when it is a single safe token, otherwise wrapped in
// double quotes with `"` and `\` escaped (the inverse of
// normalizeConditionOperand). Bare `and` / `or` / `not` are quoted so the
// word-conjunction splitter never mistakes a literal for an operator, and so
// is any `${...}` reference: the engine expands it before evaluation, and an
// expanded value such as "rock and roll" would otherwise be word-split.
func quoteConditionValue(value string) string {
	if value == "" || needsConditionQuotes(value) {
		escaped := strings.ReplaceAll(value, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		return `"` + escaped + `"`
	}
	return value
}

func needsConditionQuotes(value string) bool {
	switch value {
	case "and", "or", "not":
		return true
	}
	return strings.Contains(value, "${") || strings.ContainsAny(value, " \t\r\n\"\\=!<>&|()")
}
