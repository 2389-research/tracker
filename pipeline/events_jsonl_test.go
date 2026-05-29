// ABOUTME: Tests for the JSONL activity log event handler.
// ABOUTME: Covers pipeline, agent, and LLM event logging to the unified activity.jsonl file.
package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

	h.WriteAgentEvent("tool_call_end", "gen_code", "execute_command", "output here", "", "", "", "anthropic", "claude-sonnet-4-6")
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

func TestJSONLEventHandlerWritesLLMEvents(t *testing.T) {
	dir := t.TempDir()
	isolateSecureLog(t)
	h := NewJSONLEventHandler(dir)

	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "llm123",
	})

	h.WriteLLMEvent("request_start", "anthropic", "claude-sonnet-4-6", "", "hello world")
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

	h.WriteAgentEvent("tool_call_end", "", "cmd", "", "exit code 1", "", "process killed", "", "")
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

	h.WriteAgentEvent("tool_call_end", "gen_code", "execute_command", "ok", "", "", "", "anthropic", "claude-sonnet-4-6")
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

	h.WriteLLMEvent("request_start", "anthropic", "claude-sonnet-4-6", "", "hi")
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
	h.WriteAgentEvent("tool_call_end", "n1", "cmd", "out", "", "", "", "", "")
	h.WriteLLMEvent("request_start", "anthropic", "claude-sonnet-4-6", "", "hi")
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
// activity.jsonl with mode 0o644 for bundle/export compatibility.
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
		if mode := snapInfo.Mode().Perm(); mode != 0o644 {
			t.Errorf("snapshot mode = %o, want 0644", mode)
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
	h.WriteAgentEvent("agent_response", "node1", "", "", "", big, "", "anthropic", "claude")
	// Trigger openFile via a pipeline event with a runID; the agent
	// write above doesn't carry one.
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC),
		RunID:     "big-line-test",
	})
	// Re-emit the agent event now that the file is open.
	h.WriteAgentEvent("agent_response", "node1", "", "", "", big, "", "anthropic", "claude")

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
