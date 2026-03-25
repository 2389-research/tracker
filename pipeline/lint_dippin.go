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
		fallbackTarget, hasFallback := node.Attrs["fallback_retry_target"]

		// If node has retry_target but no max_retries or fallback, it's unbounded
		if hasRetry && retryTarget != "" && (!hasMax || maxRetries == "" || maxRetries == "0") && (!hasFallback || fallbackTarget == "") {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP104]: node %q has unbounded retry loop (no max_retries or fallback)", node.ID))
		}
	}
	return warnings
}

// knownProviderModels maps provider names to recognized model patterns.
var knownProviderModels = map[string][]string{
	"openai":    {"gpt-4o", "gpt-4o-mini", "gpt-5.4", "o1", "o1-mini", "o3-mini"},
	"anthropic": {"claude-opus-4", "claude-sonnet-4", "claude-sonnet-4-5", "claude-haiku-4"},
	"gemini":    {"gemini-2.0-flash-exp", "gemini-2.5-flash", "gemini-2.5-pro"},
}

// lintDIP108 checks for unknown model/provider combinations.
func lintDIP108(g *Graph) []string {
	var warnings []string

	for _, node := range g.Nodes {
		if node.Handler != "codergen" {
			continue
		}

		model := resolveAttr(node.Attrs, g.Attrs, "llm_model", "model")
		provider := resolveAttr(node.Attrs, g.Attrs, "llm_provider", "provider")

		if model == "" || provider == "" {
			continue
		}

		if !isKnownModelProvider(provider, model) {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP108]: node %q has potentially unknown model/provider combination %q/%q",
				node.ID, model, provider))
		}
	}
	return warnings
}

// resolveAttr looks up an attribute by primary and fallback keys in node attrs, then graph attrs.
func resolveAttr(nodeAttrs, graphAttrs map[string]string, primaryKey, fallbackKey string) string {
	if v := nodeAttrs[primaryKey]; v != "" {
		return v
	}
	if v := nodeAttrs[fallbackKey]; v != "" {
		return v
	}
	return graphAttrs[primaryKey]
}

// isKnownModelProvider checks if the model matches any known pattern for the provider.
func isKnownModelProvider(provider, model string) bool {
	knownForProvider, ok := knownProviderModels[provider]
	if !ok {
		return true // unknown provider, don't warn
	}
	for _, m := range knownForProvider {
		if strings.Contains(model, m) || strings.Contains(m, model) {
			return true
		}
	}
	return false
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

	nodeWrites := collectNodeWrites(g)
	reservedKeys := reservedContextKeys()

	for _, node := range g.Nodes {
		reads := node.Attrs["reads"]
		if reads == "" {
			continue
		}

		upstreamKeys := collectUpstreamKeys(g, node.ID, nodeWrites)

		for _, key := range strings.Split(reads, ",") {
			key = strings.TrimSpace(key)
			if key == "" || reservedKeys[key] {
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

// collectNodeWrites builds a map of node ID -> set of context keys written.
func collectNodeWrites(g *Graph) map[string]map[string]bool {
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
	return nodeWrites
}

// collectUpstreamKeys performs BFS backwards from nodeID and collects all context
// keys written by upstream nodes.
func collectUpstreamKeys(g *Graph, nodeID string, nodeWrites map[string]map[string]bool) map[string]bool {
	upstream := make(map[string]bool)
	queue := []string{nodeID}
	visited := make(map[string]bool)
	visited[nodeID] = true

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

	upstreamKeys := make(map[string]bool)
	for upNode := range upstream {
		for key := range nodeWrites[upNode] {
			upstreamKeys[key] = true
		}
	}
	return upstreamKeys
}
