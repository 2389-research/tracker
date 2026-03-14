// ABOUTME: JSONL activity log writer — appends every pipeline event as a JSON line to a file.
// ABOUTME: Provides a complete, machine-readable audit trail in <runDir>/activity.jsonl.
package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// jsonlLogEntry is the on-disk format for one activity log line.
type jsonlLogEntry struct {
	Timestamp string `json:"ts"`
	Type      string `json:"type"`
	RunID     string `json:"run_id,omitempty"`
	NodeID    string `json:"node_id,omitempty"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
}

// JSONLEventHandler appends every pipeline event as a JSON line to a file.
// The file is created lazily on the first event using the RunID and artifact
// directory to derive the path: <artifactDir>/<runID>/activity.jsonl.
// Safe for concurrent use from multiple goroutines.
type JSONLEventHandler struct {
	mu          sync.Mutex
	artifactDir string
	file        *os.File
}

// NewJSONLEventHandler creates a JSONL event logger that writes to
// <artifactDir>/<runID>/activity.jsonl. The file is opened lazily on first event.
func NewJSONLEventHandler(artifactDir string) *JSONLEventHandler {
	return &JSONLEventHandler{artifactDir: artifactDir}
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

	entry := jsonlLogEntry{
		Timestamp: evt.Timestamp.Format("2006-01-02T15:04:05.000Z07:00"),
		Type:      string(evt.Type),
		RunID:     evt.RunID,
		NodeID:    evt.NodeID,
		Message:   evt.Message,
	}
	if evt.Err != nil {
		entry.Error = evt.Err.Error()
	}
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
