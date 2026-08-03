// ABOUTME: Populates the structured StreamEvent payload fields from pipeline/agent events.
// ABOUTME: Field names mirror pipeline's jsonlLogEntry so one decoder serves NDJSON and activity.jsonl.
package tracker

import (
	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// applyStreamPipelinePayloads copies every structured payload hanging off a
// PipelineEvent onto the wire event. Each group is a no-op when its payload is
// nil, which is what keeps the omitempty guarantee: an event type that carries
// no payload serializes none of these keys.
//
// The wire event is marshalled synchronously inside the handler that calls this,
// so the maps and slices referenced here are read before control returns to the
// emitter. Only OverrideSubgraphPath is copied, matching pipeline's own
// activity.jsonl writer.
func applyStreamPipelinePayloads(entry *StreamEvent, evt pipeline.PipelineEvent) {
	applyStreamDecision(entry, evt.Decision)
	applyStreamCost(entry, evt.Cost)
	applyStreamToolSignals(entry, evt)
	applyStreamNodeSignals(entry, evt)
	applyStreamSnapshot(entry, evt.Snapshot)
}

// applyStreamDecision copies edge-decision fields (decision_* and
// conditional_fallthrough events).
func applyStreamDecision(entry *StreamEvent, d *pipeline.DecisionDetail) {
	if d == nil {
		return
	}
	entry.EdgeFrom = d.EdgeFrom
	entry.EdgeTo = d.EdgeTo
	entry.EdgeCondition = d.EdgeCondition
	entry.EdgePriority = d.EdgePriority
	if d.EdgeCondition != "" {
		match := d.ConditionMatch
		entry.ConditionMatch = &match
	}
	entry.OutcomeStatus = d.OutcomeStatus
	entry.ContextSnapshot = d.ContextSnapshot
	entry.ContextUpdates = d.ContextUpdates
	if d.RestartCount > 0 {
		rc := d.RestartCount
		entry.RestartCount = &rc
	}
	entry.ClearedNodes = d.ClearedNodes
	entry.ConditionsTried = d.ConditionsTried
	entry.TokenInput = d.TokenInput
	entry.TokenOutput = d.TokenOutput
}

// applyStreamCost copies the run-cumulative cost snapshot (cost_updated,
// budget_exceeded).
func applyStreamCost(entry *StreamEvent, c *pipeline.CostSnapshot) {
	if c == nil {
		return
	}
	entry.TotalTokens = c.TotalTokens
	entry.TotalCostUSD = c.TotalCostUSD
	entry.ProviderTotals = c.ProviderTotals
	entry.WallElapsedMs = c.WallElapsed.Milliseconds()
	entry.Estimated = c.Estimated
}

// applyStreamToolSignals copies the tool-node diagnostic payloads (truncation,
// marker, route).
func applyStreamToolSignals(entry *StreamEvent, evt pipeline.PipelineEvent) {
	if t := evt.Truncation; t != nil {
		entry.TruncStream = t.Stream
		entry.TruncLimit = t.Limit
		entry.TruncCaptured = t.CapturedBytes
		entry.TruncDropped = t.DroppedBytes
		entry.TruncTotal = t.TotalBytes
	}
	if m := evt.Marker; m != nil {
		entry.MarkerPattern = m.Pattern
		entry.MarkerTail = m.CapturedTail
		entry.MarkerError = m.Error
	}
	if r := evt.Route; r != nil {
		entry.RouteTail = r.CapturedTail
	}
}

// applyStreamNodeSignals copies the node-level payloads (auto-status, override,
// gate lifecycle).
func applyStreamNodeSignals(entry *StreamEvent, evt pipeline.PipelineEvent) {
	if a := evt.AutoStatus; a != nil {
		entry.AutoStatusTail = a.ResponseTail
		entry.AutoStatusFailClosed = a.FailClosed
	}
	if o := evt.Override; o != nil {
		entry.OverrideGate = o.GateNodeID
		entry.OverrideLabel = o.Label
		entry.OverrideActor = o.Actor
		if len(o.SubgraphPath) > 0 {
			// Copy to defend against later mutation of the source slice.
			entry.OverrideSubgraphPath = append([]string(nil), o.SubgraphPath...)
		}
	}
	if g := evt.Gate; g != nil {
		applyStreamGate(entry, g)
	}
}

// applyStreamGate copies the gate lifecycle payload (gate_opened,
// gate_resolved). Open-time and resolve-time fields are disjoint in practice;
// both sets are copied and omitempty drops the ones the emitting event left
// unset. A gate that failed to collect an answer surfaces its reason on the
// shared error field, matching the activity.jsonl writer.
func applyStreamGate(entry *StreamEvent, g *pipeline.GateDetail) {
	entry.GateID = g.GateID
	entry.GateMode = g.Mode
	entry.GateLabel = g.Label
	entry.GatePrompt = g.Prompt
	if len(g.Choices) > 0 {
		// Copy to defend against later mutation of the source slice.
		entry.GateChoices = append([]string(nil), g.Choices...)
	}
	if len(g.Question) > 0 {
		entry.GateQuestions = append([]pipeline.GateQuestion(nil), g.Question...)
	}
	entry.GateResponse = g.Response
	entry.GateOutcome = g.Outcome
	entry.GateActor = g.Actor
	entry.GateTimedOut = g.TimedOut
	if g.Error != "" && entry.Error == "" {
		entry.Error = g.Error
	}
}

// applyStreamSnapshot copies the run snapshot carried on pipeline_started.
func applyStreamSnapshot(entry *StreamEvent, snap *pipeline.RunSnapshot) {
	if snap == nil {
		return
	}
	if len(snap.Nodes) > 0 {
		nodes := make([]StreamSnapshotNode, 0, len(snap.Nodes))
		for _, n := range snap.Nodes {
			nodes = append(nodes, StreamSnapshotNode{ID: n.ID, Label: n.Label, Handler: n.Handler})
		}
		entry.SnapshotNodes = nodes
	}
	entry.SnapshotStartNode = snap.StartNode
	entry.SnapshotExitNode = snap.ExitNode
	entry.SnapshotCurrentNode = snap.CurrentNode
	if len(snap.CompletedNodes) > 0 {
		entry.SnapshotCompletedNodes = append([]string(nil), snap.CompletedNodes...)
	}
}

// applyStreamUsage copies per-call token usage onto the wire event. Used by the
// agent and LLM-trace paths, where usage rides on turn_metrics / llm_finish
// (#508). fallbackCost is used when the usage itself carries no estimated cost.
func applyStreamUsage(entry *StreamEvent, u llm.Usage, fallbackCost float64) {
	entry.TokenInput = u.InputTokens
	entry.TokenOutput = u.OutputTokens
	if u.CacheReadTokens != nil {
		entry.TokenCacheRead = *u.CacheReadTokens
	}
	if u.CacheWriteTokens != nil {
		entry.TokenCacheWrite = *u.CacheWriteTokens
	}
	entry.TurnCostUSD = u.EstimatedCost
	if entry.TurnCostUSD == 0 {
		entry.TurnCostUSD = fallbackCost
	}
}

// applyStreamAgentCapture copies the #519 run-reconstruction fields off an
// agent event onto the wire, mirroring pipeline.applyAgentEventFields so the
// live --json stream carries the same session/turn/call identity and per-turn
// capture detail that activity.jsonl does (#526). The #508 top-level usage keys
// (token_input/output, token_cache_*, turn_cost_usd) are populated separately
// by applyStreamUsage; these are the distinct capture-namespace keys.
func applyStreamAgentCapture(entry *StreamEvent, evt agent.Event) {
	entry.SessionID = evt.SessionID
	entry.TurnNo = evt.Turn
	entry.ToolInput = evt.ToolInput
	entry.FinishReason = evt.FinishReason
	entry.CallID = evt.CallID
	entry.ToolDurationMs = evt.ToolDuration.Milliseconds()
	entry.ContextUtilization = evt.ContextUtilization
	applyStreamCaptureUsage(entry, evt.Usage)
	applyStreamCaptureMetrics(entry, evt.Metrics)
}

// applyStreamCaptureUsage copies the capture-namespace usage detail (reasoning
// and cache token counts, estimated cost). The optional pointers are nil for
// providers that don't report that dimension.
func applyStreamCaptureUsage(entry *StreamEvent, u llm.Usage) {
	entry.EstimatedCost = u.EstimatedCost
	if u.ReasoningTokens != nil {
		entry.ReasoningTokens = *u.ReasoningTokens
	}
	if u.CacheReadTokens != nil {
		entry.CacheReadTokens = *u.CacheReadTokens
	}
	if u.CacheWriteTokens != nil {
		entry.CacheWriteTokens = *u.CacheWriteTokens
	}
}

// applyStreamCaptureMetrics copies the per-turn metrics block. Metrics is nil
// on every event except turn_metrics, where its values win over the raw usage
// (they are computed against the resolved post-failover model).
func applyStreamCaptureMetrics(entry *StreamEvent, m *agent.TurnMetrics) {
	if m == nil {
		return
	}
	entry.CacheReadTokens = m.CacheReadTokens
	entry.CacheWriteTokens = m.CacheWriteTokens
	entry.ContextUtilization = m.ContextUtilization
	entry.ToolCacheHits = m.ToolCacheHits
	entry.ToolCacheMisses = m.ToolCacheMisses
	entry.TurnDurationMs = m.TurnDuration.Milliseconds()
	entry.EstimatedCost = m.EstimatedCost
}

// turnMetricsCost returns the per-turn estimated cost of a turn_metrics payload,
// or 0 when the event carries none. The session computes this against the
// resolved (post-failover) model, so it is the better cost source when the raw
// usage left EstimatedCost unset.
func turnMetricsCost(m *agent.TurnMetrics) float64 {
	if m == nil {
		return 0
	}
	return m.EstimatedCost
}
