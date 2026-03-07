// ABOUTME: Middleware that emits summaries of LLM call activity for live UI display.
// ABOUTME: Captures model name, tool calls, response text snippets, and errors.
package llm

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ActivityEvent describes a single LLM call for display in a live activity feed.
type ActivityEvent struct {
	Timestamp time.Time
	Model     string
	Provider  string
	// Request info
	ToolCount    int
	MessageCount int
	// Response info (empty on error)
	ResponseSnippet string
	ToolCalls       []string // names of tools called by the model
	InputTokens     int
	OutputTokens    int
	Latency         time.Duration
	// Error info
	Err error
}

// Summary returns a one-line description of the activity event.
func (e ActivityEvent) Summary() string {
	var sb strings.Builder

	if e.Provider != "" {
		sb.WriteString(e.Provider)
		sb.WriteString("/")
	}
	sb.WriteString(e.Model)

	if e.Err != nil {
		sb.WriteString(fmt.Sprintf(" ERROR: %v", e.Err))
		return sb.String()
	}

	if len(e.ToolCalls) > 0 {
		sb.WriteString(fmt.Sprintf(" → tools: %s", strings.Join(e.ToolCalls, ", ")))
	} else if e.ResponseSnippet != "" {
		sb.WriteString(fmt.Sprintf(" → %s", e.ResponseSnippet))
	}

	sb.WriteString(fmt.Sprintf(" (%dms, %d/%d tok)", e.Latency.Milliseconds(), e.InputTokens, e.OutputTokens))
	return sb.String()
}

// ActivityCallback is called for each LLM completion with an ActivityEvent summary.
type ActivityCallback func(ActivityEvent)

// ActivityTracker is a middleware that observes LLM calls and emits activity
// summaries via a callback. It does not modify requests or responses.
type ActivityTracker struct {
	callback ActivityCallback
}

// NewActivityTracker creates an activity tracking middleware that calls the
// provided callback after each LLM completion.
func NewActivityTracker(callback ActivityCallback) *ActivityTracker {
	return &ActivityTracker{callback: callback}
}

// WrapComplete implements the Middleware interface.
func (a *ActivityTracker) WrapComplete(next CompleteHandler) CompleteHandler {
	return func(ctx context.Context, req *Request) (*Response, error) {
		start := time.Now()
		resp, err := next(ctx, req)
		latency := time.Since(start)

		evt := ActivityEvent{
			Timestamp:    start,
			Model:        req.Model,
			Provider:     req.Provider,
			ToolCount:    len(req.Tools),
			MessageCount: len(req.Messages),
			Latency:      latency,
		}

		if err != nil {
			evt.Err = err
		} else if resp != nil {
			evt.Provider = resp.Provider
			evt.InputTokens = resp.Usage.InputTokens
			evt.OutputTokens = resp.Usage.OutputTokens

			// Extract tool call names
			for _, tc := range resp.ToolCalls() {
				evt.ToolCalls = append(evt.ToolCalls, tc.Name)
			}

			// Extract a text snippet if no tool calls
			if len(evt.ToolCalls) == 0 {
				text := resp.Text()
				if len(text) > 80 {
					text = text[:77] + "…"
				}
				// Collapse newlines for single-line display
				text = strings.ReplaceAll(text, "\n", " ")
				evt.ResponseSnippet = text
			}
		}

		if a.callback != nil {
			a.callback(evt)
		}

		return resp, err
	}
}
