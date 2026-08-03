// ABOUTME: Per-turn agent usage carried onto activity.jsonl agent lines (audit-finding-1).
// ABOUTME: Keeps WriteAgentEvent's usage plumbing (and the agent-error join) out of events_jsonl.go's size budget.
package pipeline

// AgentTurnUsage is the per-turn token accounting for an agent
// turn_metrics / llm_finish event. WriteAgentEvent persists it onto the
// activity.jsonl line so a run replayed from the audit log reports the
// same per-turn usage the live NDJSON wire carries (#508). A zero value
// (the common case for non-metrics agent events) leaves every token
// field zero, which omitempty then drops from the line.
type AgentTurnUsage struct {
	TokenInput      int
	TokenOutput     int
	TokenCacheRead  int
	TokenCacheWrite int
	TurnCostUSD     float64
}

// applyAgentUsage copies the per-turn usage onto the log entry. Caller
// holds no lock requirement — entry is a local not-yet-written value.
func applyAgentUsage(entry *jsonlLogEntry, u AgentTurnUsage) {
	entry.TokenInput = u.TokenInput
	entry.TokenOutput = u.TokenOutput
	entry.TokenCacheRead = u.TokenCacheRead
	entry.TokenCacheWrite = u.TokenCacheWrite
	entry.TurnCostUSD = u.TurnCostUSD
}
