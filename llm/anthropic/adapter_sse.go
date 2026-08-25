// ABOUTME: Anthropic SSE event handlers — dispatch + per-block-type translation.
// ABOUTME: Split from adapter.go (file-size ratchet); behavior-preserving.
package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/2389-research/tracker/llm"
)

func (a *Adapter) handleSSEData(eventType string, data []byte, ch chan<- llm.StreamEvent, blockTypes map[int]string, inputUsage **anthropicUsage) {
	switch eventType {
	case "message_start":
		a.handleSSEMessageStart(data, ch, inputUsage)
	case "content_block_start":
		a.handleSSEBlockStart(data, ch, blockTypes)
	case "content_block_delta":
		a.handleSSEBlockDelta(data, ch)
	case "content_block_stop":
		a.handleSSEBlockStop(data, ch, blockTypes)
	case "message_delta":
		a.handleSSEMessageDelta(data, ch, inputUsage)
	case "error":
		a.handleSSEErrorEvent(data, ch)
	case "message_stop", "ping":
		// No action needed.
	}
}

// sseErrorEvent is the payload of an SSE "error" event delivered inside an
// HTTP-200 stream (overloaded_error, rate_limit_error, api_error, ...).
type sseErrorEvent struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// handleSSEErrorEvent surfaces a mid-stream error event as a typed EventError.
// Anthropic reports these inside HTTP-200 streams, so they are invisible to
// status-code checks; dropping them would surface as a bogus empty response.
func (a *Adapter) handleSSEErrorEvent(data []byte, ch chan<- llm.StreamEvent) {
	var evt sseErrorEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("anthropic: parse error event: %w", err)}
		return
	}
	errType := evt.Error.Type
	if errType == "" {
		errType = "api_error"
	}
	msg := evt.Error.Message
	if msg == "" {
		msg = "unknown stream error"
	}
	ch <- llm.StreamEvent{
		Type: llm.EventError,
		Err:  llm.ErrorFromStatusCode(anthropicErrorTypeToStatus(errType), fmt.Sprintf("anthropic: %s: %s", errType, msg), "anthropic"),
	}
}

// anthropicErrorTypeToStatus maps Anthropic error type strings to the HTTP
// status codes they correspond to outside of streams, so stream errors get
// the same typed-error classification as non-stream errors.
func anthropicErrorTypeToStatus(errType string) int {
	switch errType {
	case "invalid_request_error":
		return 400
	case "authentication_error":
		return 401
	case "permission_error":
		return 403
	case "not_found_error":
		return 404
	case "request_too_large":
		return 413
	case "rate_limit_error":
		return 429
	case "overloaded_error":
		return 529
	default:
		return 500
	}
}

func (a *Adapter) handleSSEMessageStart(data []byte, ch chan<- llm.StreamEvent, inputUsage **anthropicUsage) {
	var evt sseMessageStart
	if err := json.Unmarshal(data, &evt); err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("anthropic: parse message_start: %w", err)}
		return
	}
	u := evt.Message.Usage
	*inputUsage = &u
	ch <- llm.StreamEvent{Type: llm.EventStreamStart, Raw: data}
}

func (a *Adapter) handleSSEBlockStart(data []byte, ch chan<- llm.StreamEvent, blockTypes map[int]string) {
	var evt sseContentBlockStart
	if err := json.Unmarshal(data, &evt); err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("anthropic: parse content_block_start: %w", err)}
		return
	}
	blockTypes[evt.Index] = evt.ContentBlock.Type
	switch evt.ContentBlock.Type {
	case "text":
		ch <- llm.StreamEvent{Type: llm.EventTextStart, TextID: fmt.Sprintf("block_%d", evt.Index)}
	case "tool_use":
		ch <- llm.StreamEvent{Type: llm.EventToolCallStart, ToolCall: &llm.ToolCallData{ID: evt.ContentBlock.ID, Name: evt.ContentBlock.Name}}
	case "thinking":
		ch <- llm.StreamEvent{Type: llm.EventReasoningStart}
	case "redacted_thinking":
		// Redacted thinking blocks carry an opaque data blob that must be round-tripped.
		ch <- llm.StreamEvent{Type: llm.EventRedactedThinking, ReasoningSignature: evt.ContentBlock.Data}
	}
}

func (a *Adapter) handleSSEBlockDelta(data []byte, ch chan<- llm.StreamEvent) {
	var evt sseContentBlockDelta
	if err := json.Unmarshal(data, &evt); err != nil {
		// Non-fatal (#573): a single unparseable delta (truncated / over-long /
		// empty) must not abort the whole turn with a terminal EventError. Skip it
		// and keep streaming — the block's content_block_stop and the message
		// finish still arrive. Dropping one delta degrades that one block at worst,
		// versus discarding every completed turn of the episode to a node retry.
		return
	}
	switch evt.Delta.Type {
	case "text_delta":
		ch <- llm.StreamEvent{Type: llm.EventTextDelta, TextID: fmt.Sprintf("block_%d", evt.Index), Delta: evt.Delta.Text}
	case "input_json_delta":
		ch <- llm.StreamEvent{Type: llm.EventToolCallDelta, Delta: evt.Delta.PartialJSON}
	case "thinking_delta":
		ch <- llm.StreamEvent{Type: llm.EventReasoningDelta, ReasoningDelta: evt.Delta.Thinking}
	case "signature_delta":
		ch <- llm.StreamEvent{Type: llm.EventReasoningSignature, ReasoningSignature: evt.Delta.Signature}
	}
}

func (a *Adapter) handleSSEBlockStop(data []byte, ch chan<- llm.StreamEvent, blockTypes map[int]string) {
	var evt sseContentBlockStop
	if err := json.Unmarshal(data, &evt); err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("anthropic: parse content_block_stop: %w", err)}
		return
	}
	switch blockTypes[evt.Index] {
	case "text":
		ch <- llm.StreamEvent{Type: llm.EventTextEnd, TextID: fmt.Sprintf("block_%d", evt.Index)}
	case "tool_use":
		ch <- llm.StreamEvent{Type: llm.EventToolCallEnd}
	case "thinking":
		ch <- llm.StreamEvent{Type: llm.EventReasoningEnd}
	}
}

func (a *Adapter) handleSSEMessageDelta(data []byte, ch chan<- llm.StreamEvent, inputUsage **anthropicUsage) {
	var evt sseMessageDelta
	if err := json.Unmarshal(data, &evt); err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("anthropic: parse message_delta: %w", err)}
		return
	}
	fr := translateFinishReason(evt.Delta.StopReason)
	usage := llm.Usage{OutputTokens: evt.Usage.OutputTokens}
	if *inputUsage != nil {
		usage.InputTokens = (*inputUsage).InputTokens
		if (*inputUsage).CacheReadInputTokens > 0 {
			v := (*inputUsage).CacheReadInputTokens
			usage.CacheReadTokens = &v
		}
		if (*inputUsage).CacheCreationInputTokens > 0 {
			v := (*inputUsage).CacheCreationInputTokens
			usage.CacheWriteTokens = &v
		}
	}
	usage = usage.Finalize()
	ch <- llm.StreamEvent{Type: llm.EventFinish, FinishReason: &fr, Usage: &usage}
}
