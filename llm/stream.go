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
	EventStreamStart        StreamEventType = "stream_start"
	EventTextStart          StreamEventType = "text_start"
	EventTextDelta          StreamEventType = "text_delta"
	EventTextEnd            StreamEventType = "text_end"
	EventReasoningStart     StreamEventType = "reasoning_start"
	EventReasoningDelta     StreamEventType = "reasoning_delta"
	EventReasoningSignature StreamEventType = "reasoning_signature"
	EventReasoningEnd       StreamEventType = "reasoning_end"
	EventRedactedThinking   StreamEventType = "redacted_thinking"
	EventToolCallStart      StreamEventType = "tool_call_start"
	EventToolCallDelta      StreamEventType = "tool_call_delta"
	EventToolCallEnd        StreamEventType = "tool_call_end"
	EventFinish             StreamEventType = "finish"
	EventError              StreamEventType = "error"
	EventProviderEvent      StreamEventType = "provider_event"
)

// StreamEvent represents a single event in a streaming response.
type StreamEvent struct {
	Type               StreamEventType `json:"type"`
	Delta              string          `json:"delta,omitempty"`
	TextID             string          `json:"text_id,omitempty"`
	ReasoningDelta     string          `json:"reasoning_delta,omitempty"`
	ReasoningSignature string          `json:"reasoning_signature,omitempty"`
	ToolCall           *ToolCallData   `json:"tool_call,omitempty"`
	FinishReason       *FinishReason   `json:"finish_reason,omitempty"`
	Usage              *Usage          `json:"usage,omitempty"`
	FullResponse       *Response       `json:"full_response,omitempty"`
	Err                error           `json:"-"`
	Raw                json.RawMessage `json:"raw,omitempty"`
}

// StreamAccumulator collects streaming events into a complete Response.
type StreamAccumulator struct {
	// textParts tracks text parts by ID in insertion order.
	textOrder []string
	textParts map[string]*strings.Builder

	// reasoning accumulates reasoning deltas and signature.
	reasoning          strings.Builder
	reasoningSignature string

	// redactedThinking collects opaque data blobs for redacted thinking blocks.
	redactedThinking []string

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
		a.processTextStart(event)
	case EventTextDelta:
		a.processTextDelta(event)
	case EventReasoningDelta:
		a.reasoning.WriteString(event.ReasoningDelta)
	case EventReasoningSignature:
		a.reasoningSignature = event.ReasoningSignature
	case EventRedactedThinking:
		a.processRedactedThinking(event)
	case EventToolCallStart:
		a.processToolCallStart(event)
	case EventToolCallDelta:
		a.activeToolArgs.WriteString(event.Delta)
	case EventToolCallEnd:
		a.processToolCallEnd()
	case EventFinish:
		a.processFinish(event)
	}
}

func (a *StreamAccumulator) processRedactedThinking(event StreamEvent) {
	if event.ReasoningSignature != "" {
		a.redactedThinking = append(a.redactedThinking, event.ReasoningSignature)
	}
}

func (a *StreamAccumulator) processTextStart(event StreamEvent) {
	if _, exists := a.textParts[event.TextID]; !exists {
		a.textOrder = append(a.textOrder, event.TextID)
		a.textParts[event.TextID] = &strings.Builder{}
	}
}

func (a *StreamAccumulator) processTextDelta(event StreamEvent) {
	b, exists := a.textParts[event.TextID]
	if !exists {
		a.textOrder = append(a.textOrder, event.TextID)
		b = &strings.Builder{}
		a.textParts[event.TextID] = b
	}
	b.WriteString(event.Delta)
}

func (a *StreamAccumulator) processToolCallStart(event StreamEvent) {
	if event.ToolCall == nil {
		return
	}
	a.activeToolCall = &ToolCallData{
		ID:             event.ToolCall.ID,
		Name:           event.ToolCall.Name,
		ThoughtSigData: event.ToolCall.ThoughtSigData,
	}
	a.activeToolArgs.Reset()
	// Initialize from start event args (e.g., Google sends full args on start).
	if len(event.ToolCall.Arguments) > 0 {
		a.activeToolArgs.Write(event.ToolCall.Arguments)
	}
}

func (a *StreamAccumulator) processToolCallEnd() {
	if a.activeToolCall == nil {
		return
	}
	a.activeToolCall.Arguments = json.RawMessage(a.activeToolArgs.String())
	a.toolCalls = append(a.toolCalls, *a.activeToolCall)
	a.activeToolCall = nil
	a.activeToolArgs.Reset()
}

func (a *StreamAccumulator) processFinish(event StreamEvent) {
	if event.FinishReason != nil {
		a.finishReason = event.FinishReason
	}
	if event.Usage != nil {
		a.usage = event.Usage
	}
}

// Response builds a complete Response from the accumulated events.
func (a *StreamAccumulator) Response() Response {
	var content []ContentPart

	// Add reasoning first (matches API content block ordering: thinking before text).
	if a.reasoning.Len() > 0 || a.reasoningSignature != "" {
		content = append(content, ContentPart{
			Kind: KindThinking,
			Thinking: &ThinkingData{
				Text:      a.reasoning.String(),
				Signature: a.reasoningSignature,
			},
		})
	}

	// Add redacted thinking blocks (opaque data that must be round-tripped).
	for _, data := range a.redactedThinking {
		content = append(content, ContentPart{
			Kind: KindRedactedThinking,
			Thinking: &ThinkingData{
				Redacted:  true,
				Signature: data,
			},
		})
	}

	// Add text parts in insertion order.
	for _, id := range a.textOrder {
		if b, ok := a.textParts[id]; ok {
			content = append(content, ContentPart{
				Kind: KindText,
				Text: b.String(),
			})
		}
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
