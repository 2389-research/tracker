// ABOUTME: SSE stream event types and handlers for the OpenAI Responses API.
// ABOUTME: Split from adapter.go to keep files under 500 lines.
package openai

import (
	"encoding/json"
	"fmt"

	"github.com/2389-research/tracker/llm"
)

// --- SSE event types for the OpenAI Responses API ---

type sseResponseCreated struct {
	Type     string `json:"type"`
	Response struct {
		ID    string `json:"id"`
		Model string `json:"model"`
	} `json:"response"`
}

type sseOutputItemAdded struct {
	Type        string `json:"type"`
	OutputIndex int    `json:"output_index"`
	Item        struct {
		Type   string `json:"type"`
		ID     string `json:"id,omitempty"`
		CallID string `json:"call_id,omitempty"`
		Name   string `json:"name,omitempty"`
	} `json:"item"`
}

type sseOutputTextDelta struct {
	Type         string `json:"type"`
	OutputIndex  int    `json:"output_index"`
	ContentIndex int    `json:"content_index"`
	Delta        string `json:"delta"`
}

type sseFunctionCallArgsDelta struct {
	Type        string `json:"type"`
	OutputIndex int    `json:"output_index"`
	Delta       string `json:"delta"`
}

type sseOutputItemDone struct {
	Type        string `json:"type"`
	OutputIndex int    `json:"output_index"`
	Item        struct {
		Type      string `json:"type"`
		ID        string `json:"id,omitempty"`
		CallID    string `json:"call_id,omitempty"`
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"item"`
}

type sseResponseCompleted struct {
	Type     string `json:"type"`
	Response struct {
		ID                string             `json:"id"`
		Status            string             `json:"status"`
		Usage             openaiUsage        `json:"usage"`
		IncompleteDetails *incompleteDetails `json:"incomplete_details,omitempty"`
		Output            []openaiOutputItem `json:"output"`
	} `json:"response"`
}

// handleSSEData processes a single SSE data payload.
func (a *Adapter) handleSSEData(eventType string, data []byte, ch chan<- llm.StreamEvent) {
	switch eventType {
	case "response.created":
		a.handleSSEResponseCreated(data, ch)
	case "response.output_item.added":
		a.handleSSEOutputItemAdded(data, ch)
	case "response.output_text.delta":
		a.handleSSEOutputTextDelta(data, ch)
	case "response.function_call_arguments.delta":
		a.handleSSEFuncArgsDelta(data, ch)
	case "response.output_item.done":
		a.handleSSEOutputItemDone(data, ch)
	case "response.completed":
		a.handleSSEResponseCompleted(data, ch)
	case "response.in_progress", "response.output_text.done",
		"response.content_part.added", "response.content_part.done",
		"response.function_call_arguments.done", "response.reasoning.done",
		"response.reasoning.delta":
		// Events we acknowledge but don't need to act on.
	case "error", "response.failed":
		a.handleSSEError(eventType, data, ch)
	default:
		ch <- llm.StreamEvent{Type: llm.EventProviderEvent, Raw: data}
	}
}

func (a *Adapter) handleSSEResponseCreated(data []byte, ch chan<- llm.StreamEvent) {
	var evt sseResponseCreated
	if err := json.Unmarshal(data, &evt); err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai: parse response.created: %w", err)}
		return
	}
	ch <- llm.StreamEvent{Type: llm.EventStreamStart, Raw: data}
}

func (a *Adapter) handleSSEOutputItemAdded(data []byte, ch chan<- llm.StreamEvent) {
	var evt sseOutputItemAdded
	if err := json.Unmarshal(data, &evt); err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai: parse output_item.added: %w", err)}
		return
	}
	switch evt.Item.Type {
	case "message":
		ch <- llm.StreamEvent{
			Type:   llm.EventTextStart,
			TextID: fmt.Sprintf("item_%d", evt.OutputIndex),
		}
	case "function_call":
		callID := evt.Item.CallID
		if callID == "" {
			callID = evt.Item.ID
		}
		ch <- llm.StreamEvent{
			Type:     llm.EventToolCallStart,
			ToolCall: &llm.ToolCallData{ID: callID, Name: evt.Item.Name},
		}
	case "reasoning":
		ch <- llm.StreamEvent{Type: llm.EventReasoningStart}
	}
}

func (a *Adapter) handleSSEOutputTextDelta(data []byte, ch chan<- llm.StreamEvent) {
	var evt sseOutputTextDelta
	if err := json.Unmarshal(data, &evt); err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai: parse output_text.delta: %w", err)}
		return
	}
	ch <- llm.StreamEvent{
		Type:   llm.EventTextDelta,
		TextID: fmt.Sprintf("item_%d", evt.OutputIndex),
		Delta:  evt.Delta,
	}
}

func (a *Adapter) handleSSEFuncArgsDelta(data []byte, ch chan<- llm.StreamEvent) {
	var evt sseFunctionCallArgsDelta
	if err := json.Unmarshal(data, &evt); err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai: parse function_call_arguments.delta: %w", err)}
		return
	}
	ch <- llm.StreamEvent{Type: llm.EventToolCallDelta, Delta: evt.Delta}
}

func (a *Adapter) handleSSEOutputItemDone(data []byte, ch chan<- llm.StreamEvent) {
	var evt sseOutputItemDone
	if err := json.Unmarshal(data, &evt); err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai: parse output_item.done: %w", err)}
		return
	}
	switch evt.Item.Type {
	case "message":
		ch <- llm.StreamEvent{
			Type:   llm.EventTextEnd,
			TextID: fmt.Sprintf("item_%d", evt.OutputIndex),
		}
	case "function_call":
		callID := evt.Item.CallID
		if callID == "" {
			callID = evt.Item.ID
		}
		ch <- llm.StreamEvent{
			Type: llm.EventToolCallEnd,
			ToolCall: &llm.ToolCallData{
				ID:        callID,
				Name:      evt.Item.Name,
				Arguments: json.RawMessage(evt.Item.Arguments),
			},
		}
	case "reasoning":
		ch <- llm.StreamEvent{Type: llm.EventReasoningEnd}
	}
}

func (a *Adapter) handleSSEResponseCompleted(data []byte, ch chan<- llm.StreamEvent) {
	var evt sseResponseCompleted
	if err := json.Unmarshal(data, &evt); err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai: parse response.completed: %w", err)}
		return
	}
	hasFunctionCalls := false
	for _, item := range evt.Response.Output {
		if item.Type == "function_call" {
			hasFunctionCalls = true
			break
		}
	}
	fr := translateFinishReason(evt.Response.Status, hasFunctionCalls, evt.Response.IncompleteDetails)
	ch <- llm.StreamEvent{
		Type:         llm.EventFinish,
		FinishReason: &fr,
		Usage: &llm.Usage{
			InputTokens:  evt.Response.Usage.InputTokens,
			OutputTokens: evt.Response.Usage.OutputTokens,
			TotalTokens:  evt.Response.Usage.TotalTokens,
		},
	}
}

func (a *Adapter) handleSSEError(eventType string, data []byte, ch chan<- llm.StreamEvent) {
	var errEvt struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
		Response struct {
			StatusDetails struct {
				Error struct {
					Message string `json:"message"`
					Code    string `json:"code"`
				} `json:"error"`
			} `json:"status_details"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &errEvt); err == nil {
		msg := errEvt.Error.Message
		code := errEvt.Error.Code
		if msg == "" {
			msg = errEvt.Response.StatusDetails.Error.Message
			code = errEvt.Response.StatusDetails.Error.Code
		}
		if msg == "" {
			msg = fmt.Sprintf("unknown API error (event type: %s)", eventType)
		}
		ch <- llm.StreamEvent{
			Type: llm.EventError,
			Err:  sseErrorToTyped(code, msg),
		}
	}
}

// sseErrorToTyped maps an OpenAI error code from an SSE stream event to a
// typed error from the llm error hierarchy.
func sseErrorToTyped(code, message string) error {
	base := llm.ProviderError{
		SDKError:  llm.SDKError{Msg: "openai: " + message},
		Provider:  "openai",
		ErrorCode: code,
	}
	switch code {
	case "insufficient_quota":
		return &llm.QuotaExceededError{ProviderError: base}
	case "invalid_api_key", "authentication_error":
		return &llm.AuthenticationError{ProviderError: base}
	case "model_not_found":
		return &llm.NotFoundError{ProviderError: base}
	case "invalid_request_error", "invalid_request":
		return &llm.InvalidRequestError{ProviderError: base}
	case "context_length_exceeded":
		return &llm.ContextLengthError{ProviderError: base}
	case "content_filter", "content_policy_violation":
		return &llm.ContentFilterError{ProviderError: base}
	case "rate_limit_exceeded":
		return &llm.RateLimitError{ProviderError: base}
	case "server_error", "internal_error":
		return &llm.ServerError{ProviderError: base}
	default:
		return &llm.InvalidRequestError{ProviderError: base}
	}
}
