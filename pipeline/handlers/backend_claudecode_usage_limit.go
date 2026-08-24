// ABOUTME: Detects a Claude subscription usage-limit in the claude-code subprocess
// ABOUTME: output and routes it to the resumable OutcomePausedBilling pause (#590).
package handlers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/internal/diag"
	"github.com/2389-research/tracker/llm"
)

// pausedForUsageLimit returns the recoverable usage-limit result (and ok=true)
// when the subprocess signalled a subscription cap, or ok=false otherwise. It is
// checked before the generic exit-code classification: a usage-limit is neither
// a retry (retry re-hits the cap) nor a hard fail, so it is surfaced as a PLAIN
// error (not a backendFatalError) that CodergenHandler.handleRunError routes to
// the resumable OutcomePausedBilling pause via llm.IsUsageLimit.
func pausedForUsageLimit(stderr string, state *runState, exitCode int) (agent.SessionResult, error, bool) {
	uerr := usageLimitError(stderr, state)
	if uerr == nil {
		return agent.SessionResult{}, nil, false
	}
	diag.Errorf("[claude-code] subscription usage limit reached — pausing for reset (exit %d)", exitCode)
	r, _ := buildResult(state)
	return r, uerr, true
}

// usageLimitError returns a non-nil error when the claude subprocess signalled a
// subscription usage-limit (Max/Team/Pro rolling cap) in either stderr or the
// stream-json result text, and nil otherwise. The returned error carries the
// combined signal text so the codergen handler can both classify it (via
// llm.IsUsageLimit) and parse the reset timestamp (via llm.UsageLimitResetAt).
func usageLimitError(stderr string, state *runState) error {
	combined := strings.TrimSpace(stderr)
	if state.resultText != "" {
		if combined != "" {
			combined += "\n"
		}
		combined += strings.TrimSpace(state.resultText)
	}
	if combined == "" || !llm.IsUsageLimit(errors.New(combined)) {
		return nil
	}
	return fmt.Errorf("claude CLI subscription usage limit reached: %s", combined)
}
