// ABOUTME: Native LLM client resolution for an engine: tolerate a missing client only
// ABOUTME: when no node needs one ("no providers configured" with an auto/claude-code/acp graph).
package tracker

import (
	"errors"
	"fmt"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// resolveCompleter returns the LLM client and completer, building a client from
// env if needed. A missing client is tolerated when the global backend is
// claude-code or acp (out-of-process backends, any client error), or when no
// node in the graph needs the native backend and the only problem is that no
// provider is configured. A native node without a client still fails later in
// ensureNativeBackend with actionable guidance.
func resolveCompleter(cfg Config, graph *pipeline.Graph) (*llm.Client, agent.Completer, error) {
	if err := validateConfigBackend(cfg.Backend); err != nil {
		return nil, nil, err
	}
	if cfg.LLMClient != nil {
		return nil, cfg.LLMClient, nil
	}
	client, err := buildClient(cfg.Provider, cfg.GatewayURL, cfg.GatewayKind)
	if err != nil {
		if toleratesMissingClient(cfg, graph, err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("create LLM client: %w", err)
	}
	return client, client, nil
}

// toleratesMissingClient reports whether a client-construction error can be
// deferred to the node that would need the client.
func toleratesMissingClient(cfg Config, graph *pipeline.Graph, err error) bool {
	if cfg.Backend == "claude-code" || cfg.Backend == "acp" {
		return true
	}
	if !errors.Is(err, llm.ErrNoProviders) {
		return false
	}
	return !pipeline.ComputeBackendNeeds(graph, cfg.Subgraphs, cfg.Backend).NeedsNativeClient()
}

// validateConfigBackend rejects "auto" as a run-wide backend: it is a node
// value only (backend: auto in a .dip). Other values keep their existing
// handling.
func validateConfigBackend(backend string) error {
	if backend == "auto" {
		return fmt.Errorf("config backend %q: auto is a node value (backend: auto in a .dip), not a run-wide backend; use native, claude-code or acp", backend)
	}
	return nil
}
