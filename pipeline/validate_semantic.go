// ABOUTME: Semantic validation for pipeline graphs beyond structural checks.
// ABOUTME: Verifies handler registration, condition syntax, and node attribute types.
package pipeline

import (
	"fmt"
	"strconv"
	"strings"
)

// ValidateSemantic checks a Graph for semantic correctness against a handler
// registry. It verifies that referenced handlers are registered, edge conditions
// parse correctly, and node attributes have valid types. It also runs Dippin
// lint rules and returns both errors and warnings.
func ValidateSemantic(g *Graph, registry *HandlerRegistry) (errors error, warnings []string) {
	if g == nil {
		return &ValidationError{Errors: []string{"graph is nil"}}, nil
	}
	ve := &ValidationError{}
	validateHandlerRegistration(g, registry, ve)
	validateConditionSyntax(g, ve)
	validateNodeAttributes(g, ve)

	// Run Dippin lint rules (warnings only)
	lintWarnings := LintDippinRules(g)

	if ve.hasErrors() {
		return ve, lintWarnings
	}
	return nil, lintWarnings
}

// validateHandlerRegistration checks that every node's handler is registered
// in the provided registry. Start and exit handlers are exempt because they
// are built-in to the engine and may not need explicit registration.
func validateHandlerRegistration(g *Graph, registry *HandlerRegistry, ve *ValidationError) {
	for _, node := range g.Nodes {
		if node.Handler == "" || node.Handler == "start" || node.Handler == "exit" {
			continue
		}
		if !registry.Has(node.Handler) {
			ve.add(fmt.Sprintf("node %q has unregistered handler %q", node.ID, node.Handler))
		}
	}
}

// validateConditionSyntax checks that every edge condition can be parsed
// without error. It uses EvaluateCondition with an empty context to surface
// syntax issues (missing operands, bad operators) without requiring runtime data.
func validateConditionSyntax(g *Graph, ve *ValidationError) {
	emptyCtx := NewPipelineContext()
	for _, edge := range g.Edges {
		if edge.Condition == "" {
			continue
		}
		if err := checkConditionSyntax(edge.Condition, emptyCtx); err != nil {
			ve.add(fmt.Sprintf("edge %s->%s has invalid condition %q: %s",
				edge.From, edge.To, edge.Condition, err))
		}
	}
}

// checkConditionSyntax validates condition syntax by checking for empty
// operands after splitting on logical operators, verifying that comparison
// operators have non-empty left-hand sides, and attempting evaluation with
// a recovery guard against panics.
func checkConditionSyntax(condition string, ctx *PipelineContext) error {
	// Check for empty branches after splitting on || and &&.
	orBranches := strings.Split(condition, "||")
	for _, branch := range orBranches {
		branch = strings.TrimSpace(branch)
		if branch == "" {
			return fmt.Errorf("empty operand in condition")
		}
		andClauses := strings.Split(branch, "&&")
		for _, clause := range andClauses {
			clause = strings.TrimSpace(clause)
			if clause == "" {
				return fmt.Errorf("empty operand in condition")
			}
			if err := checkClauseSyntax(clause); err != nil {
				return err
			}
		}
	}

	// Try evaluating the condition to catch malformed clauses.
	// Use a recover guard in case of unexpected panics.
	var evalErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				evalErr = fmt.Errorf("condition panicked: %v", r)
			}
		}()
		_, evalErr = EvaluateCondition(condition, ctx)
	}()

	return evalErr
}

// checkClauseSyntax validates that a single condition clause has non-empty
// operands around its operator. For example, "== broken" is invalid because
// it has an empty left-hand side.
func checkClauseSyntax(clause string) error {
	inner := clause
	if strings.HasPrefix(inner, "not ") {
		inner = strings.TrimSpace(inner[4:])
	}
	return checkClauseOperands(inner, clause)
}

// checkClauseOperands checks that the recognized operator in the clause has non-empty operands.
func checkClauseOperands(inner, original string) error {
	// Check word-based operators first.
	for _, op := range []string{" contains ", " startswith ", " endswith ", " in "} {
		if idx := strings.Index(inner, op); idx >= 0 {
			left := strings.TrimSpace(inner[:idx])
			right := strings.TrimSpace(inner[idx+len(op):])
			if left == "" || right == "" {
				return fmt.Errorf("empty operand around %q in clause %q", strings.TrimSpace(op), original)
			}
			return nil
		}
	}
	// Check != before = to avoid partial match.
	if idx := strings.Index(inner, "!="); idx >= 0 {
		left := strings.TrimSpace(inner[:idx])
		right := strings.TrimSpace(inner[idx+2:])
		if left == "" || right == "" {
			return fmt.Errorf("empty operand around '!=' in clause %q", original)
		}
		return nil
	}
	if idx := strings.Index(inner, "="); idx >= 0 {
		left := strings.TrimSpace(inner[:idx])
		right := strings.TrimSpace(inner[idx+1:])
		if left == "" || right == "" {
			return fmt.Errorf("empty operand around '=' in clause %q", original)
		}
		return nil
	}
	return nil
}

// validateNodeAttributes checks that well-known node and graph attributes have
// valid types.
func validateNodeAttributes(g *Graph, ve *ValidationError) {
	validateGraphAttrs(g.Attrs, ve)
	for _, node := range g.Nodes {
		validateSingleNodeAttrs(node, ve)
	}
}

// validateGraphAttrs validates graph-level attribute values.
func validateGraphAttrs(attrs map[string]string, ve *ValidationError) {
	validateBoolAttr(attrs, "cache_tool_results", "graph", "", ve)
	validateEnumAttr(attrs, "context_compaction", []string{"auto", "none"}, "graph", "", ve)
}

// validateSingleNodeAttrs validates a single node's attribute values.
func validateSingleNodeAttrs(node *Node, ve *ValidationError) {
	if mr, ok := node.Attrs["max_retries"]; ok {
		n, err := strconv.Atoi(mr)
		if err != nil || n < 0 {
			ve.add(fmt.Sprintf("node %q has invalid max_retries %q: must be a non-negative integer", node.ID, mr))
		}
	}
	validateBoolAttr(node.Attrs, "cache_tool_results", "node", node.ID, ve)
	validateEnumAttr(node.Attrs, "context_compaction", []string{"auto", "none"}, "node", node.ID, ve)
	if v, ok := node.Attrs["context_compaction_threshold"]; ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			ve.add(fmt.Sprintf("node %q has invalid context_compaction_threshold %q: must be a float", node.ID, v))
		} else if f <= 0 || f > 1.0 {
			ve.add(fmt.Sprintf("node %q has invalid context_compaction_threshold %q: must be > 0 and <= 1.0", node.ID, v))
		}
	}
}

// validateBoolAttr checks that an attribute value is "true" or "false".
func validateBoolAttr(attrs map[string]string, key, scope, nodeID string, ve *ValidationError) {
	v, ok := attrs[key]
	if !ok {
		return
	}
	if v != "true" && v != "false" {
		prefix := scope
		if nodeID != "" {
			prefix = fmt.Sprintf("node %q", nodeID)
		}
		ve.add(fmt.Sprintf("%s has invalid %s %q: must be \"true\" or \"false\"", prefix, key, v))
	}
}

// validateEnumAttr checks that an attribute value is one of the allowed values.
func validateEnumAttr(attrs map[string]string, key string, allowed []string, scope, nodeID string, ve *ValidationError) {
	v, ok := attrs[key]
	if !ok {
		return
	}
	for _, a := range allowed {
		if v == a {
			return
		}
	}
	prefix := scope
	if nodeID != "" {
		prefix = fmt.Sprintf("node %q", nodeID)
	}
	ve.add(fmt.Sprintf("%s has invalid %s %q: must be %q", prefix, key, v, strings.Join(allowed, "\" or \"")))
}
