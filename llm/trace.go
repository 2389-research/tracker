// ABOUTME: Structured trace events for live LLM introspection across console and TUI surfaces.
package llm

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const tracePreviewLimit = 80

// NewCallID returns an identifier for one LLM request. Callers stamp it into
// TraceOptions so every trace event from that request shares it; see
// TraceEvent.CallID for why grouping matters.
//
// Locally generated rather than taken from Response.ID: the provider's ID only
// exists once the response arrives, so events emitted before then — including
// the request-start that carries the wire body — would have nothing to group
// on. Collision resistance only has to hold within one run's log.
func NewCallID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// TraceKind identifies a normalized LLM trace event.
type TraceKind string

const (
	TraceRequestStart TraceKind = "request_start"
	TraceReasoning    TraceKind = "reasoning"
	TraceText         TraceKind = "text"
	TraceToolPrepare  TraceKind = "tool_prepare"
	TraceFinish       TraceKind = "finish"
	TraceProviderRaw  TraceKind = "provider_raw"
)

// TraceEvent is a normalized event for rendering live LLM activity.
//
// Preview and RawPreview are display fields: previewText collapses newlines
// and clips to tracePreviewLimit (80 chars) so a TUI line stays a TUI line.
// The Raw* / Arguments fields alongside them carry the same content
// untruncated, for the activity log. Text and reasoning deltas are not
// clipped in either place — preserveSpacingText keeps them whole because
// coalescing clipped deltas would corrupt the reconstructed text.
type TraceEvent struct {
	Kind          TraceKind
	Provider      string
	Model         string
	ToolName      string
	Preview       string
	ProviderEvent string
	RawPreview    string
	FinishReason  string
	Usage         Usage
	// CallID groups every event belonging to one LLM request. Two log paths
	// record LLM activity — the agent session re-emits trace events as agent
	// llm_* events, and the client-level writer catches calls no session sees
	// (the autopilot interviewer, for one) — so one call can legitimately
	// appear twice. Both paths are kept because their coverage differs; this
	// is what lets a reader collapse the overlap rather than double-count
	// usage when aggregating.
	CallID string
	// RequestRaw is the verbatim wire body, set on TraceRequestStart. The
	// normalized Request says what tracker asked for; this says what actually
	// went out after provider translation and ProviderOptions merging.
	RequestRaw json.RawMessage
	// ToolArguments is the untruncated tool-call arguments, set on
	// TraceToolPrepare. Preview holds the same value clipped to 80 chars,
	// which is enough to render but not enough to reconstruct the call.
	ToolArguments json.RawMessage
	// ProviderRaw is the untruncated provider chunk, set on TraceProviderRaw
	// (verbose only). RawPreview holds the clipped form.
	ProviderRaw json.RawMessage
	// SessionOwned is true when the originating request carried
	// request-level TraceObservers — in tracker only the agent session
	// registers those, and it re-emits every trace event as an agent
	// llm_* event. Activity-log writers listening at the client level
	// skip SessionOwned events to avoid logging the same stream twice
	// (issue #354); non-session calls (e.g. the autopilot interviewer)
	// stay SessionOwned=false so the trace path remains their only,
	// and therefore kept, log surface.
	SessionOwned bool
}

// TraceOptions configure trace building behavior.
type TraceOptions struct {
	Provider string
	Model    string
	Verbose  bool
	// CallID identifies the one request this builder traces. It is stamped
	// onto every event the builder emits so a reader can group them, and —
	// more importantly — collapse the overlap between the two log paths that
	// both record LLM activity (see TraceEvent.CallID).
	CallID string
}

// TraceObserver receives normalized LLM trace events.
type TraceObserver interface {
	HandleTraceEvent(evt TraceEvent)
}

// TraceObserverFunc adapts a function into a TraceObserver.
type TraceObserverFunc func(evt TraceEvent)

// HandleTraceEvent implements TraceObserver.
func (f TraceObserverFunc) HandleTraceEvent(evt TraceEvent) {
	f(evt)
}

// TraceBuilder converts streaming events into normalized trace events.
type TraceBuilder struct {
	opts   TraceOptions
	events []TraceEvent
	// requestRaw holds the wire body from EventRequestSent until the
	// provider's stream-start arrives, so both land on one trace event
	// instead of the log carrying two request-start lines.
	requestRaw json.RawMessage
}

// NewTraceBuilder creates a trace builder for one request.
func NewTraceBuilder(opts TraceOptions) *TraceBuilder {
	return &TraceBuilder{opts: opts}
}

// Process ingests one stream event and emits any corresponding trace events.
func (b *TraceBuilder) Process(evt StreamEvent) {
	base := TraceEvent{
		Provider: b.opts.Provider,
		Model:    b.opts.Model,
		CallID:   b.opts.CallID,
	}

	switch evt.Type {
	case EventRequestSent:
		// Stash only — the trace event is emitted when the provider's
		// stream-start arrives, so there is exactly one request_start.
		b.requestRaw = evt.RequestRaw
	case EventStreamStart:
		start := base
		start.Kind = TraceRequestStart
		start.RequestRaw = firstRaw(evt.RequestRaw, b.requestRaw)
		b.events = append(b.events, start)
	case EventReasoningDelta:
		b.processReasoningDelta(evt, base)
	case EventTextDelta:
		b.processTextDelta(evt, base)
	case EventToolCallStart:
		b.processToolCallStart(evt, base)
	case EventFinish:
		b.processFinish(evt, base)
	case EventProviderEvent:
		b.processProviderEvent(evt, base)
	}
}

// processReasoningDelta emits a TraceReasoning event for a reasoning delta.
func (b *TraceBuilder) processReasoningDelta(evt StreamEvent, base TraceEvent) {
	preview := preserveSpacingText(evt.ReasoningDelta)
	if preview == "" {
		return
	}
	evtOut := base
	evtOut.Kind = TraceReasoning
	evtOut.Preview = preview
	b.events = append(b.events, evtOut)
}

// processTextDelta emits a TraceText event for a text delta.
func (b *TraceBuilder) processTextDelta(evt StreamEvent, base TraceEvent) {
	preview := preserveSpacingText(evt.Delta)
	if preview == "" {
		return
	}
	evtOut := base
	evtOut.Kind = TraceText
	evtOut.Preview = preview
	b.events = append(b.events, evtOut)
}

// processToolCallStart emits a TraceToolPrepare event for a tool call start.
func (b *TraceBuilder) processToolCallStart(evt StreamEvent, base TraceEvent) {
	if evt.ToolCall == nil {
		return
	}
	evtOut := base
	evtOut.Kind = TraceToolPrepare
	evtOut.ToolName = evt.ToolCall.Name
	evtOut.Preview = previewJSON(evt.ToolCall.Arguments)
	evtOut.ToolArguments = evt.ToolCall.Arguments
	b.events = append(b.events, evtOut)
}

// processFinish emits a TraceFinish event with reason and usage.
func (b *TraceBuilder) processFinish(evt StreamEvent, base TraceEvent) {
	finishReason := ""
	if evt.FinishReason != nil {
		finishReason = evt.FinishReason.Reason
	}
	usage := Usage{}
	if evt.Usage != nil {
		usage = *evt.Usage
	}
	evtOut := base
	evtOut.Kind = TraceFinish
	evtOut.FinishReason = finishReason
	evtOut.Usage = usage
	b.events = append(b.events, evtOut)
}

// processProviderEvent emits a TraceProviderRaw event when verbose tracing is enabled.
func (b *TraceBuilder) processProviderEvent(evt StreamEvent, base TraceEvent) {
	if !b.opts.Verbose {
		return
	}
	evtOut := base
	evtOut.Kind = TraceProviderRaw
	evtOut.ProviderEvent = inferProviderEvent(evt.Raw)
	evtOut.RawPreview = previewJSON(evt.Raw)
	evtOut.ProviderRaw = evt.Raw
	b.events = append(b.events, evtOut)
}

// Events returns the trace events emitted so far.
func (b *TraceBuilder) Events() []TraceEvent {
	out := make([]TraceEvent, len(b.events))
	copy(out, b.events)
	return out
}

// FormatTraceLine formats one trace event for console or TUI rendering.
func FormatTraceLine(evt TraceEvent, verbose bool) string {
	switch evt.Kind {
	case TraceProviderRaw:
		return formatProviderRawLine(evt, verbose)
	case TraceRequestStart:
		return formatBaseLine("llm start", evt)
	case TraceReasoning:
		return formatBaseLine("llm thinking", evt) + appendPreview(evt.Preview)
	case TraceText:
		return formatBaseLine("llm text", evt) + appendPreview(evt.Preview)
	case TraceToolPrepare:
		return formatToolPrepareLine(evt)
	case TraceFinish:
		return formatFinishLine(evt)
	}
	return ""
}

// formatProviderRawLine formats a TraceProviderRaw event line.
func formatProviderRawLine(evt TraceEvent, verbose bool) string {
	if !verbose {
		return ""
	}
	line := "provider event"
	if evt.ProviderEvent != "" {
		line += "=" + evt.ProviderEvent
	}
	if evt.RawPreview != "" {
		line += " preview=" + quotePreview(evt.RawPreview)
	}
	return line
}

// formatToolPrepareLine formats a TraceToolPrepare event line.
func formatToolPrepareLine(evt TraceEvent) string {
	line := formatBaseLine("llm tool prepare", evt)
	if evt.ToolName != "" {
		line += " name=" + evt.ToolName
	}
	line += appendPreview(evt.Preview)
	return line
}

// formatFinishLine formats a TraceFinish event line.
func formatFinishLine(evt TraceEvent) string {
	line := formatBaseLine("llm finish", evt)
	if evt.FinishReason != "" {
		line += " reason=" + evt.FinishReason
	}
	if evt.Usage.InputTokens != 0 || evt.Usage.OutputTokens != 0 {
		line += fmt.Sprintf(" tokens=%d/%d", evt.Usage.InputTokens, evt.Usage.OutputTokens)
	}
	return line
}

func formatBaseLine(prefix string, evt TraceEvent) string {
	suffix := formatProviderModelSuffix(evt.Provider, evt.Model)
	if suffix == "" {
		return prefix
	}
	return prefix + " " + suffix
}

// formatProviderModelSuffix returns "provider/model", "provider", "model", or "" depending
// on which fields are set.
func formatProviderModelSuffix(provider, model string) string {
	if provider == "" && model == "" {
		return ""
	}
	if provider == "" {
		return model
	}
	if model == "" {
		return provider
	}
	return provider + "/" + model
}

func appendPreview(preview string) string {
	if preview == "" {
		return ""
	}
	return " preview=" + quotePreview(preview)
}

func quotePreview(preview string) string {
	return fmt.Sprintf("%q", preview)
}

func previewText(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if len(text) <= tracePreviewLimit {
		return text
	}
	return text[:tracePreviewLimit-1] + "…"
}

// preserveSpacingText keeps all whitespace (including newlines) intact.
// Streaming text deltas carry leading spaces as word separators and newlines
// for paragraph structure; stripping them causes words to run together
// or flattens structured output when chunks are coalesced.
func preserveSpacingText(text string) string {
	return text
}

// firstRaw returns the first non-empty payload. Lets a stream-start event
// carry the body directly if an adapter ever sets it there, while falling
// back to the stashed EventRequestSent value.
func firstRaw(candidates ...json.RawMessage) json.RawMessage {
	for _, c := range candidates {
		if len(c) > 0 {
			return c
		}
	}
	return nil
}

func previewJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	return previewText(string(raw))
}

func inferProviderEvent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var payload struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return payload.Type
}
