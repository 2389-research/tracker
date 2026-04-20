// ABOUTME: Additional Dippin semantic lint rules (DIP103, DIP105, DIP106, DIP109).
// ABOUTME: Split from lint_dippin.go to keep files under 500 lines.
package pipeline

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// lintDIP105 checks for no guaranteed success path from start to exit.
func lintDIP105(g *Graph) []string {
	if g.StartNode == "" || g.ExitNode == "" {
		return nil
	}
	if hasUnconditionalPath(g, g.StartNode, g.ExitNode) {
		return nil
	}
	return []string{"warning[DIP105]: no guaranteed success path from start to exit (all paths require conditions)"}
}

// hasUnconditionalPath returns true if there is a path from start to goal
// using only unconditional (no condition) edges.
func hasUnconditionalPath(g *Graph, start, goal string) bool {
	visited := bfsVisit(start, func(node string) []string {
		return unconditionalNeighbors(g, node)
	})
	return visited[goal]
}

// unconditionalNeighbors returns the targets of unconditional outgoing edges from node.
func unconditionalNeighbors(g *Graph, node string) []string {
	var out []string
	for _, edge := range g.OutgoingEdges(node) {
		if edge.Condition == "" {
			out = append(out, edge.To)
		}
	}
	return out
}

// lintDIP106 checks for undefined variable references in prompts.
func lintDIP106(g *Graph) []string {
	allWrites := collectAllWrites(g)
	reservedKeys := reservedContextKeys()

	var warnings []string
	for _, node := range g.Nodes {
		warnings = append(warnings, lintDIP106Node(node, allWrites, reservedKeys)...)
	}
	return warnings
}

// lintDIP106Node checks a single node's prompt for undefined ctx variable references.
func lintDIP106Node(node *Node, allWrites map[string]bool, reservedKeys map[string]bool) []string {
	prompt := node.Attrs["prompt"]
	if prompt == "" {
		return nil
	}
	var warnings []string
	for _, ref := range findVariableReferences(prompt) {
		parts := strings.SplitN(ref, ".", 2)
		if len(parts) != 2 || parts[0] != "ctx" {
			continue
		}
		key := parts[1]
		if !reservedKeys[key] && !allWrites[key] {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP106]: node %q prompt references undefined variable ${ctx.%s}", node.ID, key))
		}
	}
	return warnings
}

// findVariableReferences extracts ${...} patterns from a string.
func findVariableReferences(s string) []string {
	var refs []string
	for {
		start := strings.Index(s, "${")
		if start == -1 {
			break
		}
		end := strings.Index(s[start:], "}")
		if end == -1 {
			break
		}
		ref := s[start+2 : start+end]
		refs = append(refs, ref)
		s = s[start+end+1:]
	}
	return refs
}

// lintDIP103 checks for overlapping conditions on edges from the same node.
func lintDIP103(g *Graph) []string {
	var warnings []string

	outgoing := make(map[string][]*Edge)
	for _, edge := range g.Edges {
		outgoing[edge.From] = append(outgoing[edge.From], edge)
	}

	for nodeID, edges := range outgoing {
		if len(edges) < 2 {
			continue
		}
		warnings = append(warnings, findDuplicateConditions(nodeID, edges)...)
	}

	return warnings
}

// findDuplicateConditions returns warnings for any condition used on more than one outgoing edge.
func findDuplicateConditions(nodeID string, edges []*Edge) []string {
	conditions := make(map[string]int)
	for _, edge := range edges {
		if edge.Condition != "" {
			conditions[edge.Condition]++
		}
	}
	var warnings []string
	for cond, count := range conditions {
		if count > 1 {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP103]: node %q has %d edges with identical condition %q",
				nodeID, count, cond))
		}
	}
	return warnings
}

// lintDIP109 checks for namespace collisions in subgraph params.
func lintDIP109(g *Graph) []string {
	var warnings []string

	reservedKeys := reservedContextKeys()

	for _, node := range g.Nodes {
		if node.Handler != "subgraph" && node.Handler != "spawn" {
			continue
		}
		warnings = append(warnings, checkSubgraphParamCollisions(node, reservedKeys)...)
	}

	return warnings
}

// checkSubgraphParamCollisions returns warnings for any params key that collides with reserved keys.
func checkSubgraphParamCollisions(node *Node, reservedKeys map[string]bool) []string {
	params := node.Attrs["params"]
	if params == "" {
		return nil
	}
	var warnings []string
	for _, pair := range strings.Split(params, ",") {
		kv := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(kv) < 1 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		if reservedKeys[key] {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP109]: node %q params key %q collides with reserved context key",
				node.ID, key))
		}
	}
	return warnings
}

// reservedContextKeys returns the set of context keys that are always available.
func reservedContextKeys() map[string]bool {
	return map[string]bool{
		"goal": true, "outcome": true, "last_response": true,
		"last_cost": true, "last_turns": true, "human_response": true,
		"turn_limit_msg": true,
	}
}

// collectAllWrites gathers all context keys written by any node.
func collectAllWrites(g *Graph) map[string]bool {
	allWrites := make(map[string]bool)
	for _, node := range g.Nodes {
		for key := range parseWriteKeys(node.Attrs["writes"]) {
			allWrites[key] = true
		}
	}
	return allWrites
}

// lintDIP120 warns when ${params.<key>} is referenced but not declared at workflow level.
func lintDIP120(g *Graph) []string {
	declared := ExtractParamsFromGraphAttrs(g.Attrs)
	references := collectParamsReferences(g)

	var warnings []string
	for key := range references {
		if _, ok := declared[key]; ok {
			continue
		}
		warnings = append(warnings, fmt.Sprintf("warning[DIP120]: workflow references undeclared param ${params.%s}", key))
	}
	slices.Sort(warnings)
	return warnings
}

// lintDIP121 warns when a declared workflow param is never referenced.
func lintDIP121(g *Graph) []string {
	declared := ExtractParamsFromGraphAttrs(g.Attrs)
	references := collectParamsReferences(g)

	var warnings []string
	for _, key := range slices.Sorted(maps.Keys(declared)) {
		if references[key] {
			continue
		}
		warnings = append(warnings, fmt.Sprintf("warning[DIP121]: workflow param %q is declared but never referenced", key))
	}
	return warnings
}

func collectParamsReferences(g *Graph) map[string]bool {
	refs := make(map[string]bool)
	for _, node := range g.Nodes {
		collectParamsReferencesInText(node.Label, refs)
		for _, value := range node.Attrs {
			collectParamsReferencesInText(value, refs)
		}
	}
	for _, edge := range g.Edges {
		collectParamsReferencesInText(edge.Condition, refs)
	}
	return refs
}

func collectParamsReferencesInText(text string, refs map[string]bool) {
	for _, ref := range findVariableReferences(text) {
		namespace, key, ok := strings.Cut(ref, ".")
		if !ok || namespace != "params" || key == "" {
			continue
		}
		refs[key] = true
	}
}
