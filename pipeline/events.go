// ABOUTME: Event types emitted during pipeline execution for UI and logging.
// ABOUTME: Mirrors the Layer 2 EventHandler pattern with pipeline-specific event types.
package pipeline

import "time"

// PipelineEventType identifies the kind of lifecycle event emitted during pipeline execution.
type PipelineEventType string

const (
	EventPipelineStarted   PipelineEventType = "pipeline_started"
	EventPipelineCompleted PipelineEventType = "pipeline_completed"
	EventPipelineFailed    PipelineEventType = "pipeline_failed"
	EventStageStarted      PipelineEventType = "stage_started"
	EventStageCompleted    PipelineEventType = "stage_completed"
	EventStageFailed       PipelineEventType = "stage_failed"
	EventStageRetrying     PipelineEventType = "stage_retrying"
	EventCheckpointSaved   PipelineEventType = "checkpoint_saved"
	EventCheckpointFailed  PipelineEventType = "checkpoint_failed"
	EventParallelStarted   PipelineEventType = "parallel_started"
	EventParallelCompleted PipelineEventType = "parallel_completed"
	EventManagerCycleTick  PipelineEventType = "manager_cycle_tick"
	EventLoopRestart       PipelineEventType = "loop_restart"
	EventWarning           PipelineEventType = "warning"
	EventEdgeTiebreaker    PipelineEventType = "edge_tiebreaker"

	// Decision audit trail events — capture decision points for post-run reconstruction.
	EventDecisionEdge      PipelineEventType = "decision_edge"
	EventDecisionCondition PipelineEventType = "decision_condition"
	EventDecisionOutcome   PipelineEventType = "decision_outcome"
	EventDecisionRestart   PipelineEventType = "decision_restart"

	// Cost governance events — emitted after each completed node and when a budget is exceeded.
	EventCostUpdated    PipelineEventType = "cost_updated"
	EventBudgetExceeded PipelineEventType = "budget_exceeded"

	// EventToolOutputTruncated fires after a tool node when one or both of
	// its output streams exceeded the per-stream cap and head bytes were
	// elided. Carries TruncationDetail. Surfaced by `tracker diagnose` so
	// operators can correlate routing mismatches with truncation (issue
	// #208).
	EventToolOutputTruncated PipelineEventType = "tool_output_truncated"

	// EventConditionalFallthrough fires when at least one conditional
	// outgoing edge from a node was evaluated, none matched, and routing
	// fell through to an unconditional fallback. The runtime already
	// emits per-condition results and a final decision_edge event, but
	// this aggregating signal is what `tracker diagnose` correlates with
	// EventToolOutputTruncated to surface "your routing marker may have
	// been dropped" (issue #208). It does NOT fire on intentional
	// unconditional-only routing.
	EventConditionalFallthrough PipelineEventType = "conditional_fallthrough"
)

// CostSnapshot is the payload for EventCostUpdated and EventBudgetExceeded events.
// It is a point-in-time view of the run's aggregate token usage, cost, and
// wall-clock elapsed time. Estimated is true when any session contributing
// to this snapshot was heuristic-derived (e.g. ACP rune-count estimator);
// per-provider detail is carried inside ProviderTotals via
// ProviderUsage.Estimated.
type CostSnapshot struct {
	TotalTokens    int
	TotalCostUSD   float64
	ProviderTotals map[string]ProviderUsage
	WallElapsed    time.Duration
	Estimated      bool
}

// ConditionEval records a single conditional edge whose condition
// evaluated false during routing. Used as the payload of
// EventConditionalFallthrough so diagnostics can show which routing
// intents missed before the fallback fired.
type ConditionEval struct {
	EdgeTo    string `json:"edge_to"`
	Condition string `json:"condition"`
}

// DecisionDetail carries structured data about a pipeline decision point.
// It is attached to PipelineEvent via the Decision field for audit trail events.
type DecisionDetail struct {
	// Edge selection fields.
	EdgeFrom     string `json:"edge_from,omitempty"`
	EdgeTo       string `json:"edge_to,omitempty"`
	EdgePriority string `json:"edge_priority,omitempty"` // "condition", "label", "suggested", "weight", "lexical"

	// Condition evaluation fields.
	EdgeCondition  string `json:"edge_condition,omitempty"`
	ConditionMatch bool   `json:"condition_match,omitempty"`

	// Node outcome fields.
	OutcomeStatus  string            `json:"outcome_status,omitempty"`
	ContextUpdates map[string]string `json:"context_updates,omitempty"`

	// Context snapshot at the decision point (routing-relevant keys).
	ContextSnapshot map[string]string `json:"context_snapshot,omitempty"`

	// Restart/loop fields.
	RestartCount int      `json:"restart_count,omitempty"`
	ClearedNodes []string `json:"cleared_nodes,omitempty"`

	// Session stats from handler outcome.
	TokenInput  int `json:"token_input,omitempty"`
	TokenOutput int `json:"token_output,omitempty"`

	// ConditionsTried lists the conditional edges that were evaluated
	// false on the way to a fallback selection. Populated only on
	// EventConditionalFallthrough events. Each entry pairs the target
	// node with the literal condition string so a diagnostics consumer
	// can render "your routing intents X, Y, Z all missed" without
	// re-evaluating expressions.
	ConditionsTried []ConditionEval `json:"conditions_tried,omitempty"`
}

// TruncationDetail carries structured data about a tool-output truncation
// event. Attached to PipelineEvent via the Truncation field when a tool
// stream's tail-window cap was hit. Issue #208.
type TruncationDetail struct {
	Stream        string `json:"stream"`         // "stdout" or "stderr"
	Limit         int    `json:"limit"`          // per-stream cap in effect
	CapturedBytes int    `json:"captured_bytes"` // bytes preserved in the captured tail
	DroppedBytes  int    `json:"dropped_bytes"`  // bytes elided from the head
	TotalBytes    int    `json:"total_bytes"`    // CapturedBytes + DroppedBytes for convenience
}

// PipelineEvent carries data about a single pipeline lifecycle occurrence.
type PipelineEvent struct {
	Type       PipelineEventType
	Timestamp  time.Time
	RunID      string
	NodeID     string
	Message    string
	Err        error
	Decision   *DecisionDetail   // non-nil for decision audit trail events
	Cost       *CostSnapshot     // non-nil for EventCostUpdated and EventBudgetExceeded events
	Truncation *TruncationDetail // non-nil for EventToolOutputTruncated
}

// PipelineEventHandler receives pipeline events for observability purposes.
type PipelineEventHandler interface {
	HandlePipelineEvent(evt PipelineEvent)
}

// PipelineEventHandlerFunc is an adapter that lets ordinary functions serve as PipelineEventHandler.
type PipelineEventHandlerFunc func(evt PipelineEvent)

func (f PipelineEventHandlerFunc) HandlePipelineEvent(evt PipelineEvent) { f(evt) }

// pipelineNoopHandler silently discards all events.
type pipelineNoopHandler struct{}

func (pipelineNoopHandler) HandlePipelineEvent(PipelineEvent) {}

// PipelineNoopHandler is a handler that does nothing, useful as a default.
var PipelineNoopHandler PipelineEventHandler = pipelineNoopHandler{}

// NodeScopedPipelineHandler wraps a PipelineEventHandler and prefixes every
// event's NodeID with parentNodeID + "/". Child pipeline lifecycle events
// (started/completed/failed) are filtered out because the parent engine
// already tracks the subgraph node's lifecycle.
func NodeScopedPipelineHandler(parentNodeID string, inner PipelineEventHandler) PipelineEventHandler {
	if inner == nil {
		return PipelineNoopHandler
	}
	return PipelineEventHandlerFunc(func(evt PipelineEvent) {
		// Filter child pipeline lifecycle events — the parent tracks these.
		switch evt.Type {
		case EventPipelineStarted, EventPipelineCompleted, EventPipelineFailed:
			return
		}
		if evt.NodeID != "" {
			evt.NodeID = parentNodeID + "/" + evt.NodeID
		}
		inner.HandlePipelineEvent(evt)
	})
}

// PipelineMultiHandler fans out each event to every provided handler.
// Nil handlers in the list are safely skipped.
func PipelineMultiHandler(handlers ...PipelineEventHandler) PipelineEventHandler {
	cp := make([]PipelineEventHandler, len(handlers))
	copy(cp, handlers)
	return pipelineMultiHandler(cp)
}

type pipelineMultiHandler []PipelineEventHandler

func (m pipelineMultiHandler) HandlePipelineEvent(evt PipelineEvent) {
	for _, h := range m {
		if h != nil {
			h.HandlePipelineEvent(evt)
		}
	}
}
