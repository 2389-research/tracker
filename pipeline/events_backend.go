// ABOUTME: EventBackendResolved and its payload: the one-per-run announcement of what
// ABOUTME: a node's `backend: auto` resolved to (claude-code or native) and why.
package pipeline

import "fmt"

// EventBackendResolved fires once per run, when the first `backend: auto`
// node resolves (or a resume restores the recorded value). Message carries
// the announcement text so every surface (console, TUI, activity log) shows
// the same line; the library itself never prints.
const EventBackendResolved PipelineEventType = "backend_resolved"

// BackendResolvedDetail is the payload for EventBackendResolved.
type BackendResolvedDetail struct {
	Backend         string `json:"backend"`
	Reason          string `json:"reason,omitempty"`
	ClaudePath      string `json:"claude_path,omitempty"`
	ClaudeVersion   string `json:"claude_version,omitempty"`
	FromRecord      bool   `json:"from_record,omitempty"`
	NativeAvailable bool   `json:"native_available,omitempty"` // a native client also exists
	// Explicit marks the #719 line for a claude-code backend chosen by the
	// node or --backend (not auto); ClaudeSource is TRACKER_CLAUDE_BIN or PATH.
	Explicit     bool   `json:"explicit,omitempty"`
	ClaudeSource string `json:"claude_source,omitempty"`
}

// BackendResolvedMessage is the announcement line for a resolution.
func BackendResolvedMessage(d BackendResolvedDetail) string {
	where := claudeWhere(d)
	if d.Explicit {
		return fmt.Sprintf("backend: claude-code (%s)", where)
	}
	if d.FromRecord {
		return fmt.Sprintf("backend: auto → %s (recorded in the checkpoint)", d.Backend)
	}
	if d.Backend != "claude-code" {
		return fmt.Sprintf("backend: auto → %s (%s)", d.Backend, d.Reason)
	}
	msg := fmt.Sprintf("backend: auto → claude-code (%s; no API key needed)", where)
	if d.NativeAvailable {
		msg += " — set --backend native to use your API key instead"
	}
	return msg
}

// claudeWhere is "claude <version> at <path>", plus ", from TRACKER_CLAUDE_BIN"
// when the binary was named rather than found on PATH.
func claudeWhere(d BackendResolvedDetail) string {
	w := "claude at " + d.ClaudePath
	if d.ClaudeVersion != "" {
		w = "claude " + d.ClaudeVersion + " at " + d.ClaudePath
	}
	if d.ClaudeSource == "TRACKER_CLAUDE_BIN" {
		w += ", from TRACKER_CLAUDE_BIN"
	}
	return w
}

// applyBackendResolvedFields copies the payload into an activity-log entry.
func applyBackendResolvedFields(entry *jsonlLogEntry, d *BackendResolvedDetail) {
	if d == nil {
		return
	}
	entry.BackendResolved = d.Backend
	entry.BackendReason = d.Reason
	entry.ClaudePath = d.ClaudePath
	entry.ClaudeVersion = d.ClaudeVersion
	entry.BackendFromRecord = d.FromRecord
}
