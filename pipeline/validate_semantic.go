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
// parse correctly, and node attributes have valid types.
func ValidateSemantic(g *Graph, registry *HandlerRegistry) error {
	if g == nil {
		return &ValidationError{Errors: []string{"graph is nil"}}
	}
	ve := &ValidationError{}
	validateHandlerRegistration(g, registry, ve)
	validateConditionSyntax(g, ve)
	validateNodeAttributes(g, ve)
	if ve.hasErrors() {
		return ve
	}
	return nil
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
	// Strip "not " prefix for negation.
	inner := clause
	if strings.HasPrefix(inner, "not ") {
		inner = strings.TrimSpace(inner[4:])
	}

	// Check word-based operators.
	for _, op := range []string{" contains ", " startswith ", " endswith ", " in "} {
		if idx := strings.Index(inner, op); idx >= 0 {
			left := strings.TrimSpace(inner[:idx])
			right := strings.TrimSpace(inner[idx+len(op):])
			if left == "" || right == "" {
				return fmt.Errorf("empty operand around %q in clause %q", strings.TrimSpace(op), clause)
			}
			return nil
		}
	}

	// Check != before =.
	if idx := strings.Index(inner, "!="); idx >= 0 {
		left := strings.TrimSpace(inner[:idx])
		right := strings.TrimSpace(inner[idx+2:])
		if left == "" || right == "" {
			return fmt.Errorf("empty operand around '!=' in clause %q", clause)
		}
		return nil
	}

	if idx := strings.Index(inner, "="); idx >= 0 {
		left := strings.TrimSpace(inner[:idx])
		right := strings.TrimSpace(inner[idx+1:])
		if left == "" || right == "" {
			return fmt.Errorf("empty operand around '=' in clause %q", clause)
		}
		return nil
	}

	// No recognized operator found — EvaluateCondition will report this.
	return nil
}

// validateNodeAttributes checks that well-known node and graph attributes have
// valid types.
func validateNodeAttributes(g *Graph, ve *ValidationError) {
	// Validate graph-level attributes.
	if v, ok := g.Attrs["cache_tool_results"]; ok {
		if v != "true" && v != "false" {
			ve.add(fmt.Sprintf("graph has invalid cache_tool_results %q: must be \"true\" or \"false\"", v))
		}
	}
	if v, ok := g.Attrs["context_compaction"]; ok {
		if v != "auto" && v != "none" {
			ve.add(fmt.Sprintf("graph has invalid context_compaction %q: must be \"auto\" or \"none\"", v))
		}
	}

	for _, node := range g.Nodes {
		if mr, ok := node.Attrs["max_retries"]; ok {
			n, err := strconv.Atoi(mr)
			if err != nil || n < 0 {
				ve.add(fmt.Sprintf("node %q has invalid max_retries %q: must be a non-negative integer", node.ID, mr))
			}
		}
		if v, ok := node.Attrs["cache_tool_results"]; ok {
			if v != "true" && v != "false" {
				ve.add(fmt.Sprintf("node %q has invalid cache_tool_results %q: must be \"true\" or \"false\"", node.ID, v))
			}
		}
		if v, ok := node.Attrs["context_compaction_threshold"]; ok {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				ve.add(fmt.Sprintf("node %q has invalid context_compaction_threshold %q: must be a float", node.ID, v))
			} else if f <= 0 || f > 1.0 {
				ve.add(fmt.Sprintf("node %q has invalid context_compaction_threshold %q: must be > 0 and <= 1.0", node.ID, v))
			}
		}
	}
}
