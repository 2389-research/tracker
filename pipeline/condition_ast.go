// ABOUTME: Single parsed model for edge/gate conditions — one OR/AND/clause split
// ABOUTME: and one paren policy shared by runtime eval, validation, and var analysis.
package pipeline

import (
	"errors"
	"fmt"
	"strings"
)

// ErrParenthesizedCondition is returned by ParseCondition when a condition
// contains a parenthesis outside a quoted span. The tracker condition dialect
// has no grouping parentheses — || is the lowest precedence, && is higher, and
// splitting happens on those bare tokens. A parenthesized expression such as
// `a=1 || (b=2 && c=3)` would split into garbage tokens (`(b`, `c)`) and
// silently mis-route at runtime, so it is rejected loudly and consistently at
// parse time regardless of whether the text came from an author-written Raw
// string or a formatted Parsed IR tree. Authors should use a flat form.
var ErrParenthesizedCondition = errors.New("parentheses are not supported in conditions (|| is lowest precedence, && higher; no grouping)")

// CompiledCondition is the single parsed form of a tracker edge / gate
// condition. It captures the dialect's fixed precedence structure exactly once
// — an OR of AND-groups of clauses — so runtime evaluation, syntax validation,
// and variable-reference extraction all walk the same tree instead of each
// re-splitting the raw text with its own copy of the precedence rules.
//
// Raw retains the original (trimmed) text for diagnostics / audit. An empty
// condition compiles to a tree with no branches, which every consumer treats as
// always-true (matching EvaluateCondition("") == true).
type CompiledCondition struct {
	Raw      string
	Branches []ConditionBranch // OR-joined; empty means always true
}

// ConditionBranch is one ||-separated branch: an AND of clause texts. Clause
// texts are trimmed and retain any `not ` prefix and operator — per-clause
// policy (operator discovery, operand emptiness, coercion) stays with each
// consumer so they keep their existing strictness while sharing one precedence
// model.
type ConditionBranch struct {
	Clauses []string // &&-separated clause texts, trimmed
}

// ParseCondition splits expr into the fixed OR/AND/clause structure exactly
// once, using the shared quote-aware scanner. It rejects the two structural
// errors that would otherwise surface as silent mis-routing at runtime —
// unmatched double quotes and parentheses — and leaves per-clause validation to
// the caller. Empty / whitespace-only input yields a branch-less tree.
func ParseCondition(expr string) (*CompiledCondition, error) {
	trimmed := strings.TrimSpace(expr)
	cc := &CompiledCondition{Raw: trimmed}
	if trimmed == "" {
		return cc, nil
	}
	if err := rejectGroupingParens(trimmed); err != nil {
		return nil, err
	}
	branches, err := splitConditionBranches(trimmed)
	if err != nil {
		return nil, err
	}
	cc.Branches = branches
	return cc, nil
}

// rejectGroupingParens fails when a parenthesis appears outside a quoted span.
// scanOutsideDoubleQuotes also validates that quotes are matched.
func rejectGroupingParens(trimmed string) error {
	outside, err := scanOutsideDoubleQuotes(trimmed)
	if err != nil {
		return err
	}
	for i := 0; i < len(trimmed); i++ {
		if outside[i] && (trimmed[i] == '(' || trimmed[i] == ')') {
			return fmt.Errorf("%w: %q", ErrParenthesizedCondition, trimmed)
		}
	}
	return nil
}

// splitConditionBranches splits on || (branches) then && (clauses), trimming
// each clause. Both splits are quote-aware via the shared scanner.
func splitConditionBranches(trimmed string) ([]ConditionBranch, error) {
	branchTexts, err := splitOutsideQuotes(trimmed, "||")
	if err != nil {
		return nil, err
	}
	branches := make([]ConditionBranch, 0, len(branchTexts))
	for _, bt := range branchTexts {
		clauseTexts, err := splitOutsideQuotes(strings.TrimSpace(bt), "&&")
		if err != nil {
			return nil, err
		}
		clauses := make([]string, len(clauseTexts))
		for i, ct := range clauseTexts {
			clauses[i] = strings.TrimSpace(ct)
		}
		branches = append(branches, ConditionBranch{Clauses: clauses})
	}
	return branches, nil
}

// evaluate walks the OR of branches (short-circuit): true if any branch is true.
func (cc *CompiledCondition) evaluate(ctx *PipelineContext) (bool, error) {
	for _, branch := range cc.Branches {
		result, err := branch.evaluate(ctx)
		if err != nil {
			return false, err
		}
		if result {
			return true, nil
		}
	}
	return false, nil
}

// evaluate walks the AND of clauses (short-circuit): true only if all are true.
func (b ConditionBranch) evaluate(ctx *PipelineContext) (bool, error) {
	for _, clause := range b.Clauses {
		result, err := evaluateClause(clause, ctx)
		if err != nil {
			return false, err
		}
		if !result {
			return false, nil
		}
	}
	return true, nil
}
