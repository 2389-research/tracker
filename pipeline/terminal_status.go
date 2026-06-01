// ABOUTME: TerminalStatus named string type for EngineResult.Status taxonomy.
// ABOUTME: Carries IsSuccess() helper used by CLI exit-code, audit, and JSON consumers.
package pipeline

// TerminalStatus is the run-level terminal status carried on EngineResult.Status,
// tracker.Result.Status, tracker.AuditReport.Status, and tracker.RunSummary.Status.
//
// The known values are:
//
//   - OutcomeSuccess              "success"
//   - OutcomeFail                 "fail"
//   - OutcomeBudgetExceeded       "budget_exceeded"
//   - OutcomeValidationOverridden "validation_overridden"
//
// The enum is open — future minor releases may add new values. Consumers should
// use IsSuccess() to classify rather than switching on the raw string.
type TerminalStatus string

// IsSuccess reports whether the terminal status represents a run that completed
// without failure. Currently true for {success, validation_overridden}. Any
// unrecognized value returns false (fail-closed).
func (s TerminalStatus) IsSuccess() bool {
	switch s {
	case OutcomeSuccess, OutcomeValidationOverridden:
		return true
	default:
		return false
	}
}
