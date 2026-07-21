// ABOUTME: Session run-error classification for the native codergen backend —
// ABOUTME: distinguishes fatal (config, billing, non-retryable provider) from retryable.
package handlers

import (
	"errors"
	"fmt"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// handleRunError processes session run errors, distinguishing fatal from retryable.
func (h *CodergenHandler) handleRunError(runErr error, node *pipeline.Node, prompt, artifactRoot string, sessResult agent.SessionResult, collector *transcriptCollector, priorEpisodes []string) (pipeline.Outcome, error) {
	var cfgErr *llm.ConfigurationError
	if errors.As(runErr, &cfgErr) {
		return pipeline.Outcome{}, fmt.Errorf("node %q: %w", node.ID, runErr)
	}

	// Billing/quota exhaustion is non-retryable no matter how the error is typed
	// or wrapped — retrying just re-hits the empty balance. Hard-fail with an
	// actionable, account-attributed message (provider + env var + masked key +
	// billing URL) so the user knows exactly which account to top up (#487).
	if help, isBilling := llm.BillingHelp(runErr); isBilling {
		return pipeline.Outcome{}, fmt.Errorf("node %q: %w\n%s", node.ID, runErr, help)
	}

	if pe, ok := runErr.(llm.ProviderErrorInterface); ok && !pe.Retryable() {
		return pipeline.Outcome{}, fmt.Errorf("node %q: %w", node.ID, runErr)
	}

	outcome := pipeline.Outcome{
		Status: pipeline.OutcomeRetry,
		ContextUpdates: map[string]string{
			pipeline.ContextKeyLastResponse:             runErr.Error(),
			pipeline.ContextKeyResponsePrefix + node.ID: runErr.Error(),
			// #304: clear guard flags so a prior retry's state doesn't
			// persist into downstream conditional routing.
			pipeline.ContextKeyNodeCostExceeded: "",
			pipeline.ContextKeyNodeNoProgress:   "",
		},
		Stats: buildSessionStats(sessResult),
	}
	h.applyEpisodeContextUpdates(outcome.ContextUpdates, sessResult, priorEpisodes)
	responseArtifact := collector.transcript()
	if responseArtifact == "" {
		responseArtifact = runErr.Error()
	}
	responseArtifact += "\n\n" + sessResult.String()
	if err := pipeline.WriteStageArtifacts(artifactRoot, node.ID, prompt, responseArtifact, outcome); err != nil {
		return pipeline.Outcome{}, err
	}
	return outcome, nil
}
