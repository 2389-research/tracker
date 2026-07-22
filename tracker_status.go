// ABOUTME: Reads a run's agent-authored status-update timeline from the activity
// ABOUTME: log — a clean, high-level "what got done" history, distinct from the firehose (#494).
package tracker

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
)

// statusUpdateType is the activity-log type for agent-authored status narration
// (mirrors agent.EventStatusUpdate; kept as a literal to avoid importing agent here).
const statusUpdateType = "status_update"

// StatusEntry is one agent-authored status update.
type StatusEntry struct {
	Timestamp string `json:"ts"`
	NodeID    string `json:"node_id,omitempty"`
	Text      string `json:"text"`
}

// RunStatusTimeline returns the agent-authored status updates for a run, in log
// order — a compact high-level timeline of what the run accomplished, without the
// per-turn/tool firehose. runDir must be a resolved run directory.
func RunStatusTimeline(runDir string) ([]StatusEntry, error) {
	path, _ := ResolveActivityLogPath(runDir)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []StatusEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if e, ok := parseStatusLine(scanner.Text()); ok {
			out = append(out, e)
		}
	}
	if err := scanner.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// parseStatusLine extracts a StatusEntry from one activity-log line, or false if
// the line isn't a non-empty status_update event.
func parseStatusLine(raw string) (StatusEntry, bool) {
	body, _ := stripActivitySentinel(raw)
	line := strings.TrimSpace(body)
	if line == "" {
		return StatusEntry{}, false
	}
	var e statusLogEntry
	if json.Unmarshal([]byte(line), &e) != nil {
		return StatusEntry{}, false
	}
	if e.Type != statusUpdateType || strings.TrimSpace(e.Content) == "" {
		return StatusEntry{}, false
	}
	return StatusEntry{Timestamp: e.Timestamp, NodeID: e.NodeID, Text: strings.TrimSpace(e.Content)}, true
}

// statusLogEntry is the slice of an activity-log line the status timeline needs.
type statusLogEntry struct {
	Timestamp string `json:"ts"`
	Type      string `json:"type"`
	NodeID    string `json:"node_id"`
	Content   string `json:"content"`
}
