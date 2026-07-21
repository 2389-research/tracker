// ABOUTME: LLM-identity node-config resolution — model, provider, reasoning
// ABOUTME: effort, and failover lanes, each with graph-default then node-override.
package pipeline

// applyModelProvider resolves llm_model and llm_provider with graph defaults
// then node overrides.
func (n *Node) applyModelProvider(cfg *AgentNodeConfig, graphAttrs map[string]string) {
	if v, ok := graphAttrs["llm_model"]; ok {
		cfg.Model = v
	}
	if v, ok := n.Attrs["llm_model"]; ok {
		cfg.Model = v
	}
	if v, ok := graphAttrs["llm_provider"]; ok {
		cfg.Provider = v
	}
	if v, ok := n.Attrs["llm_provider"]; ok {
		cfg.Provider = v
	}
}

// applyReasoningEffort resolves reasoning_effort: graph then node; non-empty wins.
func (n *Node) applyReasoningEffort(cfg *AgentNodeConfig, graphAttrs map[string]string) {
	if v, ok := graphAttrs["reasoning_effort"]; ok && v != "" {
		cfg.ReasoningEffort = v
	}
	if v, ok := n.Attrs["reasoning_effort"]; ok && v != "" {
		cfg.ReasoningEffort = v
	}
}

// applyFallbacks resolves llm_fallbacks: graph default then node override; the
// raw comma-separated "provider/model" string is parsed by the codergen handler.
func (n *Node) applyFallbacks(cfg *AgentNodeConfig, graphAttrs map[string]string) {
	if v, ok := graphAttrs["llm_fallbacks"]; ok && v != "" {
		cfg.Fallbacks = v
	}
	if v, ok := n.Attrs["llm_fallbacks"]; ok && v != "" {
		cfg.Fallbacks = v
	}
}
