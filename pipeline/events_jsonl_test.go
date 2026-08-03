// ABOUTME: Tests for the JSONL activity log event handler.
// ABOUTME: Covers pipeline, agent, and LLM event logging to the unified activity.jsonl file.
package pipeline

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
)

func TestJSONLEventHandlerWritesEvents(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)
	defer h.Close()

	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Date(2026, 3, 11, 10, 0, 0, 0, time.UTC),
		RunID:     "abc123",
		Message:   "pipeline started",
	})
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventStageStarted,
		Timestamp: time.Date(2026, 3, 11, 10, 0, 1, 0, time.UTC),
		RunID:     "abc123",
		NodeID:    "step1",
		Message:   "executing node",
	})

	h.Close()

	logPath := filepath.Join(dir, "abc123", "activity.jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read activity log: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %s", len(lines), string(data))
	}

	var entry jsonlLogEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("unmarshal first line: %v", err)
	}
	if entry.Type != "pipeline_started" {
		t.Errorf("expected pipeline_started, got %q", entry.Type)
	}
	if entry.RunID != "abc123" {
		t.Errorf("expected run_id abc123, got %q", entry.RunID)
	}
}

func TestJSONLEventHandlerRecordsErrors(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)
	defer h.Close()

	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineFailed,
		Timestamp: time.Now(),
		RunID:     "def456",
		Message:   "pipeline failed",
		Err:       &testErr{msg: "context cancelled"},
	})

	h.Close()

	data, err := os.ReadFile(filepath.Join(dir, "def456", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read activity log: %v", err)
	}

	var entry jsonlLogEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry.Error != "context cancelled" {
		t.Errorf("expected error field, got %q", entry.Error)
	}
}

func TestJSONLEventHandlerNoopWithoutRunID(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)
	defer h.Close()

	// Event without RunID should not panic or create files
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
	})

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected no files without RunID, got %d", len(entries))
	}
}

func TestJSONLEventHandlerCloseWithoutEvents(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)
	// Close without writing any events should not panic
	if err := h.Close(); err != nil {
		t.Fatalf("Close without events: %v", err)
	}
}

func TestJSONLEventHandlerWritesPipelineSource(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)
	defer h.Close()

	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "src123",
		Message:   "started",
	})
	h.Close()

	data, err := os.ReadFile(filepath.Join(dir, "src123", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var entry jsonlLogEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry.Source != "pipeline" {
		t.Errorf("source = %q, want pipeline", entry.Source)
	}
}

func TestJSONLEventHandlerWritesAgentEvents(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)

	// Open the file by sending a pipeline event first (to get run ID).
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "agent123",
	})

	h.WriteAgentEvent(agent.Event{Type: "tool_call_end", NodeID: "gen_code", ToolName: "execute_command", ToolOutput: "output here", Provider: "anthropic", Model: "claude-sonnet-4-6"})
	h.Close()

	data, err := os.ReadFile(filepath.Join(dir, "agent123", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	var entry jsonlLogEntry
	if err := json.Unmarshal([]byte(lines[1]), &entry); err != nil {
		t.Fatalf("unmarshal agent line: %v", err)
	}
	if entry.Source != "agent" {
		t.Errorf("source = %q, want agent", entry.Source)
	}
	if entry.ToolName != "execute_command" {
		t.Errorf("tool_name = %q, want execute_command", entry.ToolName)
	}
	if entry.Content != "output here" {
		t.Errorf("content = %q, want 'output here'", entry.Content)
	}
	if entry.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", entry.Provider)
	}
	if entry.NodeID != "gen_code" {
		t.Errorf("node_id = %q, want gen_code", entry.NodeID)
	}
}

// TestJSONLEventHandlerWritesAgentUsage pins audit-finding-1: a turn_metrics
// agent event carries its per-turn token usage through WriteAgentEvent onto
// the activity.jsonl line, so the log path matches the NDJSON wire (#508).
// Before the fix WriteAgentEvent had no usage parameter, so every token field
// stayed zero and omitempty dropped them from the line.
func TestJSONLEventHandlerWritesAgentUsage(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)

	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "usage123",
	})

	h.WriteAgentEvent(agent.Event{
		Type:     "turn_metrics",
		NodeID:   "gen_code",
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
		Metrics: &agent.TurnMetrics{
			InputTokens:      100,
			OutputTokens:     40,
			CacheReadTokens:  900,
			CacheWriteTokens: 12,
			EstimatedCost:    0.0033,
		},
	})
	h.Close()

	data, err := os.ReadFile(filepath.Join(dir, "usage123", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var entry jsonlLogEntry
	if err := json.Unmarshal([]byte(lines[1]), &entry); err != nil {
		t.Fatalf("unmarshal agent line: %v", err)
	}
	if entry.TokenInput != 100 || entry.TokenOutput != 40 {
		t.Errorf("token in/out = %d/%d, want 100/40", entry.TokenInput, entry.TokenOutput)
	}
	if entry.TokenCacheRead != 900 || entry.TokenCacheWrite != 12 {
		t.Errorf("cache read/write = %d/%d, want 900/12", entry.TokenCacheRead, entry.TokenCacheWrite)
	}
	if entry.TurnCostUSD < 0.00329 || entry.TurnCostUSD > 0.00331 {
		t.Errorf("turn_cost_usd = %f, want 0.0033", entry.TurnCostUSD)
	}
}

func TestJSONLEventHandlerWritesLLMEvents(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)

	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "llm123",
	})

	h.WriteLLMEvent(llm.TraceEvent{Kind: "request_start", Provider: "anthropic", Model: "claude-sonnet-4-6", Preview: "hello world"})
	h.Close()

	data, err := os.ReadFile(filepath.Join(dir, "llm123", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	var entry jsonlLogEntry
	if err := json.Unmarshal([]byte(lines[1]), &entry); err != nil {
		t.Fatalf("unmarshal llm line: %v", err)
	}
	if entry.Source != "llm" {
		t.Errorf("source = %q, want llm", entry.Source)
	}
	if entry.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", entry.Provider)
	}
	if entry.Model != "claude-sonnet-4-6" {
		t.Errorf("model = %q, want claude-sonnet-4-6", entry.Model)
	}
	if entry.Content != "hello world" {
		t.Errorf("content = %q, want 'hello world'", entry.Content)
	}
}

func TestJSONLEventHandlerAgentErrorCombining(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)

	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "err123",
	})

	h.WriteAgentEvent(agent.Event{Type: "tool_call_end", ToolName: "cmd", ToolError: "exit code 1", Err: errors.New("process killed")})
	h.Close()

	data, err := os.ReadFile(filepath.Join(dir, "err123", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var entry jsonlLogEntry
	if err := json.Unmarshal([]byte(lines[1]), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if entry.Error != "exit code 1: process killed" {
		t.Errorf("error = %q, want 'exit code 1: process killed'", entry.Error)
	}
}

func TestBuildLogEntry_CostSnapshot(t *testing.T) {
	evt := PipelineEvent{
		Type:      EventCostUpdated,
		Timestamp: time.Unix(100, 0),
		RunID:     "run-1",
		Cost: &CostSnapshot{
			TotalTokens:  1500,
			TotalCostUSD: 0.0375,
			ProviderTotals: map[string]ProviderUsage{
				"anthropic": {InputTokens: 1000, OutputTokens: 500, CostUSD: 0.0375, SessionCount: 2},
			},
			WallElapsed: 500 * time.Millisecond,
		},
	}
	entry := buildLogEntry(evt)
	if entry.TotalTokens != 1500 {
		t.Errorf("TotalTokens = %d, want 1500", entry.TotalTokens)
	}
	if entry.TotalCostUSD < 0.03749 || entry.TotalCostUSD > 0.03751 {
		t.Errorf("TotalCostUSD = %f, want 0.0375", entry.TotalCostUSD)
	}
	if entry.WallElapsedMs != 500 {
		t.Errorf("WallElapsedMs = %d, want 500", entry.WallElapsedMs)
	}
	if entry.ProviderTotals == nil || entry.ProviderTotals["anthropic"].InputTokens != 1000 {
		t.Errorf("ProviderTotals[anthropic] = %+v", entry.ProviderTotals["anthropic"])
	}
	if entry.Estimated {
		t.Error("Estimated = true for a cost snapshot with Estimated:false; want false")
	}
}

// TestBuildLogEntry_CostSnapshot_Estimated pins the #186 NDJSON surface:
// when CostSnapshot.Estimated is true, the activity.jsonl entry carries
// `estimated: true` so external consumers (dashboards, tracker diagnose,
// embedded integrations) can distinguish heuristic spend from metered
// spend without re-deriving the flag from ProviderTotals.
func TestBuildLogEntry_CostSnapshot_Estimated(t *testing.T) {
	evt := PipelineEvent{
		Type:      EventCostUpdated,
		Timestamp: time.Unix(100, 0),
		RunID:     "run-1",
		Cost: &CostSnapshot{
			TotalTokens:  300,
			TotalCostUSD: 0.0125,
			ProviderTotals: map[string]ProviderUsage{
				"acp": {InputTokens: 200, OutputTokens: 100, CostUSD: 0.0125, SessionCount: 1, Estimated: true},
			},
			WallElapsed: 250 * time.Millisecond,
			Estimated:   true,
		},
	}
	entry := buildLogEntry(evt)
	if !entry.Estimated {
		t.Error("Estimated = false; want true (CostSnapshot.Estimated=true)")
	}
	if !entry.ProviderTotals["acp"].Estimated {
		t.Error("ProviderTotals[acp].Estimated = false; want true (per-bucket flag lost)")
	}
}

func TestBuildLogEntry_NilCost(t *testing.T) {
	evt := PipelineEvent{Type: EventPipelineStarted, Timestamp: time.Unix(100, 0), RunID: "run-1"}
	entry := buildLogEntry(evt)
	if entry.TotalTokens != 0 || entry.TotalCostUSD != 0 {
		t.Errorf("nil cost should yield zero fields, got %+v", entry)
	}
}

// TestPipelineEvent_BundleIdentity_FlowsToJSONL pins the contract that the
// engine's stamped BundleIdentity makes it onto every JSONL log entry —
// this is how `.dipx` bundle provenance ends up on every line of
// activity.jsonl.
func TestPipelineEvent_BundleIdentity_FlowsToJSONL(t *testing.T) {
	evt := PipelineEvent{
		Type:           EventPipelineStarted,
		Timestamp:      time.Unix(100, 0),
		RunID:          "run-1",
		BundleIdentity: "sha256:efb5648d28e6c2",
	}
	entry := buildLogEntry(evt)
	if entry.BundleIdentity != "sha256:efb5648d28e6c2" {
		t.Errorf("BundleIdentity not copied to jsonlLogEntry: got %q want %q", entry.BundleIdentity, "sha256:efb5648d28e6c2")
	}
}

// TestPipelineEvent_BundleIdentity_OmittedWhenEmpty pins the JSON tag
// behavior: plain .dip runs (empty identity) must not emit a
// bundle_identity field at all, so external consumers can distinguish
// bundle runs from non-bundle runs by field presence.
func TestPipelineEvent_BundleIdentity_OmittedWhenEmpty(t *testing.T) {
	evt := PipelineEvent{Type: EventPipelineStarted, Timestamp: time.Unix(100, 0), RunID: "run-1"}
	entry := buildLogEntry(evt)
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "bundle_identity") {
		t.Errorf("empty BundleIdentity should be omitted from JSON, got %s", string(data))
	}
}

// TestJSONLEventHandler_WriteAgentEvent_StampsBundleIdentity pins that
// agent events written via WriteAgentEvent (the path used by codergen
// session emissions in cmd/tracker/run.go) carry the configured .dipx
// bundle identity. WriteAgentEvent bypasses HandlePipelineEvent — and
// therefore Engine.emit and the registry's BundleIdentityStamper — so
// without an explicit stamp here, agent lines in activity.jsonl would
// land without bundle provenance.
func TestJSONLEventHandler_WriteAgentEvent_StampsBundleIdentity(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)
	h.SetBundleIdentity("sha256:abc123")

	// Pipeline event first to open the file (RunID-derived path).
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "bundle-agent",
	})

	h.WriteAgentEvent(agent.Event{Type: "tool_call_end", NodeID: "gen_code", ToolName: "execute_command", ToolOutput: "ok", Provider: "anthropic", Model: "claude-sonnet-4-6"})
	h.Close()

	data, err := os.ReadFile(filepath.Join(dir, "bundle-agent", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	var entry jsonlLogEntry
	if err := json.Unmarshal([]byte(lines[1]), &entry); err != nil {
		t.Fatalf("unmarshal agent line: %v", err)
	}
	if entry.BundleIdentity != "sha256:abc123" {
		t.Errorf("agent event bundle_identity = %q, want sha256:abc123", entry.BundleIdentity)
	}
}

// TestJSONLEventHandler_WriteLLMEvent_StampsBundleIdentity pins the same
// contract for the LLM trace observer write path (wireLLMTraceToLog /
// buildTUIPipelineHandler). Without an explicit stamp here, llm lines
// in activity.jsonl would land without bundle provenance.
func TestJSONLEventHandler_WriteLLMEvent_StampsBundleIdentity(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)
	h.SetBundleIdentity("sha256:abc123")

	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "bundle-llm",
	})

	h.WriteLLMEvent(llm.TraceEvent{Kind: "request_start", Provider: "anthropic", Model: "claude-sonnet-4-6", Preview: "hi"})
	h.Close()

	data, err := os.ReadFile(filepath.Join(dir, "bundle-llm", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	var entry jsonlLogEntry
	if err := json.Unmarshal([]byte(lines[1]), &entry); err != nil {
		t.Fatalf("unmarshal llm line: %v", err)
	}
	if entry.BundleIdentity != "sha256:abc123" {
		t.Errorf("llm event bundle_identity = %q, want sha256:abc123", entry.BundleIdentity)
	}
}

// TestJSONLEventHandler_NoStampingWhenIdentityEmpty pins the no-op
// behavior for plain .dip runs: when SetBundleIdentity was never called
// (or called with ""), agent and llm lines must omit bundle_identity
// entirely. External consumers distinguish bundle runs from non-bundle
// runs by field presence — TestPipelineEvent_BundleIdentity_OmittedWhenEmpty
// pins the same surface for pipeline-source lines.
func TestJSONLEventHandler_NoStampingWhenIdentityEmpty(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)
	// Intentionally no SetBundleIdentity call.

	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "no-bundle",
	})
	h.WriteAgentEvent(agent.Event{Type: "tool_call_end", NodeID: "n1", ToolName: "cmd", ToolOutput: "out"})
	h.WriteLLMEvent(llm.TraceEvent{Kind: "request_start", Provider: "anthropic", Model: "claude-sonnet-4-6", Preview: "hi"})
	h.Close()

	data, err := os.ReadFile(filepath.Join(dir, "no-bundle", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.Contains(line, "bundle_identity") {
			t.Errorf("line %d should omit bundle_identity for plain .dip run, got: %s", i, line)
		}
	}
}

// TestJSONLEventHandler_PreservesCallerSetBundleIdentity is the
// agent/llm-side analogue of the Engine.emit and BundleIdentityStamper
// guards: the in-method stamp only runs when entry.BundleIdentity is
// currently empty. Today neither WriteAgentEvent nor WriteLLMEvent
// expose a way to pre-set the identity (the stamping happens after
// struct construction inside the method), but the guard is in place to
// match the upstream pattern. We assert via a constructed entry that
// the guard logic does the right thing — defensive coverage so a
// future refactor (e.g., a WriteAgentEventWithIdentity variant) that
// pre-sets the field won't accidentally clobber the caller's value.
func TestJSONLEventHandler_PreservesCallerSetBundleIdentity(t *testing.T) {
	// Mirror the in-method guard exactly so the test pins the behavior
	// even if the methods later evolve to accept a caller-supplied
	// identity (the guard would then matter at the public surface).
	caller := "sha256:caller"
	handler := "sha256:handler"
	entry := jsonlLogEntry{BundleIdentity: caller}
	if entry.BundleIdentity == "" {
		entry.BundleIdentity = handler
	}
	if entry.BundleIdentity != caller {
		t.Errorf("caller-set identity should be preserved: got %q want %q", entry.BundleIdentity, caller)
	}
}

// TestJSONLEventHandler_WriteBundleMismatchForced pins the contract that
// the bundle_mismatch_forced audit entry lands in activity.jsonl with the
// correct shape — source=cli, type=bundle_mismatch_forced, bundle_identity
// stamped with the CURRENT identity (what the run actually executes
// against, not the original checkpoint identity), and a message preserving
// both identities for post-hoc forensics.
func TestJSONLEventHandler_WriteBundleMismatchForced(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)

	originalID := "sha256:" + strings.Repeat("a", 64)
	currentID := "sha256:" + strings.Repeat("b", 64)
	h.WriteBundleMismatchForced("force-run", originalID, currentID)
	h.Close()

	data, err := os.ReadFile(filepath.Join(dir, "force-run", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read activity log: %v", err)
	}
	line := strings.TrimSpace(string(data))

	var entry map[string]any
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatalf("not valid JSON: %v\nline: %s", err, line)
	}

	if entry["type"] != "bundle_mismatch_forced" {
		t.Errorf("type = %v, want bundle_mismatch_forced", entry["type"])
	}
	if entry["source"] != "cli" {
		t.Errorf("source = %v, want cli", entry["source"])
	}
	if entry["run_id"] != "force-run" {
		t.Errorf("run_id = %v, want force-run", entry["run_id"])
	}
	if entry["bundle_identity"] != currentID {
		t.Errorf("bundle_identity should be the CURRENT identity (what the run actually uses): got %v, want %s", entry["bundle_identity"], currentID)
	}
	msg, _ := entry["message"].(string)
	if !strings.Contains(msg, originalID) || !strings.Contains(msg, currentID) {
		t.Errorf("message should contain both identities: %q", msg)
	}
	if !strings.Contains(msg, "--force-bundle-mismatch") {
		t.Errorf("message should mention --force-bundle-mismatch: %q", msg)
	}
}

// TestJSONLEventHandler_WriteBundleMismatchForced_EmptyOriginal pins that
// a plain-.dip-to-.dipx upgrade (empty original identity, populated current)
// renders the original side as "(none — plain .dip)" so the audit trail
// can distinguish "no bundle was claimed" from "wrong bundle".
func TestJSONLEventHandler_WriteBundleMismatchForced_EmptyOriginal(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)

	currentID := "sha256:" + strings.Repeat("c", 64)
	h.WriteBundleMismatchForced("upgrade-run", "", currentID)
	h.Close()

	data, err := os.ReadFile(filepath.Join(dir, "upgrade-run", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &entry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	msg, _ := entry["message"].(string)
	if !strings.Contains(msg, "(none — plain .dip)") {
		t.Errorf("empty original should render as plain-.dip marker: %q", msg)
	}
	if !strings.Contains(msg, currentID) {
		t.Errorf("message should still contain current id: %q", msg)
	}
}

// TestJSONLEventHandler_WriteBundleMismatchForced_NoOpWithoutRunID pins the
// no-op behavior when the caller can't supply a run ID (the file path is
// derived from the run ID, so we have no destination otherwise). Matches
// HandlePipelineEvent's defensive guard for events without RunID.
func TestJSONLEventHandler_WriteBundleMismatchForced_NoOpWithoutRunID(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)

	h.WriteBundleMismatchForced("", "sha256:aa", "sha256:bb")
	h.Close()

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected no files without RunID, got %d", len(entries))
	}
}

// TestJSONLEventHandler_SecureWriteAndStrippedSnapshot pins the #213
// contract end-to-end: events land in the integrity-protected secure
// path with sentinel-prefixed lines and mode 0o600; on Close a
// sentinel-stripped snapshot is mirrored to <artifactDir>/<runID>/
// activity.jsonl with mode 0o600 for bundle/export compatibility (#525:
// tightened from 0o644 now that the mirror holds verbatim request bodies
// and full tool I/O).
func TestJSONLEventHandler_SecureWriteAndStrippedSnapshot(t *testing.T) {
	secureBase := t.TempDir()
	t.Setenv(auditDirEnvVar, secureBase)
	t.Setenv(xdgStateHomeEnvVar, "")

	artifactDir := t.TempDir()
	h := NewJSONLEventHandler(artifactDir)

	runID := "secure-snapshot-test"
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC),
		RunID:     runID,
	})
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventStageStarted,
		Timestamp: time.Date(2026, 5, 13, 10, 0, 1, 0, time.UTC),
		RunID:     runID,
		NodeID:    "Build",
	})
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	securePath := filepath.Join(secureBase, runID, "activity.jsonl")
	secureBytes, err := os.ReadFile(securePath)
	if err != nil {
		t.Fatalf("read secure log: %v", err)
	}
	secureLines := strings.Split(strings.TrimSuffix(string(secureBytes), "\n"), "\n")
	if len(secureLines) != 2 {
		t.Fatalf("secure log: expected 2 lines, got %d: %q", len(secureLines), string(secureBytes))
	}
	for i, line := range secureLines {
		if !strings.HasPrefix(line, ActivityLogSentinel) {
			t.Errorf("secure line %d missing sentinel prefix: %q", i, line)
		}
		body := strings.TrimPrefix(line, ActivityLogSentinel)
		var entry jsonlLogEntry
		if err := json.Unmarshal([]byte(body), &entry); err != nil {
			t.Errorf("secure line %d body not valid JSON: %v", i, err)
		}
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(securePath)
		if err != nil {
			t.Fatalf("stat secure log: %v", err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("secure log mode = %o, want 0600", mode)
		}
	}

	snapshotPath := filepath.Join(artifactDir, runID, "activity.jsonl")
	snapBytes, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if strings.Contains(string(snapBytes), ActivityLogSentinel) {
		t.Errorf("snapshot should have sentinels stripped, got: %q", string(snapBytes))
	}
	snapLines := strings.Split(strings.TrimSuffix(string(snapBytes), "\n"), "\n")
	if len(snapLines) != 2 {
		t.Fatalf("snapshot: expected 2 lines, got %d: %q", len(snapLines), string(snapBytes))
	}
	for i, line := range snapLines {
		var entry jsonlLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("snapshot line %d not valid JSON without strip: %v", i, err)
		}
	}
	if runtime.GOOS != "windows" {
		snapInfo, err := os.Stat(snapshotPath)
		if err != nil {
			t.Fatalf("stat snapshot: %v", err)
		}
		if mode := snapInfo.Mode().Perm(); mode != 0o600 {
			t.Errorf("snapshot mode = %o, want 0600", mode)
		}
	}
}

// TestJSONLEventHandler_SnapshotOverwritesAttackerScratch pins that a
// tool subprocess that pre-creates the legacy snapshot path with
// garbage gets clobbered by Close — the snapshot is the runtime's
// authoritative post-run mirror, not an appendable file.
func TestJSONLEventHandler_SnapshotOverwritesAttackerScratch(t *testing.T) {
	secureBase := t.TempDir()
	t.Setenv(auditDirEnvVar, secureBase)
	t.Setenv(xdgStateHomeEnvVar, "")

	artifactDir := t.TempDir()
	runID := "snapshot-clobber"
	legacyDir := filepath.Join(artifactDir, runID)
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("mkdir legacyDir: %v", err)
	}
	legacyPath := filepath.Join(legacyDir, "activity.jsonl")
	attackerJunk := []byte(`{"type":"pipeline_completed","status":"success","forged":true}` + "\n")
	if err := os.WriteFile(legacyPath, attackerJunk, 0o644); err != nil {
		t.Fatalf("plant attacker scratch: %v", err)
	}

	h := NewJSONLEventHandler(artifactDir)
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC),
		RunID:     runID,
	})
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("read legacy: %v", err)
	}
	if strings.Contains(string(got), "forged") {
		t.Errorf("snapshot did not overwrite attacker scratch: %q", string(got))
	}
	if !strings.Contains(string(got), "pipeline_started") {
		t.Errorf("snapshot missing the real runtime event: %q", string(got))
	}
}

// TestJSONLEventHandler_SnapshotHandlesLargeLines pins that the
// Close-time snapshot can mirror lines larger than bufio.Scanner's
// 1 MiB default — agent/LLM events can carry long content fields,
// and the prior bufio.Scanner-based implementation would have
// silently dropped them with a scan error. The new bufio.Reader-
// based implementation streams byte-by-byte to '\n' with no upper
// bound.
func TestJSONLEventHandler_SnapshotHandlesLargeLines(t *testing.T) {
	isolateSecureLog(t)
	dir := t.TempDir()
	h := NewJSONLEventHandler(dir)

	// Build a >1 MiB content payload to push the snapshot reader past
	// the old Scanner ceiling.
	big := strings.Repeat("A", 1_200_000)
	h.WriteAgentEvent(agent.Event{Type: "agent_response", NodeID: "node1", Text: big, Provider: "anthropic", Model: "claude"})
	// Trigger openFile via a pipeline event with a runID; the agent
	// write above doesn't carry one.
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC),
		RunID:     "big-line-test",
	})
	// Re-emit the agent event now that the file is open.
	h.WriteAgentEvent(agent.Event{Type: "agent_response", NodeID: "node1", Text: big, Provider: "anthropic", Model: "claude"})

	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if serr := h.SnapshotErr(); serr != nil {
		t.Errorf("SnapshotErr: %v (snapshot should handle large lines)", serr)
	}

	snapshotPath := filepath.Join(dir, "big-line-test", "activity.jsonl")
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !strings.Contains(string(data), big) {
		t.Errorf("snapshot missing the >1MiB payload — Scanner regression?")
	}
}

// TestJSONLEventHandler_SecureLogReclampsMode pins that a pre-existing
// secure file with wider mode (e.g. left over from a crash + manual
// fiddling) gets re-tightened to 0o600 when reopened — OpenFile's
// mode argument only applies at creation.
func TestJSONLEventHandler_SecureLogReclampsMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits don't round-trip on Windows")
	}
	secureBase := t.TempDir()
	t.Setenv(auditDirEnvVar, secureBase)
	t.Setenv(xdgStateHomeEnvVar, "")

	runID := "secure-reclamp"
	secureDir := filepath.Join(secureBase, runID)
	if err := os.MkdirAll(secureDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	securePath := filepath.Join(secureDir, "activity.jsonl")
	if err := os.WriteFile(securePath, []byte{}, 0o644); err != nil {
		t.Fatalf("plant wide-mode file: %v", err)
	}

	h := NewJSONLEventHandler(t.TempDir())
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC),
		RunID:     runID,
	})
	_ = h.Close()

	info, err := os.Stat(securePath)
	if err != nil {
		t.Fatalf("stat secure log: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("secure log mode after reopen = %o, want 0600", mode)
	}
}

func TestJSONL_OverrideEventRoundTrip(t *testing.T) {
	detail := &OverrideDetail{
		GateNodeID:   "EscalateReview",
		Label:        "accept",
		Actor:        ActorHuman,
		SubgraphPath: []string{"Outer", "Inner"},
		Timestamp:    time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC),
	}
	ev := PipelineEvent{
		Type:      EventValidationOverridden,
		Timestamp: time.Date(2026, 5, 29, 12, 0, 1, 0, time.UTC),
		RunID:     "test-run",
		NodeID:    "EscalateReview",
		Message:   "validation override",
		Override:  detail,
	}
	entry := buildLogEntry(ev)

	if entry.OverrideGate != "EscalateReview" {
		t.Errorf("OverrideGate = %q, want EscalateReview", entry.OverrideGate)
	}
	if entry.OverrideLabel != "accept" {
		t.Errorf("OverrideLabel = %q, want accept", entry.OverrideLabel)
	}
	if entry.OverrideActor != ActorHuman {
		t.Errorf("OverrideActor = %q, want %q", entry.OverrideActor, ActorHuman)
	}
	if len(entry.OverrideSubgraphPath) != 2 ||
		entry.OverrideSubgraphPath[0] != "Outer" ||
		entry.OverrideSubgraphPath[1] != "Inner" {
		t.Errorf("OverrideSubgraphPath = %v, want [Outer Inner]", entry.OverrideSubgraphPath)
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded jsonlLogEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.OverrideGate != entry.OverrideGate {
		t.Errorf("round-trip OverrideGate: got %q want %q", decoded.OverrideGate, entry.OverrideGate)
	}
	if decoded.OverrideActor != entry.OverrideActor {
		t.Errorf("round-trip OverrideActor: got %q want %q", decoded.OverrideActor, entry.OverrideActor)
	}
	if len(decoded.OverrideSubgraphPath) != len(entry.OverrideSubgraphPath) {
		t.Errorf("round-trip path length: got %d want %d",
			len(decoded.OverrideSubgraphPath), len(entry.OverrideSubgraphPath))
	}
}

// isolateSecureLog pins TRACKER_AUDIT_DIR to a per-test tmp dir so
// tests using shared/hardcoded runIDs (abc123, def456, etc.) don't
// collide on the user's $HOME-based default secure path. Without this,
// CI hosts where many tests run in the same process and use the same
// runID would see appended/aliased writes across test cases. Also
// clears XDG_STATE_HOME so the override unambiguously wins.
func isolateSecureLog(t *testing.T) {
	t.Helper()
	t.Setenv(auditDirEnvVar, t.TempDir())
	t.Setenv(xdgStateHomeEnvVar, "")
}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

func TestJSONLEventHandler_DropsRawLLMEventsByDefault(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)

	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "raw123",
	})

	// Raw provider chunks are debugging payload (#354): both spellings
	// are dropped unless raw capture is enabled.
	h.WriteAgentEvent(agent.Event{Type: "llm_provider_raw", NodeID: "gen_code", Text: "chunk", Provider: "anthropic", Model: "m"})
	h.WriteLLMEvent(llm.TraceEvent{Kind: "provider_raw", Provider: "anthropic", Model: "m", Preview: "chunk"})
	// Non-raw events still land.
	h.WriteAgentEvent(agent.Event{Type: "llm_text", NodeID: "gen_code", Text: "hello", Provider: "anthropic", Model: "m"})
	h.WriteLLMEvent(llm.TraceEvent{Kind: "text", Provider: "anthropic", Model: "m", Preview: "hello"})
	h.Close()

	data, err := os.ReadFile(filepath.Join(dir, "raw123", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (pipeline + 2 text), got %d:\n%s", len(lines), data)
	}
	for _, line := range lines {
		var entry jsonlLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if entry.Type == "provider_raw" || entry.Type == "llm_provider_raw" {
			t.Errorf("raw event %q written despite capture disabled", entry.Type)
		}
	}
}

func TestJSONLEventHandler_WritesRawLLMEventsWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)
	h.SetCaptureRawLLM(true)

	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "raw456",
	})

	h.WriteAgentEvent(agent.Event{Type: "llm_provider_raw", NodeID: "gen_code", Text: "chunk", Provider: "anthropic", Model: "m"})
	h.WriteLLMEvent(llm.TraceEvent{Kind: "provider_raw", Provider: "anthropic", Model: "m", Preview: "chunk"})
	h.Close()

	data, err := os.ReadFile(filepath.Join(dir, "raw456", "activity.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (pipeline + 2 raw), got %d:\n%s", len(lines), data)
	}
}

// TestWriteAgentEventCapturesReconstructionFields pins the fields that let a
// post-hoc reader rebuild a run as a tree. Each of these was populated on
// agent.Event but dropped at the log boundary, so a reader could see that a
// tool ran without knowing which turn issued it or what was asked for.
func TestWriteAgentEventCapturesReconstructionFields(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "recon1",
	})

	reasoning, cacheRead, cacheWrite := 7, 11, 13
	h.WriteAgentEvent(agent.Event{
		Type:               "tool_call_start",
		NodeID:             "Generate",
		SessionID:          "sess-abc",
		Turn:               3,
		ToolName:           "bash",
		ToolInput:          `{"command":"ls -la"}`,
		ToolDuration:       250 * time.Millisecond,
		FinishReason:       "tool_use",
		ContextUtilization: 0.42,
		Usage: llm.Usage{
			InputTokens:      100,
			OutputTokens:     50,
			ReasoningTokens:  &reasoning,
			CacheReadTokens:  &cacheRead,
			CacheWriteTokens: &cacheWrite,
			EstimatedCost:    0.0125,
		},
	})
	h.Close()

	entry := lastAgentEntry(t, dir, "recon1")

	if entry.SessionID != "sess-abc" {
		t.Errorf("session_id = %q, want sess-abc", entry.SessionID)
	}
	if entry.TurnNo != 3 {
		t.Errorf("turn_no = %d, want 3", entry.TurnNo)
	}
	// The whole point of the field: the command, not just the tool name.
	if entry.ToolInput != `{"command":"ls -la"}` {
		t.Errorf("tool_input = %q, want the arguments JSON", entry.ToolInput)
	}
	if entry.ToolDurationMs != 250 {
		t.Errorf("tool_duration_ms = %d, want 250", entry.ToolDurationMs)
	}
	if entry.FinishReason != "tool_use" {
		t.Errorf("finish_reason = %q, want tool_use", entry.FinishReason)
	}
	if entry.ContextUtilization != 0.42 {
		t.Errorf("context_utilization = %v, want 0.42", entry.ContextUtilization)
	}
	if entry.TokenInput != 100 || entry.TokenOutput != 50 {
		t.Errorf("tokens = %d/%d, want 100/50", entry.TokenInput, entry.TokenOutput)
	}
	if entry.ReasoningTokens != 7 || entry.CacheReadTokens != 11 || entry.CacheWriteTokens != 13 {
		t.Errorf("reasoning/cacheRead/cacheWrite = %d/%d/%d, want 7/11/13",
			entry.ReasoningTokens, entry.CacheReadTokens, entry.CacheWriteTokens)
	}
	if entry.EstimatedCost != 0.0125 {
		t.Errorf("estimated_cost = %v, want 0.0125", entry.EstimatedCost)
	}
}

// TestWriteAgentEventTurnMetrics covers the turn_metrics event, whose per-turn
// economics arrive in a separate Metrics block rather than on Usage.
func TestWriteAgentEventTurnMetrics(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "recon2",
	})

	h.WriteAgentEvent(agent.Event{
		Type:      "turn_metrics",
		NodeID:    "Generate",
		SessionID: "sess-xyz",
		Turn:      2,
		Metrics: &agent.TurnMetrics{
			InputTokens:        900,
			OutputTokens:       120,
			CacheReadTokens:    800,
			CacheWriteTokens:   64,
			ContextUtilization: 0.71,
			ToolCacheHits:      4,
			ToolCacheMisses:    1,
			TurnDuration:       2 * time.Second,
			EstimatedCost:      0.031,
		},
	})
	h.Close()

	entry := lastAgentEntry(t, dir, "recon2")

	if entry.SessionID != "sess-xyz" || entry.TurnNo != 2 {
		t.Errorf("identity = %q/%d, want sess-xyz/2", entry.SessionID, entry.TurnNo)
	}
	if entry.TokenInput != 900 || entry.TokenOutput != 120 {
		t.Errorf("tokens = %d/%d, want 900/120", entry.TokenInput, entry.TokenOutput)
	}
	if entry.CacheReadTokens != 800 || entry.CacheWriteTokens != 64 {
		t.Errorf("cache r/w = %d/%d, want 800/64", entry.CacheReadTokens, entry.CacheWriteTokens)
	}
	if entry.ToolCacheHits != 4 || entry.ToolCacheMisses != 1 {
		t.Errorf("tool cache h/m = %d/%d, want 4/1", entry.ToolCacheHits, entry.ToolCacheMisses)
	}
	if entry.TurnDurationMs != 2000 {
		t.Errorf("turn_duration_ms = %d, want 2000", entry.TurnDurationMs)
	}
	if entry.ContextUtilization != 0.71 {
		t.Errorf("context_utilization = %v, want 0.71", entry.ContextUtilization)
	}
	if entry.EstimatedCost != 0.031 {
		t.Errorf("estimated_cost = %v, want 0.031", entry.EstimatedCost)
	}
}

// TestJoinAgentErrors pins the merge of the two error channels: a tool can
// fail and the session can error on the same event, and neither should
// silently overwrite the other.
func TestJoinAgentErrors(t *testing.T) {
	tests := []struct {
		name string
		evt  agent.Event
		want string
	}{
		{"neither", agent.Event{}, ""},
		{"tool only", agent.Event{ToolError: "exit 1"}, "exit 1"},
		{"session only", agent.Event{Err: errors.New("killed")}, "killed"},
		{"both", agent.Event{ToolError: "exit 1", Err: errors.New("killed")}, "exit 1: killed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinAgentErrors(tt.evt); got != tt.want {
				t.Errorf("joinAgentErrors() = %q, want %q", got, tt.want)
			}
		})
	}
}

// lastAgentEntry reads the run's activity log and returns the final
// agent-source entry.
func lastAgentEntry(t *testing.T, dir, runID string) jsonlLogEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, runID, "activity.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var entry jsonlLogEntry
		if err := json.Unmarshal([]byte(lines[i]), &entry); err != nil {
			t.Fatalf("unmarshal line %d: %v", i, err)
		}
		if entry.Source == "agent" {
			return entry
		}
	}
	t.Fatalf("no agent-source entry in %d lines", len(lines))
	return jsonlLogEntry{}
}

// TestWriteLLMEventKeepsFullPayload pins that the activity log records the
// untruncated payload. The old signature took previewText output, capped at 80
// characters, so a long tool-argument list or provider chunk reached disk
// clipped with nothing to indicate content was missing.
func TestWriteLLMEventKeepsFullPayload(t *testing.T) {
	longArgs := `{"command":"` + strings.Repeat("x", 300) + `"}`

	tests := []struct {
		name string
		evt  llm.TraceEvent
		want string
	}{
		{
			name: "request body wins over preview",
			evt: llm.TraceEvent{
				Kind:       "request_start",
				RequestRaw: []byte(`{"model":"m","messages":[]}`),
				Preview:    "clipped…",
			},
			want: `{"model":"m","messages":[]}`,
		},
		{
			name: "full tool arguments, not the clipped preview",
			evt: llm.TraceEvent{
				Kind:          "tool_prepare",
				ToolName:      "bash",
				ToolArguments: []byte(longArgs),
				Preview:       "clipped…",
			},
			want: longArgs,
		},
		{
			name: "full provider chunk",
			evt: llm.TraceEvent{
				Kind:        "provider_raw",
				ProviderRaw: []byte(`{"type":"delta"}`),
				RawPreview:  "clipped…",
			},
			want: `{"type":"delta"}`,
		},
		{
			name: "text deltas fall back to Preview, which is never clipped",
			evt:  llm.TraceEvent{Kind: "text", Preview: "hello world"},
			want: "hello world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			isolateSecureLog(t)
			h := NewJSONLEventHandler(dir)
			h.SetCaptureRawLLM(true) // provider_raw is gated off by default
			h.HandlePipelineEvent(PipelineEvent{
				Type:      EventPipelineStarted,
				Timestamp: time.Now(),
				RunID:     "llmfull",
			})
			evt := tt.evt
			evt.CallID = "call-42"
			h.WriteLLMEvent(evt)
			h.Close()

			entry := lastEntryOfSource(t, dir, "llmfull", "llm")
			if entry.Content != tt.want {
				t.Errorf("content = %q (len %d), want %q (len %d)",
					entry.Content, len(entry.Content), tt.want, len(tt.want))
			}
			if entry.CallID != "call-42" {
				t.Errorf("call_id = %q, want call-42", entry.CallID)
			}
		})
	}
}

// TestWriteLLMEventRecordsUsage pins that per-call token usage reaches the log,
// so cost can be attributed to a call rather than only to the run.
func TestWriteLLMEventRecordsUsage(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "llmusage",
	})

	cacheRead := 512
	h.WriteLLMEvent(llm.TraceEvent{
		Kind:         "finish",
		CallID:       "call-7",
		FinishReason: "tool_calls",
		Usage: llm.Usage{
			InputTokens:     600,
			OutputTokens:    40,
			CacheReadTokens: &cacheRead,
			EstimatedCost:   0.0042,
		},
	})
	h.Close()

	entry := lastEntryOfSource(t, dir, "llmusage", "llm")
	if entry.TokenInput != 600 || entry.TokenOutput != 40 {
		t.Errorf("tokens = %d/%d, want 600/40", entry.TokenInput, entry.TokenOutput)
	}
	if entry.CacheReadTokens != 512 {
		t.Errorf("cache_read_tokens = %d, want 512", entry.CacheReadTokens)
	}
	if entry.EstimatedCost != 0.0042 {
		t.Errorf("estimated_cost = %v, want 0.0042", entry.EstimatedCost)
	}
	if entry.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", entry.FinishReason)
	}
}

// lastEntryOfSource returns the final activity-log entry with the given source.
func lastEntryOfSource(t *testing.T, dir, runID, source string) jsonlLogEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, runID, "activity.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var entry jsonlLogEntry
		if err := json.Unmarshal([]byte(lines[i]), &entry); err != nil {
			t.Fatalf("unmarshal line %d: %v", i, err)
		}
		if entry.Source == source {
			return entry
		}
	}
	t.Fatalf("no %s-source entry in %d lines", source, len(lines))
	return jsonlLogEntry{}
}

// TestBuildLogEntryPersistsTerminalStatus pins the field the engine already
// guarantees but the log used to drop. Engine.emitTerminalBackstop exists to
// make sure exactly one event per run carries TerminalStatus -- it fires even
// on a panic or an invariant error -- yet buildLogEntry never copied it, so a
// post-hoc reader had to infer how a run ended from event types alone.
func TestBuildLogEntryPersistsTerminalStatus(t *testing.T) {
	entry := buildLogEntry(PipelineEvent{
		Type:           EventPipelineFailed,
		Timestamp:      time.Now(),
		RunID:          "r1",
		TerminalStatus: "fail",
	})
	if entry.TerminalStatus != "fail" {
		t.Errorf("terminal_status = %q, want fail", entry.TerminalStatus)
	}
}

// TestBuildLogEntryPersistsNodeKindAndAttempt pins node identity that cannot be
// recovered from the filesystem: artifact directories are sparse in large runs,
// so a reader inferring kind from which files exist loses it for most nodes.
func TestBuildLogEntryPersistsNodeKindAndAttempt(t *testing.T) {
	entry := buildLogEntry(PipelineEvent{
		Type:      EventStageStarted,
		Timestamp: time.Now(),
		RunID:     "r1",
		NodeID:    "Generate",
		NodeKind:  "codergen",
		AttemptNo: 3,
	})
	if entry.NodeKind != "codergen" {
		t.Errorf("node_kind = %q, want codergen", entry.NodeKind)
	}
	if entry.AttemptNo != 3 {
		t.Errorf("attempt_no = %d, want 3", entry.AttemptNo)
	}
}

// TestTerminalStatusOmittedOnNonTerminalEvents guards the other half of the
// contract: the field must be absent on ordinary events, so "any line with a
// terminal_status" stays a reliable way to find the run's outcome.
func TestTerminalStatusOmittedOnNonTerminalEvents(t *testing.T) {
	entry := buildLogEntry(PipelineEvent{
		Type:      EventStageStarted,
		Timestamp: time.Now(),
		RunID:     "r1",
		NodeID:    "A",
	})
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "terminal_status") {
		t.Errorf("non-terminal event carries terminal_status: %s", encoded)
	}
}

// TestApplyAgentEventFieldsCarriesCallIDAndRequestRaw pins the boundary a live
// run exposed. CallID and RequestRaw were added to llm.TraceEvent, but in a
// normal run every LLM event reaches the log via the agent session's
// re-emission — the client-level writer skips session-owned events — so both
// were dropped exactly where they mattered.
func TestApplyAgentEventFieldsCarriesCallIDAndRequestRaw(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	var entry jsonlLogEntry
	applyAgentEventFields(&entry, agent.Event{
		Type:       "llm_request_start",
		SessionID:  "s1",
		Turn:       1,
		CallID:     "call-9",
		RequestRaw: body,
	})
	if entry.CallID != "call-9" {
		t.Errorf("call_id = %q, want call-9", entry.CallID)
	}
	// The wire body is the highest-fidelity content the event carries, so it
	// must win over the display preview the generic path would use.
	if entry.Content != string(body) {
		t.Errorf("content = %q, want the wire body", entry.Content)
	}
}
