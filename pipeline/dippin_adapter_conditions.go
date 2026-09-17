// ABOUTME: Adapter-side condition text resolution — dippin ir.Condition to tracker's dialect (#647).
// ABOUTME: Parsed AST is authoritative; Raw is parsed with dippin's parser, then used verbatim only as a last resort.
package pipeline

import (
	"fmt"
	"strings"

	"github.com/2389-research/dippin-lang/ir"
	"github.com/2389-research/dippin-lang/simulate"
)

// dippinConditionText renders a dippin ir.Condition in tracker's dialect for
// the engine. Precedence (#647):
//
//  1. Condition.Parsed — dippin's structured AST is authoritative. Its parser
//     already resolved `and` / `or` / `not` precedence, so serializing the tree
//     (SerializeDippinCondition) cannot drift from `dippin simulate`.
//  2. Condition.Raw parsed on the fly with dippin's own parser
//     (simulate.ParseCondition) — covers callers that hand FromDippinIR an
//     ir.Workflow without running validator.Validate (which is what populates
//     Parsed on the normal .dip load path).
//  3. Condition.Raw verbatim — only when dippin's parser rejects the text.
//     Tracker's dialect is a superset (`&&`, `||`, `matches`, numeric compares)
//     that a hand-built graph may use; the text still goes through
//     ParseCondition, which now also honors the word forms, before it is
//     accepted.
//
// Returns "" for nil / empty. what names the field for error messages.
func dippinConditionText(c *ir.Condition, what string) (string, error) {
	if c == nil {
		return "", nil
	}
	expr := c.Parsed
	if expr == nil && strings.TrimSpace(c.Raw) != "" {
		if parsed, err := simulate.ParseCondition(c.Raw); err == nil {
			expr = parsed
		}
	}
	if expr == nil {
		return strings.TrimSpace(c.Raw), nil
	}
	text, err := SerializeDippinCondition(expr)
	if err != nil {
		return "", fmt.Errorf("%s %q: %w", what, c.Raw, err)
	}
	return text, nil
}

// setConditionAttr stores the serialized condition under key, omitting the
// attr for a nil / empty condition so the handler applies its own default.
func setConditionAttr(attrs map[string]string, key string, c *ir.Condition) error {
	text, err := dippinConditionText(c, key)
	if err != nil {
		return err
	}
	if text != "" {
		attrs[key] = text
	}
	return nil
}

// managerLoopConditionText is the string-only form of dippinConditionText
// retained for callers that cannot propagate an error; a serialization
// failure (DNF bound, unknown operator) yields "" — the adapter's own paths
// use dippinConditionText and surface the error.
func managerLoopConditionText(c *ir.Condition) string {
	text, err := dippinConditionText(c, "condition")
	if err != nil {
		return ""
	}
	return text
}

// checkNoBareWordConjunction fails when a serialized/raw condition still
// carries a bare `and` / `or` token inside a clause or an empty clause (a
// dangling conjunction). ParseCondition's splitter already consumes the word
// forms, so this is belt-and-suspenders on top of it: tracker can never route
// on a literal `x and y` string (#647).
func checkNoBareWordConjunction(cc *CompiledCondition) error {
	for _, branch := range cc.Branches {
		for _, clause := range branch.Clauses {
			if err := checkClauseConjunction(cc.Raw, clause); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkClauseConjunction(raw, clause string) error {
	if clause == "" {
		return fmt.Errorf("empty clause in condition %q (dangling `and`/`or`/`&&`/`||`?)", raw)
	}
	if clauseHasBareWordConjunction(clause) {
		return fmt.Errorf("condition %q: bare word conjunction inside clause %q would be routed on as a literal", raw, clause)
	}
	return nil
}

// clauseHasBareWordConjunction reports whether `and` / `or` stands alone
// outside double quotes anywhere in clause. An unmatched quote reports true so
// the caller fails loudly (ParseCondition already rejected it upstream).
func clauseHasBareWordConjunction(clause string) bool {
	outside, err := scanOutsideDoubleQuotes(clause)
	if err != nil {
		return true
	}
	for i := range clause {
		if outside[i] && (isBareWord(clause, i, "and") || isBareWord(clause, i, "or")) {
			return true
		}
	}
	return false
}
