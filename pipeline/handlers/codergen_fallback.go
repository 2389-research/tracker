// ABOUTME: Parses the llm_fallbacks attr into ordered provider/model failover
// ABOUTME: targets for the codergen session (#486).
package handlers

import (
	"strings"

	"github.com/2389-research/tracker/llm"
)

// parseFallbackTargets parses a comma-separated "provider/model" list (the
// llm_fallbacks attr) into ordered failover targets. Malformed entries (no "/",
// or an empty side) are skipped; whitespace is trimmed. Returns nil for empty.
func parseFallbackTargets(raw string) []llm.Target {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []llm.Target
	for _, part := range strings.Split(raw, ",") {
		provider, model, ok := strings.Cut(part, "/")
		provider = strings.TrimSpace(provider)
		model = strings.TrimSpace(model)
		if !ok || provider == "" || model == "" {
			continue
		}
		out = append(out, llm.Target{Provider: provider, Model: model})
	}
	return out
}
