// ABOUTME: Unified per-gate checkpoint state (#602) — phase enum, record, central transitions.
// ABOUTME: Replaces the four pre-#602 scattered maps and migrates old checkpoints one-way on load.
package pipeline

// GatePhase is the explicit lifecycle phase of a goal gate's redirect state
// (#602). It is single-valued by construction, so the pre-#602 contradictory
// combinations (a gate simultaneously "recheck-pending" and "overridden" in two
// independent maps) are unrepresentable: at most one phase is set, and a
// transition to one phase replaces any other. Override precedence over a pending
// recheck — the runtime rule in checkGoalGateNode — is preserved because
// MarkGateOverridden runs last on the covered-gate path and legacy migration
// applies overridden after pending.
type GatePhase string

const (
	// GatePhaseActive is the zero value: a live gate with no outstanding
	// redirect. A gate in this phase is judged by its LastOutcome.
	GatePhaseActive GatePhase = ""

	// GatePhaseRecheckPending marks a gate whose retry/fallback redirect has
	// fired but which has not re-executed since (#348 defect 1). While pending,
	// the gate stays visible to the exit-time goal-gate check even after
	// clearDownstream removed it from CompletedNodes, and a retry re-enters AT
	// the gate so it re-evaluates the current tree instead of replaying an
	// escalation tail that routes around it. Cleared when the gate re-executes.
	GatePhaseRecheckPending GatePhase = "recheck_pending"

	// GatePhaseOverridden marks a gate whose last (failed) outcome a human
	// resolved by traversing an override edge from the gate's escalation (#348
	// defect 2). An overridden gate is treated as satisfied by the exit-time
	// check and is not re-entered; the run completes validation_overridden.
	// Cleared when the gate re-executes so a fresh failure re-prompts the human.
	GatePhaseOverridden GatePhase = "overridden"
)

// GateState is the consolidated per-node goal-gate record (#602). It is keyed by
// node ID in Checkpoint.GateStates and replaces the four pre-#602 maps. Despite
// the "gate" name it also holds the one-shot fallback latch for a plain
// strict-failure node (strictFailureFallback keys FallbackTaken by node ID, not
// only by goal-gate ID) — the record is per node.
type GateState struct {
	// Phase is the gate's explicit lifecycle phase (see GatePhase).
	Phase GatePhase `json:"phase,omitempty"`

	// LastOutcome is the terminal status the gate node last produced
	// ("success"/"fail"/"partial_success"/...). Durable so a resumed run's
	// exit-time check sees a genuinely-passed gate as satisfied instead of
	// re-judging it against an empty in-memory map (#533).
	LastOutcome string `json:"last_outcome,omitempty"`

	// FallbackTaken records that the node has already used its one-shot
	// fallback/escalation route. Persisted so the guard survives resume — the
	// fallback is one-shot per node per run even across resumes.
	FallbackTaken bool `json:"fallback_taken,omitempty"`
}

// gateState returns the GateState for id, creating (and inserting) a zero-value
// record if none exists. The returned pointer is the live map entry — callers
// mutate it in place. This is the single write entry point for gate state.
func (cp *Checkpoint) gateState(id string) *GateState {
	if cp.GateStates == nil {
		cp.GateStates = make(map[string]*GateState)
	}
	gs := cp.GateStates[id]
	if gs == nil {
		gs = &GateState{}
		cp.GateStates[id] = gs
	}
	return gs
}

// gateStateOrNil returns the GateState for id without creating one, so read-only
// queries never grow the map (and stay nil-safe on a pre-gate checkpoint).
func (cp *Checkpoint) gateStateOrNil(id string) *GateState {
	if cp.GateStates == nil {
		return nil
	}
	return cp.GateStates[id]
}

// SetGateRecheckPending marks a goal-gate node as awaiting re-execution
// after a retry/fallback redirect (#348 defect 1). Transition: → RecheckPending.
func (cp *Checkpoint) SetGateRecheckPending(nodeID string) {
	cp.gateState(nodeID).Phase = GatePhaseRecheckPending
}

// ClearGateRecheckPending records that a goal-gate node has re-executed,
// clearing its pending recheck (#348 defect 1). Only a gate actually in the
// RecheckPending phase is reset to Active — an overridden gate is left untouched
// (mirrors the pre-#602 maps, where deleting the pending entry never affected the
// overridden entry).
func (cp *Checkpoint) ClearGateRecheckPending(nodeID string) {
	if gs := cp.gateStateOrNil(nodeID); gs != nil && gs.Phase == GatePhaseRecheckPending {
		gs.Phase = GatePhaseActive
	}
}

// IsGateRecheckPending reports whether a goal-gate node is awaiting
// re-execution after a retry/fallback redirect (#348 defect 1).
func (cp *Checkpoint) IsGateRecheckPending(nodeID string) bool {
	gs := cp.gateStateOrNil(nodeID)
	return gs != nil && gs.Phase == GatePhaseRecheckPending
}

// MarkGateOverridden records that a human resolved a failed goal gate via an
// override edge from its escalation (#348 defect 2). Transition: → Overridden.
// This replaces any pending phase, encoding the override-wins-over-pending
// precedence directly in the state instead of a cross-map ordering rule.
func (cp *Checkpoint) MarkGateOverridden(nodeID string) {
	cp.gateState(nodeID).Phase = GatePhaseOverridden
}

// ClearGateOverridden drops a gate's override when the gate re-executes, so a
// fresh failure on new work re-prompts the human (#348 defect 2). Only a gate
// actually in the Overridden phase is reset to Active.
func (cp *Checkpoint) ClearGateOverridden(nodeID string) {
	if gs := cp.gateStateOrNil(nodeID); gs != nil && gs.Phase == GatePhaseOverridden {
		gs.Phase = GatePhaseActive
	}
}

// IsGateOverridden reports whether a goal gate was human-overridden (#348
// defect 2). A gate with no state returns false.
func (cp *Checkpoint) IsGateOverridden(nodeID string) bool {
	gs := cp.gateStateOrNil(nodeID)
	return gs != nil && gs.Phase == GatePhaseOverridden
}

// IsFallbackTaken reports whether a node has already used its one-shot
// fallback/escalation route.
func (cp *Checkpoint) IsFallbackTaken(nodeID string) bool {
	gs := cp.gateStateOrNil(nodeID)
	return gs != nil && gs.FallbackTaken
}

// MarkFallbackTaken latches a node's one-shot fallback so a loop-back cannot
// re-escalate forever. Persisted so the guard survives resume.
func (cp *Checkpoint) MarkFallbackTaken(nodeID string) {
	cp.gateState(nodeID).FallbackTaken = true
}

// GateOutcome returns the terminal status a gate node last produced, or "" if
// the node has no recorded outcome. Durable across resume (#533).
func (cp *Checkpoint) GateOutcome(nodeID string) string {
	gs := cp.gateStateOrNil(nodeID)
	if gs == nil {
		return ""
	}
	return gs.LastOutcome
}

// SetGateOutcome records the terminal status a gate node produced so the
// exit-time check (and a resumed run) can read it back (#533).
func (cp *Checkpoint) SetGateOutcome(nodeID, status string) {
	cp.gateState(nodeID).LastOutcome = status
}

// legacyGateMaps is the pre-#602 wire shape: the four scattered gate maps. It is
// unmarshaled only inside LoadCheckpoint to migrate an old checkpoint; the fields
// no longer exist on Checkpoint, so a live run can never write them again.
type legacyGateMaps struct {
	NodeOutcomes       map[string]string `json:"node_outcomes"`
	FallbackTaken      map[string]bool   `json:"fallback_taken"`
	GateRecheckPending map[string]bool   `json:"gate_recheck_pending"`
	OverriddenGates    map[string]bool   `json:"overridden_gates"`
}

// migrateLegacyGateState folds a pre-#602 checkpoint's four scattered maps into
// the unified GateStates record (one-way). It is a no-op when no legacy field is
// present (a new-format checkpoint round-trips through GateStates directly). The
// phases are applied last, overridden after pending (in migrateLegacyPhases), so
// a legacy checkpoint carrying BOTH for one gate resolves to Overridden — the
// same precedence checkGoalGateNode enforces at runtime (override wins).
func (cp *Checkpoint) migrateLegacyGateState(legacy legacyGateMaps) {
	// A value already set by the new-format GateStates in the same file is not
	// clobbered (legacy and new are mutually exclusive in practice; defensive).
	for id, outcome := range legacy.NodeOutcomes {
		if gs := cp.gateState(id); gs.LastOutcome == "" {
			gs.LastOutcome = outcome
		}
	}
	for id, taken := range legacy.FallbackTaken {
		if taken {
			cp.gateState(id).FallbackTaken = true
		}
	}
	cp.migrateLegacyPhases(legacy.GateRecheckPending, legacy.OverriddenGates)
}

// migrateLegacyPhases resolves the two pre-#602 phase maps into a single Phase,
// overridden winning over pending for any gate present in both.
func (cp *Checkpoint) migrateLegacyPhases(pending, overridden map[string]bool) {
	for id, isPending := range pending {
		if isPending {
			cp.gateState(id).Phase = GatePhaseRecheckPending
		}
	}
	for id, isOverridden := range overridden {
		if isOverridden {
			cp.gateState(id).Phase = GatePhaseOverridden
		}
	}
}
