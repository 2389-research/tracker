// ABOUTME: Unified NDJSON stream writer for --json mode.
// ABOUTME: Streams pipeline events, LLM trace events, and agent events as typed JSON lines to stdout.
package main

import (
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// jsonStreamEvent is the unified NDJSON output format for --json mode.
type jsonStreamEvent struct {
	Timestamp string `json:"ts"`
	Source     string `json:"source"`             // "pipeline", "llm", "agent"
	Type      string `json:"type"`                // event type within source
	RunID     string `json:"run_id,omitempty"`    // pipeline run ID
	NodeID    string `json:"node_id,omitempty"`   // pipeline node ID
	Message   string `json:"message,omitempty"`   // human-readable message
	Error     string `json:"error,omitempty"`     // error text
	Provider  string `json:"provider,omitempty"`  // LLM provider
	Model     string `json:"model,omitempty"`     // LLM model
	ToolName  string `json:"tool_name,omitempty"` // tool name for agent/LLM tool events
	Content   string `json:"content,omitempty"`   // text content (LLM output, tool output)
}

// jsonStream writes typed NDJSON events to an io.Writer. Thread-safe.
type jsonStream struct {
	mu sync.Mutex
	w  io.Writer
}

func newJSONStream(w io.Writer) *jsonStream {
	return &jsonStream{w: w}
}

func (s *jsonStream) write(evt jsonStreamEvent) {
	data, err := json.Marshal(evt)
	if err != nil {
		return
	}
	data = append(data, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.w.Write(data)
}

// pipelineHandler returns a PipelineEventHandler that writes to this stream.
func (s *jsonStream) pipelineHandler() pipeline.PipelineEventHandler {
	return pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		entry := jsonStreamEvent{
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
		s.write(entry)
	})
}

// traceObserver returns an LLM TraceObserver that writes to this stream.
func (s *jsonStream) traceObserver() llm.TraceObserver {
	return llm.TraceObserverFunc(func(evt llm.TraceEvent) {
		entry := jsonStreamEvent{
			Timestamp: time.Now().Format("2006-01-02T15:04:05.000Z07:00"),
			Source:    "llm",
			Type:      string(evt.Kind),
			Provider:  evt.Provider,
			Model:     evt.Model,
			ToolName:  evt.ToolName,
			Content:   evt.Preview,
		}
		s.write(entry)
	})
}

// agentHandler returns an agent EventHandler that writes to this stream.
func (s *jsonStream) agentHandler() agent.EventHandler {
	return agent.EventHandlerFunc(func(evt agent.Event) {
		content := evt.ToolOutput
		if content == "" {
			content = evt.Text
		}
		entry := jsonStreamEvent{
			Timestamp: time.Now().Format("2006-01-02T15:04:05.000Z07:00"),
			Source:    "agent",
			Type:      string(evt.Type),
			Provider:  evt.Provider,
			Model:     evt.Model,
			ToolName:  evt.ToolName,
			Content:   content,
		}
		if evt.ToolError != "" {
			entry.Error = evt.ToolError
		}
		if evt.Err != nil {
			if entry.Error != "" {
				entry.Error += ": " + evt.Err.Error()
			} else {
				entry.Error = evt.Err.Error()
			}
		}
		s.write(entry)
	})
}
