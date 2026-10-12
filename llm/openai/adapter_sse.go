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
	if a.handleSSEDataCoreEvents(eventType, data, ch) {
		return
	}
	a.handleSSEDataOtherEvents(eventType, data, ch)
}

// handleSSEDataCoreEvents handles the primary response event types. Returns true if handled.
func (a *Adapter) handleSSEDataCoreEvents(eventType string, data []byte, ch chan<- llm.StreamEvent) bool {
	switch eventType {
	case "response.created":
		a.handleSSEResponseCreated(data, ch)
	case "response.output_item.added":
		a.handleSSEOutputItemAdded(data, ch)
	case "response.output_text.delta":
		a.handleSSEOutputTextDelta(data, ch)
	case "response.function_call_arguments.delta":
		a.handleSSEFuncArgsDelta(data, ch)
	default:
		return false
	}
	return true
}

// handleSSEDataOtherEvents handles completion, error, and no-op event types.
func (a *Adapter) handleSSEDataOtherEvents(eventType string, data []byte, ch chan<- llm.StreamEvent) {
	switch eventType {
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
	// Carry the response id and returned model so the stream accumulator can
	// reproduce the same metadata the non-stream Complete path returns (#605).
	ch <- llm.StreamEvent{
		Type:         llm.EventStreamStart,
		Raw:          data,
		FullResponse: &llm.Response{ID: evt.Response.ID, Model: evt.Response.Model},
	}
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
	usage := translateUsage(evt.Response.Usage)
	ch <- llm.StreamEvent{
		Type:         llm.EventFinish,
		FinishReason: &fr,
		Usage:        &usage,
	}
}

// sseErrorDetail is the code, type and message of a stream error.
type sseErrorDetail struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// sseErrorEvent is an `error` or `response.failed` event. OpenAI's API
// reference puts an error event's code and message at the top level and a
// failed response's under response.error. A nested error object and
// response.status_details.error are read too, so a server that sends either
// shape still surfaces its message.
type sseErrorEvent struct {
	Code     string         `json:"code"`
	Message  string         `json:"message"`
	Error    sseErrorDetail `json:"error"`
	Response struct {
		Error         sseErrorDetail `json:"error"`
		StatusDetails struct {
			Error sseErrorDetail `json:"error"`
		} `json:"status_details"`
	} `json:"response"`
}

// detail returns the first of the event's error details that carries a
// message or a code.
func (e *sseErrorEvent) detail() sseErrorDetail {
	for _, d := range []sseErrorDetail{
		{Code: e.Code, Message: e.Message},
		e.Error,
		e.Response.Error,
		e.Response.StatusDetails.Error,
	} {
		if d.Message != "" || d.Code != "" {
			return d
		}
	}
	return sseErrorDetail{}
}

func (a *Adapter) handleSSEError(eventType string, data []byte, ch chan<- llm.StreamEvent) {
	var errEvt sseErrorEvent
	if err := json.Unmarshal(data, &errEvt); err != nil {
		// An error event whose payload cannot be parsed must still surface
		// as an error — swallowing it would turn a failed request into a
		// bogus empty response.
		ch <- llm.StreamEvent{
			Type: llm.EventError,
			Err:  fmt.Errorf("openai: %s event with unparseable payload: %w", eventType, err),
		}
		return
	}
	d := errEvt.detail()
	msg := d.Message
	if msg == "" {
		msg = fmt.Sprintf("unknown API error (event type: %s)", eventType)
	}
	if d.Code != "" || d.Type != "" {
		// Name the code and type so the log shows what the server sent; the
		// code decides whether the error is retried (#684).
		msg += fmt.Sprintf(" (code=%q, type=%q)", d.Code, d.Type)
	}
	ch <- llm.StreamEvent{
		Type: llm.EventError,
		Err:  llm.ErrorFromOpenAICode(d.Code, "openai: "+msg, "openai"),
	}
}
