// ABOUTME: Checkpoint provenance for resume rewind (#651): where a fail-routed node came from and where the run halted.
// ABOUTME: Enriches #650's per-node GateState.FallbackOrigin and adds HaltedAt; all omitempty, legacy-safe.
package pipeline

// FallbackOriginKind names the routing mechanism that carried a failed node
// to its fallback/escalation target (#651). It is informational — the rewind
// decision keys on the origin node's handler, not on the kind — but it lets
// `tracker diagnose` and a resume log line say HOW the run got there.
type FallbackOriginKind string

const (
	// FallbackOriginFailEdge: any edge advanced while ctx.outcome = fail —
	// an explicit `when ctx.outcome = fail` edge (build_product's
	// `Setup -> AbortRun`) or an unconditional fallthrough on a failed node.
	FallbackOriginFailEdge FallbackOriginKind = "fail_edge"
	// FallbackOriginStrictFailure: the node/graph-level fallback_target
	// (`on_failure`) consulted by strictFailureFallback (#295). The #653
	// failure cascade (guards all missed on a fail outcome) lands here too —
	// it resolves the target through the same findFallbackTarget and the
	// same one-shot latch; the two are told apart by the cascade's
	// conditional_fallthrough event, not by a separate kind.
	FallbackOriginStrictFailure FallbackOriginKind = "strict_failure"
	// FallbackOriginRetryExhausted: fallback_retry_target after the retry
	// budget was spent (handleRetryExhausted).
	FallbackOriginRetryExhausted FallbackOriginKind = "retry_exhausted"
	// FallbackOriginGoalGate: an unsatisfied goal gate's one-shot fallback
	// redirect at exit time (handleExitNode).
	FallbackOriginGoalGate FallbackOriginKind = "goal_gate"
)

// FallbackOriginRecord is the provenance of one fail-routing hop (#651): the
// node whose failure routed the run to a target, the outcome it produced, a
// short reason, and the mechanism that carried it. It is a read-side
// projection of the target's GateState (#650's FallbackOrigin node field plus
// the #651 enrichment fields) — there is ONE persisted representation.
type FallbackOriginRecord struct {
	Node    string             `json:"node"`
	Outcome string             `json:"outcome,omitempty"`
	Reason  string             `json:"reason,omitempty"`
	Kind    FallbackOriginKind `json:"kind,omitempty"`
}

// maxFallbackReasonLen bounds the persisted failure reason so a multi-KB
// tool_stderr tail does not bloat every checkpoint save.
const maxFallbackReasonLen = 240

// RecordFallbackOrigin remembers that origin's failure routed the run to
// target (#651): #650's SetFallbackOrigin plus the enrichment fields, on the
// target's GateState. A self-route (target == origin) is ignored so it cannot
// clobber the real upstream origin. The latest hop into a target wins:
// provenance describes the most recent arrival (an ordinary entry clears it —
// advanceToNextNode calls ClearFallbackOrigin before this on the same hop).
func (cp *Checkpoint) RecordFallbackOrigin(target, origin string, outcome TerminalStatus, reason string, kind FallbackOriginKind) {
	if target == "" || origin == "" || target == origin {
		return
	}
	if len(reason) > maxFallbackReasonLen {
		reason = reason[:maxFallbackReasonLen] + "…"
	}
	gs := cp.gateState(target)
	gs.FallbackOrigin = origin
	gs.FallbackOriginOutcome = string(outcome)
	gs.FallbackOriginReason = reason
	gs.FallbackOriginKind = kind
}

// GetFallbackOrigin returns the recorded provenance for target, if any. A
// legacy checkpoint (pre-#650 / pre-#651) has no origin and reports nothing —
// resume then behaves exactly as before (no rewind). A #650-only record (node
// set, enrichment empty) still rewinds: the node is what matters.
func (cp *Checkpoint) GetFallbackOrigin(target string) (FallbackOriginRecord, bool) {
	gs := cp.gateStateOrNil(target)
	if gs == nil || gs.FallbackOrigin == "" {
		return FallbackOriginRecord{}, false
	}
	return FallbackOriginRecord{
		Node:    gs.FallbackOrigin,
		Outcome: gs.FallbackOriginOutcome,
		Reason:  gs.FallbackOriginReason,
		Kind:    gs.FallbackOriginKind,
	}, true
}

// RecordHalt marks the node at which the run dead-stopped (#651): a
// strict-failure halt, an exhausted retry budget with no fallback, a failed
// exit node, or an unsatisfied goal gate with no redirect. Resume reads and
// clears it — the halted node is re-executed (it may already sit in
// CompletedNodes because OutcomeFail marks completion before routing), and,
// when it was reached by fail-routing, the run rewinds to the origin instead.
func (cp *Checkpoint) RecordHalt(nodeID string) {
	cp.HaltedAt = nodeID
}
