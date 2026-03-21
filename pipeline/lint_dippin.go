// ABOUTME: Dippin semantic lint rules (DIP101-DIP112).
// ABOUTME: These are warnings that flag likely workflow design issues but don't block execution.
package pipeline

import (
	"fmt"
	"strings"
)

// LintDippinRules runs all Dippin semantic lint checks (DIP101-DIP112).
// Returns a list of warning messages. Warnings don't block execution but should be reviewed.
func LintDippinRules(g *Graph) []string {
	var warnings []string

	warnings = append(warnings, lintDIP110(g)...)
	warnings = append(warnings, lintDIP111(g)...)
	warnings = append(warnings, lintDIP102(g)...)
	warnings = append(warnings, lintDIP104(g)...)
	warnings = append(warnings, lintDIP108(g)...)
	warnings = append(warnings, lintDIP101(g)...)
	warnings = append(warnings, lintDIP107(g)...)
	warnings = append(warnings, lintDIP112(g)...)
	warnings = append(warnings, lintDIP105(g)...)
	warnings = append(warnings, lintDIP106(g)...)
	warnings = append(warnings, lintDIP103(g)...)
	warnings = append(warnings, lintDIP109(g)...)

	return warnings
}

// lintDIP110 checks for agent nodes with empty prompts.
func lintDIP110(g *Graph) []string {
	var warnings []string
	for _, node := range g.Nodes {
		// Only check agent nodes (handler=codergen)
		if node.Handler != "codergen" {
			continue
		}
		prompt := strings.TrimSpace(node.Attrs["prompt"])
		if prompt == "" {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP110]: empty prompt on agent node %q", node.ID))
		}
	}
	return warnings
}

// lintDIP111 checks for tool nodes without timeout.
func lintDIP111(g *Graph) []string {
	var warnings []string
	for _, node := range g.Nodes {
		// Only check tool nodes
		if node.Handler != "tool" {
			continue
		}
		// If node has a command but no timeout, warn
		if node.Attrs["tool_command"] != "" && node.Attrs["timeout"] == "" {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP111]: tool node %q has no timeout", node.ID))
		}
	}
	return warnings
}

// lintDIP102 checks for routing nodes with conditional edges but no default/unconditional edge.
func lintDIP102(g *Graph) []string {
	var warnings []string

	// Build adjacency map of outgoing edges per node
	outgoing := make(map[string][]*Edge)
	for _, edge := range g.Edges {
		outgoing[edge.From] = append(outgoing[edge.From], edge)
	}

	for nodeID, edges := range outgoing {
		if len(edges) == 0 {
			continue
		}

		hasConditional := false
		hasUnconditional := false
		for _, edge := range edges {
			if edge.Condition != "" {
				hasConditional = true
			} else {
				hasUnconditional = true
			}
		}

		// Warn if node has conditional edges but no unconditional fallback
		if hasConditional && !hasUnconditional {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP102]: node %q has conditional edges but no default/unconditional edge", nodeID))
		}
	}

	return warnings
}

// lintDIP104 checks for unbounded retry loops.
func lintDIP104(g *Graph) []string {
	var warnings []string
	for _, node := range g.Nodes {
		retryTarget, hasRetry := node.Attrs["retry_target"]
		maxRetries, hasMax := node.Attrs["max_retries"]
		fallbackTarget, hasFallback := node.Attrs["fallback_target"]

		// If node has retry_target but no max_retries or fallback, it's unbounded
		if hasRetry && retryTarget != "" && (!hasMax || maxRetries == "" || maxRetries == "0") && (!hasFallback || fallbackTarget == "") {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP104]: node %q has unbounded retry loop (no max_retries or fallback)", node.ID))
		}
	}
	return warnings
}

// lintDIP108 checks for unknown model/provider combinations.
func lintDIP108(g *Graph) []string {
	var warnings []string
	
	// Known good combinations (not exhaustive, just common ones)
	knownModels := map[string][]string{
		"openai":    {"gpt-4o", "gpt-4o-mini", "gpt-5.4", "o1", "o1-mini", "o3-mini"},
		"anthropic": {"claude-opus-4", "claude-sonnet-4", "claude-sonnet-4-5", "claude-haiku-4"},
		"google":    {"gemini-2.0-flash-exp", "gemini-2.5-flash", "gemini-2.5-pro"},
	}

	for _, node := range g.Nodes {
		if node.Handler != "codergen" {
			continue
		}

		model := node.Attrs["llm_model"]
		if model == "" {
			model = node.Attrs["model"]
		}
		if model == "" {
			model = g.Attrs["llm_model"]
		}

		provider := node.Attrs["llm_provider"]
		if provider == "" {
			provider = node.Attrs["provider"]
		}
		if provider == "" {
			provider = g.Attrs["llm_provider"]
		}

		// Skip if both are empty (will use defaults)
		if model == "" && provider == "" {
			continue
		}

		// Check if combination is known
		if provider != "" && model != "" {
			if knownForProvider, ok := knownModels[provider]; ok {
				found := false
				for _, m := range knownForProvider {
					if strings.Contains(model, m) || strings.Contains(m, model) {
						found = true
						break
					}
				}
				if !found {
					warnings = append(warnings, fmt.Sprintf(
						"warning[DIP108]: node %q has potentially unknown model/provider combination %q/%q",
						node.ID, model, provider))
				}
			}
		}
	}
	return warnings
}

// lintDIP101 checks for nodes only reachable via conditional edges.
func lintDIP101(g *Graph) []string {
	var warnings []string
	if g.StartNode == "" {
		return warnings
	}

	// Build unconditional edge map
	unconditional := make(map[string][]string)
	for _, edge := range g.Edges {
		if edge.Condition == "" {
			unconditional[edge.From] = append(unconditional[edge.From], edge.To)
		}
	}

	// BFS from start following only unconditional edges
	reachableUnconditional := make(map[string]bool)
	queue := []string{g.StartNode}
	reachableUnconditional[g.StartNode] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		for _, next := range unconditional[current] {
			if !reachableUnconditional[next] {
				reachableUnconditional[next] = true
				queue = append(queue, next)
			}
		}
	}

	// Check all nodes reachable via ANY edge
	allReachable := make(map[string]bool)
	queue = []string{g.StartNode}
	allReachable[g.StartNode] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		for _, edge := range g.OutgoingEdges(current) {
			if !allReachable[edge.To] {
				allReachable[edge.To] = true
				queue = append(queue, edge.To)
			}
		}
	}

	// Warn for nodes reachable overall but not via unconditional path
	for nodeID := range allReachable {
		if !reachableUnconditional[nodeID] && nodeID != g.StartNode {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP101]: node %q only reachable via conditional edges", nodeID))
		}
	}

	return warnings
}

// lintDIP107 checks for unused context writes.
func lintDIP107(g *Graph) []string {
	var warnings []string

	// Collect all writes and reads
	writes := make(map[string][]string) // key -> []nodeID
	reads := make(map[string][]string)  // key -> []nodeID

	for _, node := range g.Nodes {
		if w := node.Attrs["writes"]; w != "" {
			for _, key := range strings.Split(w, ",") {
				key = strings.TrimSpace(key)
				if key != "" {
					writes[key] = append(writes[key], node.ID)
				}
			}
		}
		if r := node.Attrs["reads"]; r != "" {
			for _, key := range strings.Split(r, ",") {
				key = strings.TrimSpace(key)
				if key != "" {
					reads[key] = append(reads[key], node.ID)
				}
			}
		}
	}

	// Warn for keys that are written but never read
	for key, writers := range writes {
		if _, isRead := reads[key]; !isRead {
			for _, nodeID := range writers {
				warnings = append(warnings, fmt.Sprintf(
					"warning[DIP107]: node %q writes unused context key %q", nodeID, key))
			}
		}
	}

	return warnings
}

// lintDIP112 checks for reads of keys not produced upstream.
func lintDIP112(g *Graph) []string {
	var warnings []string
	if g.StartNode == "" {
		return warnings
	}

	// Collect writes per node
	nodeWrites := make(map[string]map[string]bool)
	for _, node := range g.Nodes {
		if w := node.Attrs["writes"]; w != "" {
			nodeWrites[node.ID] = make(map[string]bool)
			for _, key := range strings.Split(w, ",") {
				key = strings.TrimSpace(key)
				if key != "" {
					nodeWrites[node.ID][key] = true
				}
			}
		}
	}

	// For each node, collect upstream writes via BFS
	for _, node := range g.Nodes {
		reads := node.Attrs["reads"]
		if reads == "" {
			continue
		}

		// BFS backwards to find all upstream nodes
		upstream := make(map[string]bool)
		queue := []string{node.ID}
		visited := make(map[string]bool)
		visited[node.ID] = true

		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]

			for _, edge := range g.Edges {
				if edge.To == current && !visited[edge.From] {
					visited[edge.From] = true
					upstream[edge.From] = true
					queue = append(queue, edge.From)
				}
			}
		}

		// Collect all keys produced upstream
		upstreamKeys := make(map[string]bool)
		for upstreamNode := range upstream {
			for key := range nodeWrites[upstreamNode] {
				upstreamKeys[key] = true
			}
		}

		// Check each read key
		for _, key := range strings.Split(reads, ",") {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}

			// Skip reserved context keys (these are always available)
			reservedKeys := map[string]bool{
				"goal": true, "outcome": true, "last_response": true,
				"last_cost": true, "last_turns": true, "human_response": true,
			}
			if reservedKeys[key] {
				continue
			}

			if !upstreamKeys[key] {
				warnings = append(warnings, fmt.Sprintf(
					"warning[DIP112]: node %q reads key %q not produced by upstream writes", node.ID, key))
			}
		}
	}

	return warnings
}

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

	// Collect all writes to determine available keys
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

	// Reserved context keys
	reservedKeys := map[string]bool{
		"goal": true, "outcome": true, "last_response": true,
		"last_cost": true, "last_turns": true, "human_response": true,
	}

	for _, node := range g.Nodes {
		prompt := node.Attrs["prompt"]
		if prompt == "" {
			continue
		}

		// Find all ${...} references
		refs := findVariableReferences(prompt)
		for _, ref := range refs {
			// Parse ${ctx.X}, ${params.Y}, ${graph.Z}
			parts := strings.SplitN(ref, ".", 2)
			if len(parts) != 2 {
				continue
			}

			namespace := parts[0]
			key := parts[1]

			// Check if key is defined
			if namespace == "ctx" {
				if !reservedKeys[key] && !allWrites[key] {
					warnings = append(warnings, fmt.Sprintf(
						"warning[DIP106]: node %q prompt references undefined variable ${ctx.%s}", node.ID, key))
				}
			}
			// params and graph namespaces are user-provided, skip checking
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

	// Group edges by source node
	outgoing := make(map[string][]*Edge)
	for _, edge := range g.Edges {
		outgoing[edge.From] = append(outgoing[edge.From], edge)
	}

	for nodeID, edges := range outgoing {
		if len(edges) < 2 {
			continue
		}

		// Check for identical conditions
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

	// Reserved context keys
	reservedKeys := map[string]bool{
		"goal": true, "outcome": true, "last_response": true,
		"last_cost": true, "last_turns": true, "human_response": true,
	}

	for _, node := range g.Nodes {
		if node.Handler != "subgraph" && node.Handler != "spawn" {
			continue
		}

		params := node.Attrs["params"]
		if params == "" {
			continue
		}

		// Parse params (format: key1=val1,key2=val2)
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
