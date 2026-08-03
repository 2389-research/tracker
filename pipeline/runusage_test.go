// ABOUTME: Tests for how run totals are derived from a log that reports the same consumption at several granularities.
// ABOUTME: Covers cross-tier de-duplication, per-visit node rollups, dual-path call counting, and per-node attribution.
package pipeline

import (
	"path/filepath"
	"testing"
)

// TestRunTotalsIgnoreCoarserRestatements is the regression test for a live
// t6 codegen run reporting exactly three times its real token count. One call
// is logged three ways — as the call, as the turn that issued it, and as the
// node rollup — and summing all three tripled every figure.
func TestRunTotalsIgnoreCoarserRestatements(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "r1")
	writeLog(t, runDir,
		`{"ts":"2026-01-01T00:00:00.000Z","source":"pipeline","type":"stage_started","node_id":"Gen","node_kind":"codergen","attempt_no":1}`,
		`{"ts":"2026-01-01T00:00:01.000Z","source":"agent","type":"turn_start","node_id":"Gen","session_id":"s1","turn_no":1}`,
		`{"ts":"2026-01-01T00:00:02.000Z","source":"agent","type":"llm_finish","node_id":"Gen","session_id":"s1","turn_no":1,"call_id":"c1","token_input":100,"token_output":40,"cache_read_tokens":7}`,
		`{"ts":"2026-01-01T00:00:03.000Z","source":"agent","type":"turn_metrics","node_id":"Gen","session_id":"s1","turn_no":1,"token_input":100,"token_output":40,"cache_read_tokens":7,"estimated_cost":0.5}`,
		`{"ts":"2026-01-01T00:00:04.000Z","source":"pipeline","type":"decision_outcome","node_id":"Gen","outcome_status":"success","token_input":100,"token_output":40}`,
	)

	m, err := AssembleRunManifest(runDir, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if m.Totals.InputTokens != 100 || m.Totals.OutputTokens != 40 {
		t.Errorf("tokens counted %d/%d, want 100/40 (one call, not three restatements)",
			m.Totals.InputTokens, m.Totals.OutputTokens)
	}
	if m.Totals.CacheReadTokens != 7 {
		t.Errorf("cache read tokens = %d, want 7", m.Totals.CacheReadTokens)
	}
	// Cost is reported only by the turn tier, so it must survive tokens being
	// taken from the call tier.
	if m.Totals.CostUSD != 0.5 {
		t.Errorf("cost = %v, want 0.5 (from the turn tier)", m.Totals.CostUSD)
	}
	if m.Totals.LLMCalls != 1 {
		t.Errorf("llm_calls = %d, want 1", m.Totals.LLMCalls)
	}
}

// TestRunTotalsSumEveryNodeVisit guards the opposite error: node rollups are
// emitted once per visit, so a retried node reports twice and both visits are
// real consumption. Keying them by node id alone kept only the last and
// silently halved archived runs whose only usage figures came from that tier.
func TestRunTotalsSumEveryNodeVisit(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "r2")
	writeLog(t, runDir,
		`{"ts":"2026-01-01T00:00:01.000Z","source":"pipeline","type":"decision_outcome","node_id":"Gen","token_input":100,"token_output":10}`,
		`{"ts":"2026-01-01T00:00:02.000Z","source":"pipeline","type":"decision_outcome","node_id":"Gen","token_input":300,"token_output":20}`,
	)

	m, err := AssembleRunManifest(runDir, "r2")
	if err != nil {
		t.Fatal(err)
	}
	if m.Totals.InputTokens != 400 || m.Totals.OutputTokens != 30 {
		t.Errorf("tokens = %d/%d, want 400/30 (both visits)",
			m.Totals.InputTokens, m.Totals.OutputTokens)
	}
}

// TestLLMCallsUsesTheFullerLogPath covers logs written before call_id existed.
// Both paths record every call, and their timestamps differ by microseconds, so
// pairing by timestamp double-counts while trusting the agent path alone
// undercounts — one archived run recorded 253 calls on the client path and only
// 84 on the agent path.
func TestLLMCallsUsesTheFullerLogPath(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "r3")
	writeLog(t, runDir,
		`{"ts":"2026-01-01T00:00:01.000Z","source":"llm","type":"finish","model":"m"}`,
		`{"ts":"2026-01-01T00:00:01.001Z","source":"agent","type":"llm_finish","node_id":"Gen","model":"m"}`,
		`{"ts":"2026-01-01T00:00:02.000Z","source":"llm","type":"finish","model":"m"}`,
		`{"ts":"2026-01-01T00:00:03.000Z","source":"llm","type":"finish","model":"m"}`,
		`{"ts":"2026-01-01T00:00:04.000Z","source":"pipeline","type":"decision_outcome","node_id":"Gen","token_input":5,"token_output":5}`,
	)

	m, err := AssembleRunManifest(runDir, "r3")
	if err != nil {
		t.Fatal(err)
	}
	if m.Totals.LLMCalls != 3 {
		t.Errorf("llm_calls = %d, want 3 (the fuller path, neither the union of 4 nor the agent subset of 1)",
			m.Totals.LLMCalls)
	}
}

// TestNodeUsageSumsToRunTotals is the invariant that makes per-node cost
// trustworthy: attribute from the same tier the run totals use, so the parts
// add up to the whole.
func TestNodeUsageSumsToRunTotals(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "r4")
	writeLog(t, runDir,
		`{"ts":"2026-01-01T00:00:01.000Z","source":"agent","type":"llm_finish","node_id":"A","call_id":"c1","token_input":100,"token_output":10}`,
		`{"ts":"2026-01-01T00:00:02.000Z","source":"agent","type":"turn_metrics","node_id":"A","session_id":"s1","turn_no":1,"estimated_cost":0.25}`,
		`{"ts":"2026-01-01T00:00:03.000Z","source":"agent","type":"llm_finish","node_id":"B","call_id":"c2","token_input":50,"token_output":5}`,
		`{"ts":"2026-01-01T00:00:04.000Z","source":"agent","type":"turn_metrics","node_id":"B","session_id":"s2","turn_no":1,"estimated_cost":0.75}`,
		`{"ts":"2026-01-01T00:00:05.000Z","source":"pipeline","type":"stage_started","node_id":"Tool","node_kind":"tool"}`,
	)

	m, err := AssembleRunManifest(runDir, "r4")
	if err != nil {
		t.Fatal(err)
	}
	var in, out int
	var cost float64
	for _, n := range m.Nodes {
		if n.ID == "Tool" && n.Usage != nil {
			t.Errorf("tool node carries usage %+v, want none", n.Usage)
		}
		if n.Usage == nil {
			continue
		}
		in += n.Usage.InputTokens
		out += n.Usage.OutputTokens
		cost += n.Usage.CostUSD
	}
	if in != m.Totals.InputTokens || out != m.Totals.OutputTokens {
		t.Errorf("node tokens sum to %d/%d, run totals say %d/%d",
			in, out, m.Totals.InputTokens, m.Totals.OutputTokens)
	}
	if cost != m.Totals.CostUSD {
		t.Errorf("node costs sum to %v, run total says %v", cost, m.Totals.CostUSD)
	}
}

// TestNodeAttributionSurvivesANodelessDuplicate covers a CodeRabbit finding on
// #519: both log paths share a call_id, but the client-sourced "finish" line
// carries no node_id while the agent-sourced "llm_finish" does. Last-writer-wins
// meant a node-less duplicate arriving second erased the attribution, so the run
// totals stayed right while the per-node shares stopped summing to the whole —
// the one invariant runusage.go exists to hold.
func TestNodeAttributionSurvivesANodelessDuplicate(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "r5")
	writeLog(t, runDir,
		// Agent path first, carrying the node.
		`{"ts":"2026-01-01T00:00:01.000Z","source":"agent","type":"llm_finish","node_id":"Gen","call_id":"c1","token_input":100,"token_output":10}`,
		// Client path second, same call, no node_id.
		`{"ts":"2026-01-01T00:00:01.001Z","source":"llm","type":"finish","call_id":"c1","token_input":100,"token_output":10}`,
	)

	m, err := AssembleRunManifest(runDir, "r5")
	if err != nil {
		t.Fatal(err)
	}
	if m.Totals.InputTokens != 100 {
		t.Errorf("input_tokens = %d, want 100 (one call, counted once)", m.Totals.InputTokens)
	}
	var attributed int
	for _, n := range m.Nodes {
		if n.Usage != nil {
			attributed += n.Usage.InputTokens
		}
	}
	if attributed != m.Totals.InputTokens {
		t.Errorf("node usage sums to %d but run total is %d — the node-less duplicate erased the attribution",
			attributed, m.Totals.InputTokens)
	}
}
