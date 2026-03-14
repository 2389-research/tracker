// ABOUTME: JSONL activity log writer — appends every event as a JSON line to a file.
// ABOUTME: Captures pipeline, agent, and LLM trace events for a complete audit trail in <runDir>/activity.jsonl.
package pipeline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// jsonlLogEntry is the on-disk format for one activity log line.
type jsonlLogEntry struct {
	Timestamp string `json:"ts"`
	Source    string `json:"source"`             // "pipeline", "agent", "llm"
	Type      string `json:"type"`
	RunID     string `json:"run_id,omitempty"`
	NodeID    string `json:"node_id,omitempty"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	Content   string `json:"content,omitempty"`
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
		Source:    "pipeline",
		Type:      string(evt.Type),
		RunID:     evt.RunID,
		NodeID:    evt.NodeID,
		Message:   evt.Message,
	}
	if evt.Err != nil {
		entry.Error = evt.Err.Error()
	}
	h.writeEntry(entry)
}

// WriteAgentEvent logs an agent event to the activity log.
// The caller is responsible for passing the event; the handler writes
// it to the same JSONL file as pipeline events.
func (h *JSONLEventHandler) WriteAgentEvent(evtType, toolName, toolOutput, toolError, text, errMsg, provider, model string) {
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
