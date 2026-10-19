// ABOUTME: The CLI's native LLM client resolution: tolerate a missing client for a
// ABOUTME: headless global backend, or a graph whose agent nodes never need native.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// resolveLLMClient builds the LLM client, handling non-fatal failures for
// headless backends: a global claude-code/acp backend tolerates any client
// error, and a graph whose agent nodes never need the native backend (all
// auto / claude-code / acp) tolerates "no providers configured".
func resolveLLMClient(backend string, graph *pipeline.Graph, subgraphs map[string]*pipeline.Graph) (*llm.Client, error) {
	llmClient, err := buildLLMClient()
	if err == nil {
		return llmClient, nil
	}
	headless := backend == "claude-code" || backend == "acp"
	noNative := errors.Is(err, llm.ErrNoProviders) && !pipeline.ComputeBackendNeeds(graph, subgraphs, backend).NeedsNativeClient()
	if !headless && !noNative {
		return nil, formatLLMClientError(err)
	}
	if headless {
		fmt.Fprintf(os.Stderr, "note: no native LLM client (%v) — using %s for all LLM calls\n", err, backend)
	}
	return nil, nil
}
