// ABOUTME: Streaming event types and accumulator for incremental LLM responses.
// ABOUTME: Defines StreamEvent, StreamEventType enum, and StreamAccumulator.
package llm

import (
	"encoding/json"
	"strings"
)

// StreamEventType discriminates the kind of streaming event.
type StreamEventType string

const (
	EventStreamStart    StreamEventType = "stream_start"
	EventTextStart      StreamEventType = "text_start"
	EventTextDelta      StreamEventType = "text_delta"
	EventTextEnd        StreamEventType = "text_end"
	EventReasoningStart StreamEventType = "reasoning_start"
	EventReasoningDelta StreamEventType = "reasoning_delta"
	EventReasoningEnd   StreamEventType = "reasoning_end"
	EventToolCallStart  StreamEventType = "tool_call_start"
	EventToolCallDelta  StreamEventType = "tool_call_delta"
	EventToolCallEnd    StreamEventType = "tool_call_end"
	EventFinish         StreamEventType = "finish"
	EventError          StreamEventType = "error"
	EventProviderEvent  StreamEventType = "provider_event"
)

// StreamEvent represents a single event in a streaming response.
type StreamEvent struct {
	Type           StreamEventType `json:"type"`
	Delta          string          `json:"delta,omitempty"`
	TextID         string          `json:"text_id,omitempty"`
	ReasoningDelta string          `json:"reasoning_delta,omitempty"`
	ToolCall       *ToolCallData   `json:"tool_call,omitempty"`
	FinishReason   *FinishReason   `json:"finish_reason,omitempty"`
	Usage          *Usage          `json:"usage,omitempty"`
	FullResponse   *Response       `json:"full_response,omitempty"`
	Err            error           `json:"-"`
	Raw            json.RawMessage `json:"raw,omitempty"`
}

// StreamAccumulator collects streaming events into a complete Response.
type StreamAccumulator struct {
	// textParts tracks text parts by ID in insertion order.
	textOrder []string
	textParts map[string]*strings.Builder

	// reasoning accumulates reasoning deltas.
	reasoning strings.Builder

	// toolCalls collects completed tool calls.
	toolCalls []ToolCallData

	// activeToolCall tracks the tool call currently being streamed.
	activeToolCall *ToolCallData
	// activeToolArgs accumulates argument deltas for the active tool call.
	activeToolArgs strings.Builder

	finishReason *FinishReason
	usage        *Usage
}

// NewStreamAccumulator creates a new StreamAccumulator ready to process events.
func NewStreamAccumulator() *StreamAccumulator {
	return &StreamAccumulator{
		textParts: make(map[string]*strings.Builder),
	}
}

// Process handles a single StreamEvent, updating the accumulator state.
func (a *StreamAccumulator) Process(event StreamEvent) {
	switch event.Type {
	case EventTextStart:
		if _, exists := a.textParts[event.TextID]; !exists {
			a.textOrder = append(a.textOrder, event.TextID)
			a.textParts[event.TextID] = &strings.Builder{}
		}

	case EventTextDelta:
		b, exists := a.textParts[event.TextID]
		if !exists {
			a.textOrder = append(a.textOrder, event.TextID)
			b = &strings.Builder{}
			a.textParts[event.TextID] = b
		}
		b.WriteString(event.Delta)

	case EventReasoningDelta:
		a.reasoning.WriteString(event.ReasoningDelta)

	case EventToolCallStart:
		if event.ToolCall != nil {
			a.activeToolCall = &ToolCallData{
				ID:   event.ToolCall.ID,
				Name: event.ToolCall.Name,
			}
			a.activeToolArgs.Reset()
		}

	case EventToolCallDelta:
		a.activeToolArgs.WriteString(event.Delta)

	case EventToolCallEnd:
		if a.activeToolCall != nil {
			a.activeToolCall.Arguments = json.RawMessage(a.activeToolArgs.String())
			a.toolCalls = append(a.toolCalls, *a.activeToolCall)
			a.activeToolCall = nil
			a.activeToolArgs.Reset()
		}

	case EventFinish:
		if event.FinishReason != nil {
			a.finishReason = event.FinishReason
		}
		if event.Usage != nil {
			a.usage = event.Usage
		}
	}
}

// Response builds a complete Response from the accumulated events.
func (a *StreamAccumulator) Response() Response {
	var content []ContentPart

	// Add text parts in insertion order.
	for _, id := range a.textOrder {
		if b, ok := a.textParts[id]; ok {
			content = append(content, ContentPart{
				Kind: KindText,
				Text: b.String(),
			})
		}
	}

	// Add reasoning if present.
	if a.reasoning.Len() > 0 {
		content = append(content, ContentPart{
			Kind: KindThinking,
			Thinking: &ThinkingData{
				Text: a.reasoning.String(),
			},
		})
	}

	// Add tool calls.
	for i := range a.toolCalls {
		content = append(content, ContentPart{
			Kind:     KindToolCall,
			ToolCall: &a.toolCalls[i],
		})
	}

	resp := Response{
		Message: Message{
			Role:    RoleAssistant,
			Content: content,
		},
	}

	if a.finishReason != nil {
		resp.FinishReason = *a.finishReason
	}
	if a.usage != nil {
		resp.Usage = *a.usage
	}

	return resp
}
