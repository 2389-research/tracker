// ABOUTME: Console logger for structured LLM trace events.
// ABOUTME: Batches sequential text/reasoning deltas into a single summary line per stream.
package llm

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// TraceLoggerOptions configure console trace logging.
type TraceLoggerOptions struct {
	Verbose bool
}

// TraceLogger writes trace events to an io.Writer. Sequential text and
// reasoning deltas are accumulated and flushed as a single summary line
// when the stream finishes or a different event kind arrives.
type TraceLogger struct {
	w       io.Writer
	verbose bool
	mu      sync.Mutex

	// Batching state for text/reasoning deltas
	batchKind     TraceKind // TraceText or TraceReasoning, or "" when idle
	batchProvider string
	batchModel    string
	batchParts    []string
}

// NewTraceLogger creates a console trace logger.
func NewTraceLogger(w io.Writer, opts TraceLoggerOptions) *TraceLogger {
	return &TraceLogger{w: w, verbose: opts.Verbose}
}

// HandleTraceEvent implements TraceObserver.
func (l *TraceLogger) HandleTraceEvent(evt TraceEvent) {
	if l == nil || l.w == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.isBatchable(evt) {
		l.accumulateBatch(evt)
		return
	}
	l.flushAndWrite(evt)
}

// isBatchable returns true for event kinds that should be accumulated before output.
func (l *TraceLogger) isBatchable(evt TraceEvent) bool {
	return evt.Kind == TraceText || evt.Kind == TraceReasoning
}

// accumulateBatch adds a batchable event to the current batch, flushing first if kind changed.
func (l *TraceLogger) accumulateBatch(evt TraceEvent) {
	if l.batchKind != evt.Kind {
		l.flushBatch()
		l.batchKind = evt.Kind
		l.batchProvider = evt.Provider
		l.batchModel = evt.Model
	}
	if evt.Preview != "" {
		l.batchParts = append(l.batchParts, evt.Preview)
	}
}

// flushAndWrite flushes any pending batch and writes the formatted line for evt.
// Non-batchable events that produce no output are silently ignored to avoid
// breaking an active text/reasoning batch with invisible events.
func (l *TraceLogger) flushAndWrite(evt TraceEvent) {
	line := FormatTraceLine(evt, l.verbose)
	if line == "" {
		return
	}
	l.flushBatch()
	l.writeLine(line)
}

// Flush forces any pending batched output to be written. Callers should
// invoke this when a stream terminates without a normal finish event
// (e.g. on error) to avoid losing buffered trace output.
func (l *TraceLogger) Flush() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushBatch()
}

// flushBatch writes the accumulated text/reasoning batch as a single line.
func (l *TraceLogger) flushBatch() {
	if l.batchKind == "" || len(l.batchParts) == 0 {
		l.batchKind = ""
		l.batchParts = nil
		return
	}

	preview := previewText(strings.Join(l.batchParts, " "))
	synthetic := TraceEvent{
		Kind:     l.batchKind,
		Provider: l.batchProvider,
		Model:    l.batchModel,
		Preview:  preview,
	}
	line := FormatTraceLine(synthetic, l.verbose)
	if line != "" {
		l.writeLine(line)
	}

	l.batchKind = ""
	l.batchProvider = ""
	l.batchModel = ""
	l.batchParts = nil
}

func (l *TraceLogger) writeLine(line string) {
	ts := time.Now().Format("15:04:05")
	fmt.Fprintf(l.w, "[%s] %s\n", ts, line)
}
