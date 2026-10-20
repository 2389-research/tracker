// ABOUTME: Maps a finished run's (result, error) to the CLI's command error:
// ABOUTME: waiting at a gate, --fail-on-override, a failure, or success.
package main

import (
	"fmt"
	"os"

	"github.com/2389-research/tracker/pipeline"
)

// interpretRunResult converts a raw engine run result into a pipeline-level
// error.
//
// Mapping:
//   - Status==paused_gate         -> errPausedAtGate (exit 4), even with runErr
//   - runErr != nil               -> wrapped engine error (exit 1)
//   - Status.IsSuccess() && !flag -> nil (exit 0); covers both success and
//     validation_overridden by default
//   - Status==validation_overridden && --fail-on-override -> ErrValidationOverridden
//     (exit 2 — distinct from generic fail)
//   - Any other status            -> generic "pipeline finished with status: X"
//     error (exit 1); failure dominates --fail-on-override.
//
// Note: runErr precedence comes first so a low-level engine crash is surfaced
// even on a paper-success status, and the override sentinel only fires on the
// no-runErr path.
func interpretRunResult(result *pipeline.EngineResult, runErr error, cfg *runConfig) error {
	// A run waiting at a gate also returns its PauseError; the status decides.
	if result != nil && result.Status == pipeline.OutcomePausedGate {
		return errPausedAtGate
	}
	if runErr != nil {
		return fmt.Errorf("pipeline execution: %w", runErr)
	}
	if result.Status == pipeline.OutcomeValidationOverridden && cfg != nil && cfg.failOnOverride {
		head := headlineOverride(result.ValidationOverrides)
		fmt.Fprintf(os.Stderr,
			"tracker: run completed via %s at %q (label %q); --fail-on-override caused non-zero exit\n",
			result.Status, head.GateNodeID, head.Label)
		return pipeline.ErrValidationOverridden
	}
	if !result.Status.IsSuccess() {
		return fmt.Errorf("pipeline finished with status: %s", result.Status)
	}
	return nil
}

// headlineOverride returns the latest entry from in (per spec D5a — the audit
// header picks the newest entry as the "headline" since it's the override that
// drove the run to its terminal exit). Returns a zero-value OverrideDetail for
// empty input so callers can format %q on the bare fields without nil checks.
func headlineOverride(in []pipeline.OverrideDetail) pipeline.OverrideDetail {
	if len(in) == 0 {
		return pipeline.OverrideDetail{}
	}
	return in[len(in)-1]
}
