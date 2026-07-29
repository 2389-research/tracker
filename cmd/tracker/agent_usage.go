// ABOUTME: Builds pipeline.AgentTurnUsage from an agent.Event for the activity-log writer.
// ABOUTME: Mirrors the NDJSON wire's applyStreamUsage so log and stream carry identical per-turn usage.
package main

import (
	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/pipeline"
)

// agentTurnUsage extracts the per-turn token accounting from an agent event
// for WriteAgentEvent. It mirrors the NDJSON path (tracker's applyStreamUsage):
// the cost is the usage's own EstimatedCost, falling back to the turn-metrics
// cost (computed against the resolved post-failover model) when the raw usage
// left it unset. Nil cache pointers decode to zero, which omitempty drops.
func agentTurnUsage(evt agent.Event) pipeline.AgentTurnUsage {
	u := pipeline.AgentTurnUsage{
		TokenInput:  evt.Usage.InputTokens,
		TokenOutput: evt.Usage.OutputTokens,
		TurnCostUSD: evt.Usage.EstimatedCost,
	}
	if evt.Usage.CacheReadTokens != nil {
		u.TokenCacheRead = *evt.Usage.CacheReadTokens
	}
	if evt.Usage.CacheWriteTokens != nil {
		u.TokenCacheWrite = *evt.Usage.CacheWriteTokens
	}
	if u.TurnCostUSD == 0 && evt.Metrics != nil {
		u.TurnCostUSD = evt.Metrics.EstimatedCost
	}
	return u
}
