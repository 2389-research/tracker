// ABOUTME: Console logger for structured LLM trace events.
// ABOUTME: Batches sequential text/reasoning deltas into a single summary line per stream.
package llm

import (
	"fmt"
	"io"
	"strings"
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

	// Batchable event kinds: accumulate and defer output.
	if evt.Kind == TraceText || evt.Kind == TraceReasoning {
		if l.batchKind != evt.Kind {
			l.flushBatch()
			l.batchKind = evt.Kind
			l.batchProvider = evt.Provider
			l.batchModel = evt.Model
		}
		if evt.Preview != "" {
			l.batchParts = append(l.batchParts, evt.Preview)
		}
		return
	}

	// Non-batchable event: check if it would produce output before flushing.
	// This prevents invisible events (e.g. TraceProviderRaw when verbose=false)
	// from breaking an active text/reasoning batch.
	line := FormatTraceLine(evt, l.verbose)
	if line == "" {
		return
	}

	l.flushBatch()
	l.writeLine(line)
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
