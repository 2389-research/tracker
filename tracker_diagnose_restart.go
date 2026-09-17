// ABOUTME: Restart-budget-reset records surfaced by Diagnose (#643).
// ABOUTME: Informational: an enclosing loop advanced and re-armed nested budgets/latches.
package tracker

// RestartBudgetReset is one restart_budget_reset activity entry (#643): an
// enclosing loop header restarted and reset a nested target's per-target
// restart budget (and possibly its one-shot fallback latch). A reset is the
// engine working as designed, not a failure, so it raises no Suggestion; it is
// what lets an operator read "milestone loop advanced; TestMilestone budget
// reset" next to a later `max restarts exceeded` on that same target and know
// the exhaustion happened within ONE iteration.
type RestartBudgetReset struct {
	// NodeID is the nested restart target whose budget was reset.
	NodeID string `json:"node_id"`
	// ResetBy is the enclosing loop header whose restart triggered the reset.
	ResetBy string `json:"reset_by"`
	// PreviousCount is the per-target restart count before the reset (0 when
	// only the fallback latch was cleared).
	PreviousCount int `json:"previous_count"`
	// FallbackLatchCleared is true when the node's one-shot fallback latch was
	// re-armed by the same reset.
	FallbackLatchCleared bool `json:"fallback_latch_cleared,omitempty"`
}
