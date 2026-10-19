// ABOUTME: ComputeBackendNeeds: which backends a graph's agent nodes (subgraph children
// ABOUTME: included) need, so the engine can start without a native LLM client.
package pipeline

// BackendNeeds records which agent backends a run's nodes can use.
type BackendNeeds struct {
	Native, ClaudeCode, ACP, Auto bool
}

// NeedsNativeClient reports whether any agent node resolves to the native
// backend: an explicit native/codergen node, or a node with no backend while
// the global backend is unset or native. auto, claude-code and acp nodes never
// require one (auto falls back to claude-code or refuses at the node).
func (n BackendNeeds) NeedsNativeClient() bool { return n.Native }

// ComputeBackendNeeds walks g and every subgraph child.
func ComputeBackendNeeds(g *Graph, subgraphs map[string]*Graph, globalBackend string) BackendNeeds {
	var needs BackendNeeds
	addGraphNeeds(&needs, g, globalBackend)
	for _, sg := range subgraphs {
		addGraphNeeds(&needs, sg, globalBackend)
	}
	return needs
}

func addGraphNeeds(needs *BackendNeeds, g *Graph, globalBackend string) {
	if g == nil {
		return
	}
	for _, n := range g.Nodes {
		if n.Handler != "codergen" {
			continue
		}
		backend := n.AgentConfig(g.Attrs).Backend
		if backend == "" {
			backend = globalBackend
		}
		markBackend(needs, backend)
	}
}

func markBackend(needs *BackendNeeds, backend string) {
	switch backend {
	case "claude-code":
		needs.ClaudeCode = true
	case "acp":
		needs.ACP = true
	case "auto":
		needs.Auto = true
	default: // "", "native", "codergen"
		needs.Native = true
	}
}
