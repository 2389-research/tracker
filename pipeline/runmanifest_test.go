// ABOUTME: Tests for run.json assembly from a run directory.
// ABOUTME: Covers node enumeration from two sources, call-id-grouped usage, tolerance of damaged logs, and backfill.
package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLog writes lines as an activity log in runDir.
func writeLog(t *testing.T, runDir string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(runDir, "activity.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAssembleRunManifestSummarizesNodes(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "r1")
	writeLog(t, runDir,
		`{"ts":"2026-01-01T00:00:00.000Z","source":"pipeline","type":"pipeline_started","run_id":"r1"}`,
		`{"ts":"2026-01-01T00:00:01.000Z","source":"pipeline","type":"stage_started","node_id":"Gen","node_kind":"codergen","attempt_no":1}`,
		`{"ts":"2026-01-01T00:00:02.000Z","source":"agent","type":"turn_start","node_id":"Gen","session_id":"s1","turn_no":1}`,
		`{"ts":"2026-01-01T00:00:03.000Z","source":"agent","type":"tool_call_start","node_id":"Gen","tool_name":"bash","tool_input":"{\"command\":\"ls\"}","session_id":"s1","turn_no":1}`,
		`{"ts":"2026-01-01T00:00:04.000Z","source":"pipeline","type":"stage_failed","node_id":"Gen"}`,
		`{"ts":"2026-01-01T00:00:05.000Z","source":"pipeline","type":"stage_retrying","node_id":"Gen","node_kind":"codergen","attempt_no":2}`,
		`{"ts":"2026-01-01T00:00:06.000Z","source":"pipeline","type":"decision_outcome","node_id":"Gen","outcome_status":"success"}`,
		`{"ts":"2026-01-01T00:00:07.000Z","source":"pipeline","type":"pipeline_completed","terminal_status":"success"}`,
	)

	m, err := AssembleRunManifest(runDir, "r1")
	if err != nil {
		t.Fatalf("AssembleRunManifest: %v", err)
	}

	if m.SchemaVersion != RunManifestSchemaVersion {
		t.Errorf("schema_version = %d, want %d", m.SchemaVersion, RunManifestSchemaVersion)
	}
	if m.TerminalStatus != "success" {
		t.Errorf("terminal_status = %q, want success", m.TerminalStatus)
	}
	if m.StartedAt != "2026-01-01T00:00:00.000Z" || m.FinishedAt != "2026-01-01T00:00:07.000Z" {
		t.Errorf("window = %s..%s, want first..last event timestamp", m.StartedAt, m.FinishedAt)
	}
	if len(m.Nodes) != 1 {
		t.Fatalf("got %d nodes, want 1", len(m.Nodes))
	}

	n := m.Nodes[0]
	if n.Kind != "codergen" {
		t.Errorf("kind = %q, want codergen", n.Kind)
	}
	// Attempts tracks the highest attempt seen, so a retried node reads 2.
	if n.Attempts != 2 {
		t.Errorf("attempts = %d, want 2", n.Attempts)
	}
	if n.Turns != 1 {
		t.Errorf("turns = %d, want 1", n.Turns)
	}
	// Failed and Outcome are independent: this node failed an attempt and then
	// succeeded, and collapsing them would hide the retry.
	if !n.Failed {
		t.Error("failed = false, want true (a stage_failed named this node)")
	}
	if n.Outcome != "success" {
		t.Errorf("outcome = %q, want success", n.Outcome)
	}
	if m.ToolCalls["bash"] != 1 {
		t.Errorf("tool_calls[bash] = %d, want 1", m.ToolCalls["bash"])
	}
}

func TestAssembleRunManifestGroupsUsageByCallID(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "r2")
	// The same call reported twice — once by the agent session re-emitting the
	// trace event, once by the client-level writer. Both paths are kept because
	// their coverage differs, so an ungrouped sum would double the usage.
	writeLog(t, runDir,
		`{"ts":"2026-01-01T00:00:01.000Z","source":"llm","type":"finish","call_id":"c1","token_input":100,"token_output":20,"estimated_cost":0.01}`,
		`{"ts":"2026-01-01T00:00:01.000Z","source":"agent","type":"llm_finish","call_id":"c1","token_input":100,"token_output":20,"estimated_cost":0.01}`,
		`{"ts":"2026-01-01T00:00:02.000Z","source":"llm","type":"finish","call_id":"c2","token_input":50,"token_output":10,"estimated_cost":0.005}`,
	)

	m, err := AssembleRunManifest(runDir, "r2")
	if err != nil {
		t.Fatalf("AssembleRunManifest: %v", err)
	}
	if m.Totals.LLMCalls != 2 {
		t.Errorf("llm_calls = %d, want 2 (one call reported twice is still one call)", m.Totals.LLMCalls)
	}
	if m.Totals.InputTokens != 150 {
		t.Errorf("input_tokens = %d, want 150 (not 250 — the duplicate must collapse)", m.Totals.InputTokens)
	}
	if m.Totals.OutputTokens != 30 {
		t.Errorf("output_tokens = %d, want 30", m.Totals.OutputTokens)
	}
	if m.Totals.CostUSD != 0.015 {
		t.Errorf("cost_usd = %v, want 0.015", m.Totals.CostUSD)
	}
}

func TestAssembleRunManifestMergesCheckpointNodes(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "r3")
	// A node the checkpoint completed but whose events are absent from the log.
	// Enumerating from either source alone loses nodes, which is why both are
	// consulted.
	writeLog(t, runDir,
		`{"ts":"2026-01-01T00:00:01.000Z","source":"pipeline","type":"stage_started","node_id":"Gen","node_kind":"codergen","attempt_no":1}`,
	)
	cp := &Checkpoint{
		RunID:          "r3",
		CurrentNode:    "Done",
		CompletedNodes: []string{"Setup", "Gen"},
		RestartCount:   2,
		Context:        map[string]string{"graph.goal": "ship the thing"},
	}
	if err := SaveCheckpoint(cp, filepath.Join(runDir, "checkpoint.json")); err != nil {
		t.Fatal(err)
	}

	m, err := AssembleRunManifest(runDir, "r3")
	if err != nil {
		t.Fatalf("AssembleRunManifest: %v", err)
	}
	if m.Goal != "ship the thing" {
		t.Errorf("goal = %q, want the checkpoint's graph.goal", m.Goal)
	}
	if m.ReachedNode != "Done" {
		t.Errorf("reached_node = %q, want Done", m.ReachedNode)
	}
	if m.RestartCount != 2 {
		t.Errorf("restart_count = %d, want 2", m.RestartCount)
	}

	ids := map[string]bool{}
	for _, n := range m.Nodes {
		ids[n.ID] = true
	}
	if !ids["Setup"] {
		t.Error("Setup missing: a node the checkpoint completed but the log never mentioned")
	}
	if !ids["Gen"] {
		t.Error("Gen missing from the node table")
	}
	if len(m.Nodes) != 2 {
		t.Errorf("got %d nodes, want 2 with no duplicate for Gen", len(m.Nodes))
	}
}

func TestAssembleRunManifestToleratesDamagedLog(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "r4")
	// A run killed mid-write leaves a truncated final line. That run is exactly
	// the one a manifest is most useful for, so a bad line must not be fatal.
	writeLog(t, runDir,
		`{"ts":"2026-01-01T00:00:01.000Z","source":"pipeline","type":"stage_started","node_id":"A","node_kind":"tool","attempt_no":1}`,
		`not json at all`,
		`{"ts":"2026-01-01T00:00:02.000Z","source":"pipeline","type":"stage_completed","node_id":"A"`,
	)

	m, err := AssembleRunManifest(runDir, "r4")
	if err != nil {
		t.Fatalf("a damaged log should still yield a manifest, got: %v", err)
	}
	if len(m.Nodes) != 1 || m.Nodes[0].ID != "A" {
		t.Errorf("nodes = %+v, want the one parseable node", m.Nodes)
	}
	// No terminal event survived, and that absence is reported rather than
	// guessed at.
	if m.TerminalStatus != "" {
		t.Errorf("terminal_status = %q, want empty for a run with no terminal event", m.TerminalStatus)
	}
}

func TestAssembleRunManifestStripsSentinel(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "r5")
	// The live secure log prefixes every line with the sentinel; the
	// artifact-dir snapshot does not. The assembler must read either.
	writeLog(t, runDir,
		ActivityLogSentinel+`{"ts":"2026-01-01T00:00:01.000Z","source":"pipeline","type":"stage_started","node_id":"A","node_kind":"tool"}`,
		ActivityLogSentinel+`{"ts":"2026-01-01T00:00:02.000Z","source":"pipeline","type":"pipeline_completed","terminal_status":"success"}`,
	)

	m, err := AssembleRunManifest(runDir, "r5")
	if err != nil {
		t.Fatalf("AssembleRunManifest: %v", err)
	}
	if m.TerminalStatus != "success" {
		t.Errorf("terminal_status = %q — sentinel-prefixed lines were not parsed", m.TerminalStatus)
	}
	if len(m.Nodes) != 1 {
		t.Errorf("got %d nodes, want 1", len(m.Nodes))
	}
}

func TestAssembleRunManifestRequiresActivityLog(t *testing.T) {
	// No log means nothing to describe — the one hard failure.
	if _, err := AssembleRunManifest(t.TempDir(), "nope"); err == nil {
		t.Error("expected an error when the activity log is missing")
	}
}

func TestAssembleRunManifestCountsHumanIntervention(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "r6")
	writeLog(t, runDir,
		`{"ts":"2026-01-01T00:00:01.000Z","source":"pipeline","type":"human_gate_opened","node_id":"Approve"}`,
		`{"ts":"2026-01-01T00:00:02.000Z","source":"pipeline","type":"human_gate_resolved","node_id":"Approve"}`,
		`{"ts":"2026-01-01T00:00:03.000Z","source":"agent","type":"steering_injected","node_id":"Gen"}`,
	)
	m, err := AssembleRunManifest(runDir, "r6")
	if err != nil {
		t.Fatalf("AssembleRunManifest: %v", err)
	}
	if m.Human.Gates != 2 {
		t.Errorf("gates = %d, want 2", m.Human.Gates)
	}
	if m.Human.Steering != 1 {
		t.Errorf("steering = %d, want 1", m.Human.Steering)
	}
}

func TestWriteRunManifestRoundTrips(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "r7")
	writeLog(t, runDir,
		`{"ts":"2026-01-01T00:00:01.000Z","source":"pipeline","type":"stage_started","node_id":"A","node_kind":"tool","attempt_no":1}`,
		`{"ts":"2026-01-01T00:00:02.000Z","source":"pipeline","type":"pipeline_completed","terminal_status":"success"}`,
	)
	// A spec on disk should be referenced by the manifest.
	if _, err := WriteSpecArtifacts(runDir, SpecArtifacts{
		Source: "workflow X\n  goal: \"x\"\n",
		Inputs: []SpecInput{{Name: "spec.md", Content: []byte("# spec")}},
	}); err != nil {
		t.Fatal(err)
	}

	written, err := WriteRunManifest(runDir, "r7")
	if err != nil {
		t.Fatalf("WriteRunManifest: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(runDir, RunManifestFile))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var loaded RunManifest
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if loaded.TerminalStatus != written.TerminalStatus || loaded.RunID != "r7" {
		t.Errorf("round trip lost fields: %+v", loaded)
	}
	if loaded.Spec.SourceFile != SpecSourceFile || loaded.Spec.SourceSHA256 == "" {
		t.Errorf("manifest does not reference the stored spec: %+v", loaded.Spec)
	}
	if len(loaded.Spec.Inputs) != 1 {
		t.Errorf("manifest lists %d inputs, want 1", len(loaded.Spec.Inputs))
	}
}

// TestAssembleRunManifestHandlesLargeLines guards the scanner buffer. Tool
// output and raw provider payloads make individual lines large, and the default
// 64KB token limit would turn them into parse failures.
func TestAssembleRunManifestHandlesLargeLines(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "r8")
	big := strings.Repeat("x", 300_000)
	writeLog(t, runDir,
		fmt.Sprintf(`{"ts":"2026-01-01T00:00:01.000Z","source":"agent","type":"tool_call_end","node_id":"A","tool_name":"bash","content":%q}`, big),
		`{"ts":"2026-01-01T00:00:02.000Z","source":"pipeline","type":"pipeline_completed","terminal_status":"success"}`,
	)
	m, err := AssembleRunManifest(runDir, "r8")
	if err != nil {
		t.Fatalf("AssembleRunManifest: %v", err)
	}
	if m.TerminalStatus != "success" {
		t.Errorf("terminal_status = %q — an oversized line broke the scan", m.TerminalStatus)
	}
}
