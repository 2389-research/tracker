// ABOUTME: Public NDJSON event writer for the tracker --json wire format.
// ABOUTME: Threaded from pipeline/LLM/agent event streams; thread-safe for concurrent writers.
package tracker

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

const ndjsonTimestampLayout = "2006-01-02T15:04:05.000Z07:00"

// StreamEvent is the stable wire format for the tracker --json mode. Field
// tags are stable; new optional fields may be added without a major bump.
type StreamEvent struct {
	Timestamp string `json:"ts"`
	Source    string `json:"source"`              // "pipeline", "llm", "agent"
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

// NDJSONWriter is a thread-safe writer that serializes StreamEvents line by
// line onto an io.Writer. Library consumers use it to produce the same
// stream as the tracker CLI's --json mode.
//
// Backpressure note: Write holds an internal mutex for the duration of the
// underlying io.Writer.Write call. When three handler sources (pipeline,
// agent, LLM trace) share one writer, a slow backing writer serializes
// handler callbacks across those sources. If the backing writer can block
// (network socket, pipe), wrap it in a bufio.Writer or a channel-backed
// forwarder to decouple producers from the slow sink.
type NDJSONWriter struct {
	mu        sync.Mutex
	w         io.Writer
	errOnce   sync.Once
	panicOnce sync.Once
}

// NewNDJSONWriter returns a new writer backed by w.
func NewNDJSONWriter(w io.Writer) *NDJSONWriter {
	return &NDJSONWriter{w: w}
}

// Write serializes evt as a JSON line. Safe to call from multiple
// goroutines. Returns a non-nil error if marshalling or writing to the
// underlying io.Writer fails; the first write error is also logged to
// os.Stderr once so long-running callers that ignore the error still
// surface it.
func (s *NDJSONWriter) Write(evt StreamEvent) error {
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal NDJSON event: %w", err)
	}
	data = append(data, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, werr := s.w.Write(data); werr != nil {
		s.errOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "tracker: NDJSON stream write error: %v (further write errors suppressed)\n", werr)
		})
		return werr
	}
	return nil
}

// PipelineHandler returns a pipeline.PipelineEventHandler that writes events
// to this stream. Panics in the underlying writer are recovered and logged
// to os.Stderr once (per writer instance) so a misbehaving sink cannot
// crash the pipeline goroutine.
func (s *NDJSONWriter) PipelineHandler() pipeline.PipelineEventHandler {
	return pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		defer s.recoverPanic("pipeline")
		entry := StreamEvent{
			Timestamp: evt.Timestamp.Format(ndjsonTimestampLayout),
			Source:    "pipeline",
			Type:      string(evt.Type),
			RunID:     evt.RunID,
			NodeID:    evt.NodeID,
			Message:   evt.Message,
		}
		if evt.Err != nil {
			entry.Error = evt.Err.Error()
		}
		_ = s.Write(entry)
	})
}

// TraceObserver returns an llm.TraceObserver that writes trace events to
// this stream. Panics in the underlying writer are recovered (see
// PipelineHandler).
func (s *NDJSONWriter) TraceObserver() llm.TraceObserver {
	return llm.TraceObserverFunc(func(evt llm.TraceEvent) {
		defer s.recoverPanic("llm")
		_ = s.Write(StreamEvent{
			Timestamp: time.Now().Format(ndjsonTimestampLayout),
			Source:    "llm",
			Type:      string(evt.Kind),
			Provider:  evt.Provider,
			Model:     evt.Model,
			ToolName:  evt.ToolName,
			Content:   evt.Preview,
		})
	})
}

// AgentHandler returns an agent.EventHandler that writes agent events to
// this stream. Panics in the underlying writer are recovered (see
// PipelineHandler).
func (s *NDJSONWriter) AgentHandler() agent.EventHandler {
	return agent.EventHandlerFunc(func(evt agent.Event) {
		defer s.recoverPanic("agent")
		content := evt.ToolOutput
		if content == "" {
			content = evt.Text
		}
		ts := evt.Timestamp
		if ts.IsZero() {
			ts = time.Now()
		}
		entry := StreamEvent{
			Timestamp: ts.Format(ndjsonTimestampLayout),
			Source:    "agent",
			Type:      string(evt.Type),
			NodeID:    evt.NodeID,
			Provider:  evt.Provider,
			Model:     evt.Model,
			ToolName:  evt.ToolName,
			Content:   content,
		}
		entry.Error = buildStreamEntryError(evt)
		_ = s.Write(entry)
	})
}

// recoverPanic recovers from a handler panic and logs the first occurrence
// per writer instance. Using a per-instance sync.Once (not package-level)
// means multiple NDJSONWriter instances (e.g., different runs streaming to
// different sinks) each get their own suppression state, so one misbehaving
// sink does not silence unrelated panics elsewhere in the process.
func (s *NDJSONWriter) recoverPanic(source string) {
	if r := recover(); r != nil {
		s.panicOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "tracker: NDJSON %s handler recovered from panic: %v (further panics suppressed)\n", source, r)
		})
	}
}

func buildStreamEntryError(evt agent.Event) string {
	if evt.ToolError == "" && evt.Err == nil {
		return ""
	}
	if evt.ToolError != "" && evt.Err != nil {
		return evt.ToolError + ": " + evt.Err.Error()
	}
	if evt.ToolError != "" {
		return evt.ToolError
	}
	return evt.Err.Error()
}
