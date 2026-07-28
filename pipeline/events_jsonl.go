// ABOUTME: JSONL activity log writer — appends every event as a JSON line to a file.
// ABOUTME: Captures pipeline, agent, and LLM trace events for a complete audit trail in <runDir>/activity.jsonl.
package pipeline

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/internal/bundleid"
)

// JSONLEventHandler appends every pipeline event as a JSON line to a
// file. The runtime writes to an integrity-protected path outside any
// directory a tool subprocess sees as cmd.Dir (see SecureActivityLogPath);
// every line is prefixed with ActivityLogSentinel so post-hoc readers
// can flag injection. At Close() a sentinel-stripped snapshot is copied
// to the legacy path under artifactDir so bundle export (#213) and any
// pre-existing tooling that reads <runDir>/activity.jsonl still works.
//
// artifactDir is retained on the handler solely as the destination for
// that snapshot — live writes during the run never go to artifactDir.
// Safe for concurrent use from multiple goroutines.
type JSONLEventHandler struct {
	mu             sync.Mutex
	artifactDir    string
	runID          string
	securePath     string
	file           *os.File
	bundleIdentity string
	captureRawLLM  bool  // write provider_raw / llm_provider_raw lines (see SetCaptureRawLLM)
	snapshotErr    error // populated by Close; readable via SnapshotErr.
}

// NewJSONLEventHandler creates a JSONL event logger. The live log lands
// at the SecureActivityLogPath for the run's runID; on Close a stripped
// snapshot is written to <artifactDir>/<runID>/activity.jsonl. The file
// is opened lazily on first event so callers that never feed events
// produce no on-disk footprint.
func NewJSONLEventHandler(artifactDir string) *JSONLEventHandler {
	return &JSONLEventHandler{artifactDir: artifactDir}
}

// SetBundleIdentity sets the .dipx bundle identity ("sha256:<hex>") that
// will be stamped onto subsequent WriteAgentEvent and WriteLLMEvent
// writes. Empty (the default) is a no-op so plain .dip runs see no
// change. Called once at run-start after the handler is constructed.
//
// Note: events that flow through HandlePipelineEvent already get stamped
// at the engine and registry levels; this setter only affects the
// JSONL writes that bypass those chokepoints (agent and llm events).
func (h *JSONLEventHandler) SetBundleIdentity(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.bundleIdentity = id
}

// SetCaptureRawLLM controls whether raw provider streaming chunks
// (agent "llm_provider_raw" and trace "provider_raw" lines) are written
// to the activity log. Off by default: per-token raw chunks are
// debugging payload, not run telemetry, and they dominated log size
// (~92% of lines in the #354 census). Wired from --verbose so debug
// runs keep full capture.
func (h *JSONLEventHandler) SetCaptureRawLLM(enabled bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.captureRawLLM = enabled
}

// isRawLLMEventType reports whether evtType is a raw provider streaming
// chunk under either spelling (agent event or llm trace kind).
func isRawLLMEventType(evtType string) bool {
	return evtType == "provider_raw" || evtType == "llm_provider_raw"
}

// openFile creates the secure activity log file on first use.
// The file is mode 0o600 and lives outside any tool subprocess's
// cmd.Dir — see SecureActivityLogPath. Writes are sentinel-prefixed
// in writeEntry. artifactDir is still required: it pins the snapshot
// destination, and we refuse to log if the caller didn't configure one
// (matches pre-#213 behavior).
func (h *JSONLEventHandler) openFile(runID string) error {
	if h.file != nil || h.artifactDir == "" {
		return nil
	}
	securePath, err := SecureActivityLogPath(runID)
	if err != nil {
		return err
	}
	secureDir := filepath.Dir(securePath)
	if err := os.MkdirAll(secureDir, 0o700); err != nil {
		return err
	}
	// O_NOFOLLOW (unix builds) refuses to open the path if it's a
	// symlink — same threat as the snapshot's destination, narrower
	// window. Even though the secure path lives outside cmd.Dir and a
	// new run's runID is random, an out-of-band same-UID process can
	// in principle pre-plant a symlink at securePath; this catches
	// that. On Windows snapshotNoFollow is 0 (no-op).
	f, err := os.OpenFile(securePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND|snapshotNoFollow, 0o600)
	if err != nil {
		return err
	}
	// Force-tighten the file mode: the 0o600 in OpenFile only applies
	// at creation, so a pre-existing file from a prior run (or a race)
	// could retain wider permissions. Best-effort — errors are
	// non-fatal because the underlying access control is same-UID, the
	// mode is defense-in-depth against other local users.
	_ = os.Chmod(securePath, 0o600)
	// Defense in depth: if the directory was created with a more
	// permissive mode by an earlier process (race or pre-existing dir),
	// re-chmod best-effort. Errors are non-fatal — the file mode is
	// the actual access gate.
	_ = os.Chmod(secureDir, 0o700)
	h.runID = runID
	h.securePath = securePath
	h.file = f
	return nil
}

// HandlePipelineEvent implements PipelineEventHandler.
func (h *JSONLEventHandler) HandlePipelineEvent(evt PipelineEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.file == nil && evt.RunID != "" {
		_ = h.openFile(evt.RunID)
	}
	if h.file == nil {
		return
	}

	entry := buildLogEntry(evt)
	h.writeEntry(entry)
}

// buildLogEntry converts a PipelineEvent to a jsonlLogEntry.
func buildLogEntry(evt PipelineEvent) jsonlLogEntry {
	entry := jsonlLogEntry{
		Timestamp:      evt.Timestamp.Format("2006-01-02T15:04:05.000Z07:00"),
		Source:         "pipeline",
		Type:           string(evt.Type),
		RunID:          evt.RunID,
		NodeID:         evt.NodeID,
		Message:        evt.Message,
		BundleIdentity: evt.BundleIdentity,
	}
	if evt.Err != nil {
		entry.Error = evt.Err.Error()
	}
	if d := evt.Decision; d != nil {
		applyDecisionFields(&entry, d)
	}
	if evt.Cost != nil {
		entry.TotalTokens = evt.Cost.TotalTokens
		entry.TotalCostUSD = evt.Cost.TotalCostUSD
		entry.ProviderTotals = evt.Cost.ProviderTotals
		entry.WallElapsedMs = evt.Cost.WallElapsed.Milliseconds()
		entry.Estimated = evt.Cost.Estimated
	}
	if evt.Truncation != nil {
		entry.TruncStream = evt.Truncation.Stream
		entry.TruncLimit = evt.Truncation.Limit
		entry.TruncCaptured = evt.Truncation.CapturedBytes
		entry.TruncDropped = evt.Truncation.DroppedBytes
		entry.TruncTotal = evt.Truncation.TotalBytes
	}
	if evt.Marker != nil {
		entry.MarkerPattern = evt.Marker.Pattern
		entry.MarkerTail = evt.Marker.CapturedTail
		entry.MarkerError = evt.Marker.Error
	}
	if evt.Route != nil {
		entry.RouteTail = evt.Route.CapturedTail
	}
	if evt.AutoStatus != nil {
		entry.AutoStatusTail = evt.AutoStatus.ResponseTail
		entry.AutoStatusFailClosed = evt.AutoStatus.FailClosed
	}
	if evt.Override != nil {
		entry.OverrideGate = evt.Override.GateNodeID
		entry.OverrideLabel = evt.Override.Label
		entry.OverrideActor = evt.Override.Actor
		if len(evt.Override.SubgraphPath) > 0 {
			// Copy to defend against later mutation of the source slice.
			entry.OverrideSubgraphPath = append([]string(nil), evt.Override.SubgraphPath...)
		}
	}
	return entry
}

// applyDecisionFields copies edge decision fields into the log entry.
func applyDecisionFields(entry *jsonlLogEntry, d *DecisionDetail) {
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
	entry.TokenInput = d.TokenInput
	entry.TokenOutput = d.TokenOutput
	entry.ConditionsTried = d.ConditionsTried
}

// WriteAgentEvent logs an agent event to the activity log, writing to the
// same JSONL file as pipeline events. The event carries its own NodeID
// (which pipeline branch produced it) alongside the session/turn identity
// and per-turn metrics that let a post-hoc reader rebuild the run as a
// tree rather than a flat sequence — see applyAgentEventFields.
//
// Takes the whole agent.Event rather than unpacked strings: the fields
// worth logging outgrew a parameter list, and passing the struct means a
// newly-populated field reaches disk without another signature change.
func (h *JSONLEventHandler) WriteAgentEvent(evt agent.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.file == nil {
		return
	}
	evtType := string(evt.Type)
	if isRawLLMEventType(evtType) && !h.captureRawLLM {
		return
	}

	content := evt.ToolOutput
	if content == "" {
		content = evt.Text
	}

	entry := jsonlLogEntry{
		Timestamp: time.Now().Format("2006-01-02T15:04:05.000Z07:00"),
		Source:    "agent",
		Type:      evtType,
		NodeID:    evt.NodeID,
		ToolName:  evt.ToolName,
		Content:   content,
		Provider:  evt.Provider,
		Model:     evt.Model,
		Error:     joinAgentErrors(evt),
	}
	applyAgentEventFields(&entry, evt)
	// Stamp .dipx bundle identity unless the caller already set one. Mirrors
	// Engine.emit and the registry's BundleIdentityStamper — these writes
	// bypass both chokepoints, so the stamping has to happen here for
	// activity.jsonl provenance to stay complete for agent events.
	if entry.BundleIdentity == "" {
		entry.BundleIdentity = h.bundleIdentity
	}
	h.writeEntry(entry)
}

// WriteLLMEvent logs an LLM trace event to the activity log.
func (h *JSONLEventHandler) WriteLLMEvent(kind, provider, model, toolName, preview string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.file == nil {
		return
	}
	if isRawLLMEventType(kind) && !h.captureRawLLM {
		return
	}

	entry := jsonlLogEntry{
		Timestamp: time.Now().Format("2006-01-02T15:04:05.000Z07:00"),
		Source:    "llm",
		Type:      kind,
		Provider:  provider,
		Model:     model,
		ToolName:  toolName,
		Content:   preview,
	}
	// Stamp .dipx bundle identity unless the caller already set one. Mirrors
	// Engine.emit and the registry's BundleIdentityStamper — these writes
	// bypass both chokepoints, so the stamping has to happen here for
	// activity.jsonl provenance to stay complete for llm trace events.
	if entry.BundleIdentity == "" {
		entry.BundleIdentity = h.bundleIdentity
	}
	h.writeEntry(entry)
}

// WriteBundleMismatchForced records a forced bundle-identity override on
// resume. Called once at run-start (before the engine fires any pipeline
// events) when --force-bundle-mismatch allowed resume despite a mismatch
// between the checkpoint's stored identity and the current bundle's
// identity. Both identities are preserved in the log entry so post-hoc
// auditors can see what was overridden.
//
// The entry's bundle_identity field is stamped with the CURRENT identity
// (what the run actually executes against), so post-hoc scans grouping
// activity.jsonl lines by bundle see this override clustered with the
// rest of the run.
//
// runID is needed to open the activity log file lazily — this is the
// first event the handler ever writes, so the file isn't open yet
// (HandlePipelineEvent's lazy openFile hasn't run). Pass the resume run
// ID here.
func (h *JSONLEventHandler) WriteBundleMismatchForced(runID, originalIdentity, currentIdentity string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.file == nil && runID != "" {
		_ = h.openFile(runID)
	}
	if h.file == nil {
		return
	}

	entry := jsonlLogEntry{
		Timestamp:      time.Now().Format("2006-01-02T15:04:05.000Z07:00"),
		Source:         "cli",
		Type:           string(EventBundleMismatchForced),
		RunID:          runID,
		BundleIdentity: currentIdentity,
		Message: fmt.Sprintf(
			"bundle identity mismatch forced via --force-bundle-mismatch (original: %s, current: %s)",
			bundleid.DisplayForLog(originalIdentity),
			bundleid.DisplayForLog(currentIdentity),
		),
	}
	h.writeEntry(entry)
}

// writeEntry marshals and writes a log entry. Caller must hold h.mu.
// Every line is prefixed with ActivityLogSentinel so post-hoc readers
// can distinguish runtime writes from anything else that touched the
// file. See the "Activity log integrity" section of CLAUDE.md for the
// threat model.
func (h *JSONLEventHandler) writeEntry(entry jsonlLogEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	buf := make([]byte, 0, len(ActivityLogSentinel)+len(data)+1)
	buf = append(buf, ActivityLogSentinel...)
	buf = append(buf, data...)
	buf = append(buf, '\n')
	_, _ = h.file.Write(buf)
}

// Close flushes the secure activity log, writes a sentinel-stripped
// snapshot to <artifactDir>/<runID>/activity.jsonl for bundle/export
// consumers, and closes the underlying file. The snapshot is
// best-effort: snapshot errors don't break Close (the secure file is
// the authoritative record) but they're stashed on h.snapshotErr so
// callers that care about bundle/export coverage can inspect them via
// SnapshotErr().
func (h *JSONLEventHandler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.file == nil {
		return nil
	}
	if err := h.file.Sync(); err != nil {
		_ = h.file.Close()
		h.file = nil
		return err
	}
	if err := h.writeSnapshot(); err != nil {
		h.snapshotErr = err
	}
	err := h.file.Close()
	h.file = nil
	return err
}

// SnapshotErr returns the error (if any) from the most recent Close-time
// snapshot mirror to the legacy run-dir path. Callers that depend on
// the legacy snapshot for bundle export or external tooling can check
// this after Close. Nil when the snapshot succeeded or was skipped
// (no artifactDir / no events). The secure file remains authoritative
// regardless of this value.
func (h *JSONLEventHandler) SnapshotErr() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.snapshotErr
}

// refuseIfSymlink errors when path exists and is a symlink. Missing
// paths are OK (the snapshot creates them). Any other error from Lstat
// propagates so the snapshot bails out rather than continuing on
// uncertain state.
func refuseIfSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", path)
	}
	return nil
}

// writeSnapshot copies the secure log to <artifactDir>/<runID>/activity.jsonl
// with sentinel prefixes stripped, so existing tooling (bundle export,
// git_artifacts, anything that greps run dirs) continues to find a
// readable JSONL file at the legacy path. Errors are returned for the
// caller's logging convenience but do not fail Close — the secure file
// stays authoritative regardless of snapshot health.
//
// Caller must hold h.mu.
func (h *JSONLEventHandler) writeSnapshot() error {
	if h.artifactDir == "" || h.runID == "" || h.securePath == "" {
		return nil
	}
	legacyDir := filepath.Join(h.artifactDir, h.runID)
	// Pre-flight: if a tool subprocess swapped <artifactDir>/<runID>
	// for a symlink during the run, MkdirAll would silently follow it
	// and OpenFile's O_NOFOLLOW only guards the final component — the
	// snapshot would land at the attacker's chosen target. Lstat catches
	// that. TOCTOU window between this check and MkdirAll is small and
	// the snapshot is best-effort: refusing on suspicion is safer than
	// silently mirroring elsewhere. Same defense for artifactDir itself.
	if err := refuseIfSymlink(h.artifactDir); err != nil {
		return fmt.Errorf("snapshot dest unsafe: %w", err)
	}
	if err := refuseIfSymlink(legacyDir); err != nil {
		return fmt.Errorf("snapshot dest unsafe: %w", err)
	}
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		return fmt.Errorf("snapshot mkdir: %w", err)
	}
	legacyPath := filepath.Join(legacyDir, "activity.jsonl")

	src, err := os.Open(h.securePath)
	if err != nil {
		return fmt.Errorf("snapshot open secure: %w", err)
	}
	defer src.Close()

	// O_NOFOLLOW (unix builds) refuses to traverse a symlink at the
	// destination — a tool subprocess that pre-creates the legacy path
	// as a symlink to a sensitive location cannot redirect our write.
	// O_TRUNC overwrites any plain-file scratch the subprocess left.
	dst, err := os.OpenFile(legacyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|snapshotNoFollow, 0o644)
	if err != nil {
		return fmt.Errorf("snapshot open legacy: %w", err)
	}
	defer dst.Close()

	w := bufio.NewWriter(dst)
	// Use bufio.Reader.ReadBytes('\n') instead of bufio.Scanner so the
	// snapshot can handle arbitrarily long lines. Agent/LLM events can
	// produce JSONL entries that exceed bufio.Scanner's 1 MiB default
	// (e.g. long ContextSnapshot maps or aggregated tool stdout in
	// captured content fields). Scanner would have silently dropped
	// those by erroring at scan-time.
	r := bufio.NewReaderSize(src, 64*1024)
	sentinel := []byte(ActivityLogSentinel)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimPrefix(line, sentinel)
			if _, wErr := w.Write(line); wErr != nil {
				return fmt.Errorf("snapshot write: %w", wErr)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("snapshot read: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("snapshot flush: %w", err)
	}
	return nil
}
