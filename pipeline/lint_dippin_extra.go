// ABOUTME: Additional Dippin semantic lint rules (DIP103, DIP105, DIP106, DIP109).
// ABOUTME: Split from lint_dippin.go to keep files under 500 lines.
package pipeline

import (
	"fmt"
	"strings"
)

// lintDIP105 checks for no guaranteed success path from start to exit.
func lintDIP105(g *Graph) []string {
	var warnings []string
	if g.StartNode == "" || g.ExitNode == "" {
		return warnings
	}

	// BFS from start to exit using only unconditional edges
	visited := make(map[string]bool)
	queue := []string{g.StartNode}
	visited[g.StartNode] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current == g.ExitNode {
			// Found a guaranteed path
			return warnings
		}

		for _, edge := range g.OutgoingEdges(current) {
			if edge.Condition == "" && !visited[edge.To] {
				visited[edge.To] = true
				queue = append(queue, edge.To)
			}
		}
	}

	warnings = append(warnings, "warning[DIP105]: no guaranteed success path from start to exit (all paths require conditions)")
	return warnings
}

// lintDIP106 checks for undefined variable references in prompts.
func lintDIP106(g *Graph) []string {
	var warnings []string

	allWrites := collectAllWrites(g)
	reservedKeys := reservedContextKeys()

	for _, node := range g.Nodes {
		prompt := node.Attrs["prompt"]
		if prompt == "" {
			continue
		}

		refs := findVariableReferences(prompt)
		for _, ref := range refs {
			parts := strings.SplitN(ref, ".", 2)
			if len(parts) != 2 {
				continue
			}
			if parts[0] == "ctx" {
				key := parts[1]
				if !reservedKeys[key] && !allWrites[key] {
					warnings = append(warnings, fmt.Sprintf(
						"warning[DIP106]: node %q prompt references undefined variable ${ctx.%s}", node.ID, key))
				}
			}
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

		conditions := make(map[string]int)
		for _, edge := range edges {
			if edge.Condition != "" {
				conditions[edge.Condition]++
			}
		}

		for cond, count := range conditions {
			if count > 1 {
				warnings = append(warnings, fmt.Sprintf(
					"warning[DIP103]: node %q has %d edges with identical condition %q",
					nodeID, count, cond))
			}
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

		params := node.Attrs["params"]
		if params == "" {
			continue
		}

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
	}

	return warnings
}

// reservedContextKeys returns the set of context keys that are always available.
func reservedContextKeys() map[string]bool {
	return map[string]bool{
		"goal": true, "outcome": true, "last_response": true,
		"last_cost": true, "last_turns": true, "human_response": true,
	}
}

// collectAllWrites gathers all context keys written by any node.
func collectAllWrites(g *Graph) map[string]bool {
	allWrites := make(map[string]bool)
	for _, node := range g.Nodes {
		if w := node.Attrs["writes"]; w != "" {
			for _, key := range strings.Split(w, ",") {
				key = strings.TrimSpace(key)
				if key != "" {
					allWrites[key] = true
				}
			}
		}
	}
	return allWrites
}
