// ABOUTME: Structured trace events for live LLM introspection across console and TUI surfaces.
package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

const tracePreviewLimit = 80

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
}

// TraceOptions configure trace building behavior.
type TraceOptions struct {
	Provider string
	Model    string
	Verbose  bool
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
	}

	switch evt.Type {
	case EventStreamStart:
		b.events = append(b.events, TraceEvent{Kind: TraceRequestStart, Provider: base.Provider, Model: base.Model})
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
	b.events = append(b.events, TraceEvent{Kind: TraceReasoning, Provider: base.Provider, Model: base.Model, Preview: preview})
}

// processTextDelta emits a TraceText event for a text delta.
func (b *TraceBuilder) processTextDelta(evt StreamEvent, base TraceEvent) {
	preview := preserveSpacingText(evt.Delta)
	if preview == "" {
		return
	}
	b.events = append(b.events, TraceEvent{Kind: TraceText, Provider: base.Provider, Model: base.Model, Preview: preview})
}

// processToolCallStart emits a TraceToolPrepare event for a tool call start.
func (b *TraceBuilder) processToolCallStart(evt StreamEvent, base TraceEvent) {
	if evt.ToolCall == nil {
		return
	}
	b.events = append(b.events, TraceEvent{
		Kind:     TraceToolPrepare,
		Provider: base.Provider,
		Model:    base.Model,
		ToolName: evt.ToolCall.Name,
		Preview:  previewJSON(evt.ToolCall.Arguments),
	})
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
	b.events = append(b.events, TraceEvent{
		Kind:         TraceFinish,
		Provider:     base.Provider,
		Model:        base.Model,
		FinishReason: finishReason,
		Usage:        usage,
	})
}

// processProviderEvent emits a TraceProviderRaw event when verbose tracing is enabled.
func (b *TraceBuilder) processProviderEvent(evt StreamEvent, base TraceEvent) {
	if !b.opts.Verbose {
		return
	}
	b.events = append(b.events, TraceEvent{
		Kind:          TraceProviderRaw,
		Provider:      base.Provider,
		Model:         base.Model,
		ProviderEvent: inferProviderEvent(evt.Raw),
		RawPreview:    previewJSON(evt.Raw),
	})
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

// FormatCoalescedLine formats an accumulated text or reasoning block for the TUI log.
// Shows clean content without the `preview="..."` wrapping, for a chat-like display.
// The provider/model is shown in a separate header line via FormatModelHeader.
func FormatCoalescedLine(kind TraceKind, accumulated string) string {
	label := "◉ "
	if kind == TraceReasoning {
		label = "◉ thinking: "
	}
	text := strings.TrimSpace(accumulated)
	return label + text
}

// FormatModelHeader returns a display string like "anthropic/claude-opus-4-6"
// for use as a section header in the activity log.
func FormatModelHeader(provider, model string) string {
	if provider != "" && model != "" {
		return provider + "/" + model
	}
	if provider != "" {
		return provider
	}
	return model
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
