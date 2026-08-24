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

// recoverablePause maps a run error to a resumable OutcomePausedBilling pause
// when it is a recoverable operational halt, or returns (nil, false) otherwise.
// Two cases share the paused terminal (resume/checkpoint/TUI/docs machinery):
//
//   - Billing/quota EXHAUSTION (#487): an actionable, account-attributed message
//     (provider + env var + masked key + billing URL) is attached so the user can
//     top up the right account. Non-retryable regardless of wrapping — retrying
//     just re-hits the empty balance.
//   - A SUBSCRIPTION usage-limit (Claude Max/Team/Pro rolling cap: "usage limit
//     reached, resets at …", #590). It is NOT insufficient_quota/credit-balance,
//     so IsBillingError deliberately misses it; it carries the parsed reset time
//     (PauseError.ResumeAfter) so a scheduler can hold the resume until the
//     subscription resets rather than relaunching straight into the same cap.
func recoverablePause(nodeID string, runErr error) (*pipeline.PauseError, bool) {
	if help, isBilling := llm.BillingHelp(runErr); isBilling {
		return pipeline.NewPauseError(pipeline.OutcomePausedBilling,
			fmt.Errorf("node %q: %w\n%s", nodeID, runErr, help)), true
	}
	if llm.IsUsageLimit(runErr) {
		resetAt, _ := llm.UsageLimitResetAt(runErr)
		return pipeline.NewPauseErrorAt(pipeline.OutcomePausedBilling,
			fmt.Errorf("node %q: %w", nodeID, runErr), resetAt), true
	}
	return nil, false
}

// handleRunError processes session run errors, distinguishing fatal from retryable.
func (h *CodergenHandler) handleRunError(runErr error, node *pipeline.Node, prompt, artifactRoot string, sessResult agent.SessionResult, collector *transcriptCollector, priorEpisodes []string) (pipeline.Outcome, error) {
	var cfgErr *llm.ConfigurationError
	if errors.As(runErr, &cfgErr) {
		return pipeline.Outcome{}, fmt.Errorf("node %q: %w", node.ID, runErr)
	}

	// A RECOVERABLE operational halt — provider billing/quota exhaustion (#487) or
	// a subscription usage-limit (#590) — stops in the resumable OutcomePausedBilling
	// terminal (checkpoint + preserved work) instead of retrying or hard-failing.
	// Checked before the ProviderErrorInterface/backendFatalError fail paths and
	// the retry fallthrough so neither is diverted into a retry or a fatal.
	if paused, ok := recoverablePause(node.ID, runErr); ok {
		return pipeline.Outcome{}, paused
	}

	if pe, ok := runErr.(llm.ProviderErrorInterface); ok && !pe.Retryable() {
		return pipeline.Outcome{}, fmt.Errorf("node %q: %w", node.ID, runErr)
	}

	// #B: a backend (claude-code) error classified as a hard fail (auth, budget,
	// OOM/SIGKILL) must not be retried — checked after BillingHelp so a credit-
	// balance exhaustion still routes to the resumable pause above.
	var fatal *backendFatalError
	if errors.As(runErr, &fatal) {
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
