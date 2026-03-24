// ABOUTME: OpenAI Responses API adapter implementing the ProviderAdapter interface.
// ABOUTME: Handles HTTP communication, SSE stream parsing, and request/response lifecycle.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/2389-research/tracker/llm"
)

const (
	defaultBaseURL = "https://api.openai.com"
	responsesPath  = "/v1/responses"
)

// Adapter implements llm.ProviderAdapter for the OpenAI Responses API.
type Adapter struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures an Adapter.
type Option func(*Adapter)

// WithBaseURL overrides the default OpenAI API base URL.
func WithBaseURL(url string) Option {
	return func(a *Adapter) {
		a.baseURL = url
	}
}

// WithHTTPClient provides a custom http.Client.
func WithHTTPClient(client *http.Client) Option {
	return func(a *Adapter) {
		a.httpClient = client
	}
}

// New creates a new OpenAI adapter with the given API key and options.
func New(apiKey string, opts ...Option) *Adapter {
	a := &Adapter{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
	for _, opt := range opts {
		opt(a)
	}
	// Strip surrounding quotes that may be embedded in env var values.
	a.apiKey = strings.Trim(a.apiKey, "\"'")
	a.baseURL = strings.Trim(a.baseURL, "\"'")
	// Normalize base URL: strip trailing /v1 suffix since responsesPath
	// already includes the /v1 prefix. OPENAI_BASE_URL conventionally
	// includes /v1 (e.g. http://localhost:9999/v1), which would cause
	// a double /v1/v1 path without this normalization.
	a.baseURL = strings.TrimSuffix(a.baseURL, "/v1")
	return a
}

// Name returns the provider identifier.
func (a *Adapter) Name() string {
	return "openai"
}

// Complete sends a synchronous request to the OpenAI Responses API.
func (a *Adapter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	body, err := translateRequest(req)
	if err != nil {
		return nil, fmt.Errorf("openai: translate request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+responsesPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	a.setHeaders(httpReq)

	start := time.Now()
	httpResp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, &llm.NetworkError{SDKError: llm.SDKError{Msg: fmt.Sprintf("openai: %s", err.Error()), Cause: err}}
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, &llm.NetworkError{SDKError: llm.SDKError{Msg: fmt.Sprintf("openai: read response: %s", err.Error()), Cause: err}}
	}

	if httpResp.StatusCode != http.StatusOK {
		msg := string(respBody)
		return nil, llm.ErrorFromStatusCode(httpResp.StatusCode, msg, "openai")
	}

	resp, err := translateResponse(respBody)
	if err != nil {
		return nil, fmt.Errorf("openai: translate response: %w", err)
	}

	resp.Provider = "openai"
	resp.Latency = time.Since(start)

	return resp, nil
}

// Stream sends a streaming request and returns a channel of events.
func (a *Adapter) Stream(ctx context.Context, req *llm.Request) <-chan llm.StreamEvent {
	ch := make(chan llm.StreamEvent, 64)
	emitProviderEvents := shouldEmitProviderEvents(req)

	go func() {
		defer close(ch)

		body, err := translateRequest(req)
		if err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai: translate request: %w", err)}
			return
		}

		// Inject stream: true into the body.
		var bodyMap map[string]any
		if err := json.Unmarshal(body, &bodyMap); err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: err}
			return
		}
		bodyMap["stream"] = true
		body, err = json.Marshal(bodyMap)
		if err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: err}
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+responsesPath, bytes.NewReader(body))
		if err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: err}
			return
		}
		a.setHeaders(httpReq)

		httpResp, err := a.httpClient.Do(httpReq)
		if err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: &llm.NetworkError{SDKError: llm.SDKError{Msg: err.Error(), Cause: err}}}
			return
		}
		defer httpResp.Body.Close()

		if httpResp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(httpResp.Body)
			ch <- llm.StreamEvent{Type: llm.EventError, Err: llm.ErrorFromStatusCode(httpResp.StatusCode, string(respBody), "openai")}
			return
		}

		a.parseSSE(httpResp.Body, ch, emitProviderEvents)
	}()

	return ch
}

// Close releases resources held by the adapter.
func (a *Adapter) Close() error {
	return nil
}

// setHeaders applies standard OpenAI API headers to the request.
func (a *Adapter) setHeaders(httpReq *http.Request) {
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
}

// parseSSE reads SSE events from the response body and emits StreamEvents.
func (a *Adapter) parseSSE(body io.Reader, ch chan<- llm.StreamEvent, emitProviderEvents bool) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	var eventType string

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if emitProviderEvents {
			ch <- llm.StreamEvent{Type: llm.EventProviderEvent, Raw: json.RawMessage(data)}
		}
		// When no SSE "event:" line precedes the data, extract the type
		// from the JSON payload itself. Some servers (including the
		// AttractorBench mock) omit SSE event lines and embed the type
		// in the data object.
		resolvedType := eventType
		if resolvedType == "" {
			var peek struct {
				Type string `json:"type"`
			}
			if json.Unmarshal([]byte(data), &peek) == nil && peek.Type != "" {
				resolvedType = peek.Type
			}
		}
		a.handleSSEData(resolvedType, []byte(data), ch)
		eventType = ""
	}

	if err := scanner.Err(); err != nil && !isContextError(err) {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai: SSE scan error: %w", err)}
	}
}

// isContextError returns true for context cancellation/deadline errors that
// are expected during normal shutdown and should not surface as SSE errors.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func shouldEmitProviderEvents(req *llm.Request) bool {
	if req == nil || req.ProviderOptions == nil {
		return false
	}
	enabled, _ := req.ProviderOptions["tracker_emit_provider_events"].(bool)
	return enabled
}

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
		var evt sseResponseCreated
		if err := json.Unmarshal(data, &evt); err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai: parse response.created: %w", err)}
			return
		}
		ch <- llm.StreamEvent{
			Type: llm.EventStreamStart,
			Raw:  data,
		}

	case "response.output_item.added":
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
			// Prefer call_id over id for function_call items.
			callID := evt.Item.CallID
			if callID == "" {
				callID = evt.Item.ID
			}
			ch <- llm.StreamEvent{
				Type: llm.EventToolCallStart,
				ToolCall: &llm.ToolCallData{
					ID:   callID,
					Name: evt.Item.Name,
				},
			}
		case "reasoning":
			ch <- llm.StreamEvent{
				Type: llm.EventReasoningStart,
			}
		}

	case "response.output_text.delta":
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

	case "response.function_call_arguments.delta":
		var evt sseFunctionCallArgsDelta
		if err := json.Unmarshal(data, &evt); err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai: parse function_call_arguments.delta: %w", err)}
			return
		}
		ch <- llm.StreamEvent{
			Type:  llm.EventToolCallDelta,
			Delta: evt.Delta,
		}

	case "response.output_item.done":
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
			ch <- llm.StreamEvent{
				Type: llm.EventReasoningEnd,
			}
		}

	case "response.completed":
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

	case "response.in_progress", "response.output_text.done",
		"response.content_part.added", "response.content_part.done",
		"response.function_call_arguments.done", "response.reasoning.done",
		"response.reasoning.delta":
		// Events we acknowledge but don't need to act on.

	case "error", "response.failed":
		// API error inside the SSE stream. Surface it so the caller can
		// report it instead of silently returning an empty response.
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
			if msg == "" {
				msg = errEvt.Response.StatusDetails.Error.Message
			}
			if msg == "" {
				msg = fmt.Sprintf("unknown API error (event type: %s)", eventType)
			}
			ch <- llm.StreamEvent{
				Type: llm.EventError,
				Err:  fmt.Errorf("openai: %s", msg),
			}
		}

	default:
		// Unknown event type — emit as raw provider event for debugging.
		ch <- llm.StreamEvent{
			Type: llm.EventProviderEvent,
			Raw:  data,
		}
	}
}
