// ABOUTME: JSONL activity log writer — appends every event as a JSON line to a file.
// ABOUTME: Captures pipeline, agent, and LLM trace events for a complete audit trail in <runDir>/activity.jsonl.
package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/2389-research/tracker/internal/bundleid"
)

// jsonlLogEntry is the on-disk format for one activity log line.
type jsonlLogEntry struct {
	Timestamp      string `json:"ts"`
	Source         string `json:"source"` // "pipeline" (engine emissions) | "agent" (LLM session) | "llm" (raw provider events) | "cli" (CLI-level audit, e.g. bundle_mismatch_forced)
	Type           string `json:"type"`
	RunID          string `json:"run_id,omitempty"`
	NodeID         string `json:"node_id,omitempty"`
	Message        string `json:"message,omitempty"`
	Error          string `json:"error,omitempty"`
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	ToolName       string `json:"tool_name,omitempty"`
	Content        string `json:"content,omitempty"`
	BundleIdentity string `json:"bundle_identity,omitempty"`

	// Decision audit trail fields.
	EdgeFrom        string            `json:"edge_from,omitempty"`
	EdgeTo          string            `json:"edge_to,omitempty"`
	EdgeCondition   string            `json:"edge_condition,omitempty"`
	EdgePriority    string            `json:"edge_priority,omitempty"`
	ConditionMatch  *bool             `json:"condition_match,omitempty"`
	OutcomeStatus   string            `json:"outcome_status,omitempty"`
	ContextSnapshot map[string]string `json:"context_snapshot,omitempty"`
	ContextUpdates  map[string]string `json:"context_updates,omitempty"`
	RestartCount    *int              `json:"restart_count,omitempty"`
	ClearedNodes    []string          `json:"cleared_nodes,omitempty"`
	TokenInput      int               `json:"token_input,omitempty"`
	TokenOutput     int               `json:"token_output,omitempty"`

	// Cost snapshot fields — non-zero for cost_updated and budget_exceeded events.
	TotalTokens    int                      `json:"total_tokens,omitempty"`
	TotalCostUSD   float64                  `json:"total_cost_usd,omitempty"`
	ProviderTotals map[string]ProviderUsage `json:"provider_totals,omitempty"`
	WallElapsedMs  int64                    `json:"wall_elapsed_ms,omitempty"`
	// Estimated is true when any session contributing to this cost snapshot
	// was heuristic-derived (currently: ACP rune-count estimator). External
	// NDJSON consumers read this to distinguish metered from estimated
	// spend — see cmd/tracker/summary.go for the equivalent CLI surface.
	Estimated bool `json:"estimated,omitempty"`

	// Truncation fields — populated for tool_output_truncated events.
	// Stream is "stdout" or "stderr"; CapturedBytes / DroppedBytes /
	// TotalBytes record the per-stream byte accounting at the time of
	// truncation. Issue #208.
	TruncStream   string `json:"trunc_stream,omitempty"`
	TruncLimit    int    `json:"trunc_limit,omitempty"`
	TruncCaptured int    `json:"trunc_captured_bytes,omitempty"`
	TruncDropped  int    `json:"trunc_dropped_bytes,omitempty"`
	TruncTotal    int    `json:"trunc_total_bytes,omitempty"`

	// Conditional-fallthrough fields — populated for
	// conditional_fallthrough events. Lists routing intents that
	// evaluated false on the way to a fallback selection.
	ConditionsTried []ConditionEval `json:"conditions_tried,omitempty"`

	// Marker fields — populated for tool_marker_missing events
	// (issue #210). Pattern is the configured marker_grep regex;
	// MarkerTail is up to 256 bytes from end of captured stdout for
	// diagnosis; MarkerError is the regex-compile error when the
	// failure was a bad regex rather than a missing match.
	MarkerPattern string `json:"marker_pattern,omitempty"`
	MarkerTail    string `json:"marker_tail,omitempty"`
	MarkerError   string `json:"marker_error,omitempty"`

	// Route fields — populated for tool_route_missing events (#212).
	// The matcher is built-in so there is no Pattern field; just the
	// captured stdout tail for diagnosis.
	RouteTail string `json:"route_tail,omitempty"`
}

// JSONLEventHandler appends every pipeline event as a JSON line to a file.
// The file is created lazily on the first event using the RunID and artifact
// directory to derive the path: <artifactDir>/<runID>/activity.jsonl.
// Safe for concurrent use from multiple goroutines.
type JSONLEventHandler struct {
	mu             sync.Mutex
	artifactDir    string
	file           *os.File
	bundleIdentity string
}

// NewJSONLEventHandler creates a JSONL event logger that writes to
// <artifactDir>/<runID>/activity.jsonl. The file is opened lazily on first event.
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

// openFile creates the activity log file on first use.
func (h *JSONLEventHandler) openFile(runID string) error {
	if h.file != nil || h.artifactDir == "" {
		return nil
	}
	dir := filepath.Join(h.artifactDir, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "activity.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
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

// WriteAgentEvent logs an agent event to the activity log.
// The caller is responsible for passing the event; the handler writes
// it to the same JSONL file as pipeline events. The nodeID identifies
// which pipeline node (branch) produced this event.
func (h *JSONLEventHandler) WriteAgentEvent(evtType, nodeID, toolName, toolOutput, toolError, text, errMsg, provider, model string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.file == nil {
		return
	}

	content := toolOutput
	if content == "" {
		content = text
	}

	entry := jsonlLogEntry{
		Timestamp: time.Now().Format("2006-01-02T15:04:05.000Z07:00"),
		Source:    "agent",
		Type:      evtType,
		NodeID:    nodeID,
		ToolName:  toolName,
		Content:   content,
		Provider:  provider,
		Model:     model,
	}
	if toolError != "" {
		entry.Error = toolError
	}
	if errMsg != "" {
		if entry.Error != "" {
			entry.Error += ": " + errMsg
		} else {
			entry.Error = errMsg
		}
	}
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
func (h *JSONLEventHandler) writeEntry(entry jsonlLogEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = h.file.Write(data)
}

// Close flushes and closes the underlying file.
func (h *JSONLEventHandler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.file == nil {
		return nil
	}
	return h.file.Close()
}
