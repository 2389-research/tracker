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

	outgoing := make(map[string][]*Edge)
	for _, edge := range g.Edges {
		outgoing[edge.From] = append(outgoing[edge.From], edge)
	}

	for nodeID, edges := range outgoing {
		if len(edges) == 0 {
			continue
		}
		if hasConditionalWithoutFallback(edges) {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP102]: node %q has conditional edges but no default/unconditional edge", nodeID))
		}
	}

	return warnings
}

// hasConditionalWithoutFallback returns true when edges contain conditional edges but no unconditional one.
func hasConditionalWithoutFallback(edges []*Edge) bool {
	hasConditional := false
	hasUnconditional := false
	for _, edge := range edges {
		if edge.Condition != "" {
			hasConditional = true
		} else {
			hasUnconditional = true
		}
	}
	return hasConditional && !hasUnconditional
}

// lintDIP104 checks for unbounded retry loops.
func lintDIP104(g *Graph) []string {
	var warnings []string
	for _, node := range g.Nodes {
		if isUnboundedRetry(node.Attrs) {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP104]: node %q has unbounded retry loop (no max_retries or fallback)", node.ID))
		}
	}
	return warnings
}

// isUnboundedRetry returns true when a node has a retry_target but neither a
// meaningful max_retries count nor a fallback_retry_target to escape the loop.
func isUnboundedRetry(attrs map[string]string) bool {
	retryTarget, hasRetry := attrs["retry_target"]
	if !hasRetry || retryTarget == "" {
		return false
	}
	maxRetries := attrs["max_retries"]
	hasMaxRetries := maxRetries != "" && maxRetries != "0"
	hasFallback := attrs["fallback_retry_target"] != ""
	return !hasMaxRetries && !hasFallback
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
	if g.StartNode == "" {
		return nil
	}

	reachableUnconditional := bfsUnconditional(g)
	allReachable := bfsAllEdges(g)

	return warnConditionalOnlyNodes(allReachable, reachableUnconditional, g.StartNode)
}

// bfsUnconditional performs BFS from StartNode following only unconditional edges.
func bfsUnconditional(g *Graph) map[string]bool {
	adj := buildUnconditionalAdj(g)
	return bfsVisit(g.StartNode, func(node string) []string { return adj[node] })
}

// buildUnconditionalAdj builds an adjacency list containing only unconditional edges.
func buildUnconditionalAdj(g *Graph) map[string][]string {
	adj := make(map[string][]string)
	for _, edge := range g.Edges {
		if edge.Condition == "" {
			adj[edge.From] = append(adj[edge.From], edge.To)
		}
	}
	return adj
}

// bfsVisit performs BFS from start using the provided neighbor function.
// Returns the set of visited nodes (including start).
func bfsVisit(start string, neighbors func(string) []string) map[string]bool {
	visited := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range neighbors(current) {
			if !visited[next] {
				visited[next] = true
				queue = append(queue, next)
			}
		}
	}
	return visited
}

// bfsAllEdges performs BFS from StartNode following all edges.
func bfsAllEdges(g *Graph) map[string]bool {
	visited := make(map[string]bool)
	visited[g.StartNode] = true
	queue := []string{g.StartNode}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range g.OutgoingEdges(current) {
			if !visited[edge.To] {
				visited[edge.To] = true
				queue = append(queue, edge.To)
			}
		}
	}
	return visited
}

// warnConditionalOnlyNodes returns DIP101 warnings for nodes only reachable via conditional edges.
func warnConditionalOnlyNodes(allReachable, reachableUnconditional map[string]bool, startNode string) []string {
	var warnings []string
	for nodeID := range allReachable {
		if !reachableUnconditional[nodeID] && nodeID != startNode {
			warnings = append(warnings, fmt.Sprintf(
				"warning[DIP101]: node %q only reachable via conditional edges", nodeID))
		}
	}
	return warnings
}

// lintDIP107 checks for unused context writes.
func lintDIP107(g *Graph) []string {
	writes, reads := collectWritesAndReads(g)
	return warnUnusedWrites(writes, reads)
}

// collectWritesAndReads builds maps of context keys to their writing/reading nodes.
func collectWritesAndReads(g *Graph) (writes map[string][]string, reads map[string][]string) {
	writes = make(map[string][]string) // key -> []nodeID
	reads = make(map[string][]string)  // key -> []nodeID
	for _, node := range g.Nodes {
		for _, key := range splitTrimKeys(node.Attrs["writes"]) {
			writes[key] = append(writes[key], node.ID)
		}
		for _, key := range splitTrimKeys(node.Attrs["reads"]) {
			reads[key] = append(reads[key], node.ID)
		}
	}
	return writes, reads
}

// splitTrimKeys splits a comma-separated attribute value into trimmed, non-empty keys.
func splitTrimKeys(attr string) []string {
	if attr == "" {
		return nil
	}
	var keys []string
	for _, key := range strings.Split(attr, ",") {
		key = strings.TrimSpace(key)
		if key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

// warnUnusedWrites returns warnings for context keys that are written but never read.
func warnUnusedWrites(writes, reads map[string][]string) []string {
	var warnings []string
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
		warnings = append(warnings, checkNodeReadKeys(g, node, nodeWrites, reservedKeys)...)
	}

	return warnings
}

// checkNodeReadKeys validates that all keys declared in a node's "reads" attr are produced upstream.
func checkNodeReadKeys(g *Graph, node *Node, nodeWrites map[string]map[string]bool, reservedKeys map[string]bool) []string {
	reads := node.Attrs["reads"]
	if reads == "" {
		return nil
	}
	upstreamKeys := collectUpstreamKeys(g, node.ID, nodeWrites)
	var warnings []string
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
	return warnings
}

// collectNodeWrites builds a map of node ID -> set of context keys written.
func collectNodeWrites(g *Graph) map[string]map[string]bool {
	nodeWrites := make(map[string]map[string]bool)
	for _, node := range g.Nodes {
		if w := node.Attrs["writes"]; w != "" {
			nodeWrites[node.ID] = parseWriteKeys(w)
		}
	}
	return nodeWrites
}

// parseWriteKeys splits a comma-separated writes attribute into a set of trimmed keys.
func parseWriteKeys(w string) map[string]bool {
	keys := make(map[string]bool)
	for _, key := range strings.Split(w, ",") {
		key = strings.TrimSpace(key)
		if key != "" {
			keys[key] = true
		}
	}
	return keys
}

// collectUpstreamKeys performs BFS backwards from nodeID and collects all context
// keys written by upstream nodes.
func collectUpstreamKeys(g *Graph, nodeID string, nodeWrites map[string]map[string]bool) map[string]bool {
	upstream := bfsUpstreamNodes(g, nodeID)
	return mergeWriteKeys(upstream, nodeWrites)
}

// bfsUpstreamNodes performs reverse BFS from nodeID and returns the set of all
// predecessor node IDs (excluding nodeID itself).
func bfsUpstreamNodes(g *Graph, nodeID string) map[string]bool {
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
	return upstream
}

// mergeWriteKeys unions all context keys written by the given set of upstream nodes.
func mergeWriteKeys(upstream map[string]bool, nodeWrites map[string]map[string]bool) map[string]bool {
	keys := make(map[string]bool)
	for upNode := range upstream {
		for key := range nodeWrites[upNode] {
			keys[key] = true
		}
	}
	return keys
}
