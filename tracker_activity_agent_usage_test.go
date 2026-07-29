// ABOUTME: Regression test for audit-finding-1 — per-turn agent usage must survive a round-trip through activity.jsonl.
// ABOUTME: A turn_metrics line with token_input/output/cache/turn_cost set must decode with those values intact (E2).
package tracker

import (
	"testing"
)

// TestParseActivityLine_AgentTurnUsageSurvives asserts that a turn_metrics
// activity.jsonl line carries its per-turn agent usage all the way to the
// exported ActivityEntry. Before the audit-finding-1 fix the writer never
// emitted these fields (and the reader could not decode the cache/cost
// extras), so replaying a run from the audit log reported zero usage per
// turn even though the live NDJSON wire carried it (#508).
func TestParseActivityLine_AgentTurnUsageSurvives(t *testing.T) {
	line := `{"ts":"2026-07-28T12:00:00.000Z","source":"agent","type":"turn_metrics",` +
		`"node_id":"gen_code","provider":"anthropic","model":"claude-sonnet-4-6",` +
		`"token_input":100,"token_output":40,` +
		`"token_cache_read":900,"token_cache_write":12,"turn_cost_usd":0.0033}`

	entry, ok := ParseActivityLine(line)
	if !ok {
		t.Fatalf("ParseActivityLine rejected a well-formed turn_metrics line: %s", line)
	}
	if entry.TokenInput != 100 {
		t.Errorf("TokenInput = %d, want 100", entry.TokenInput)
	}
	if entry.TokenOutput != 40 {
		t.Errorf("TokenOutput = %d, want 40", entry.TokenOutput)
	}
	if entry.TokenCacheRead != 900 {
		t.Errorf("TokenCacheRead = %d, want 900", entry.TokenCacheRead)
	}
	if entry.TokenCacheWrite != 12 {
		t.Errorf("TokenCacheWrite = %d, want 12", entry.TokenCacheWrite)
	}
	if entry.TurnCostUSD < 0.00329 || entry.TurnCostUSD > 0.00331 {
		t.Errorf("TurnCostUSD = %f, want 0.0033", entry.TurnCostUSD)
	}
}
