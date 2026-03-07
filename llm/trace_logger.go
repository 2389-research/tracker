// ABOUTME: Console logger for structured LLM trace events.
package llm

import (
	"fmt"
	"io"
	"time"
)

// TraceLoggerOptions configure console trace logging.
type TraceLoggerOptions struct {
	Verbose bool
}

// TraceLogger writes trace events to an io.Writer.
type TraceLogger struct {
	w       io.Writer
	verbose bool
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

	line := FormatTraceLine(evt, l.verbose)
	if line == "" {
		return
	}

	ts := time.Now().Format("15:04:05")
	fmt.Fprintf(l.w, "[%s] %s\n", ts, line)
}
