// ABOUTME: Anthropic Messages API adapter implementing the ProviderAdapter interface.
// ABOUTME: Handles HTTP communication, SSE stream parsing, and request/response lifecycle.
package anthropic

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
	defaultBaseURL      = "https://api.anthropic.com"
	anthropicAPIVersion = "2023-06-01"
	messagesPath        = "/v1/messages"
)

// Adapter implements llm.ProviderAdapter for the Anthropic Messages API.
type Adapter struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures an Adapter.
type Option func(*Adapter)

// WithBaseURL overrides the default Anthropic API base URL.
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

// New creates a new Anthropic adapter with the given API key and options.
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
	return a
}

// Name returns the provider identifier.
func (a *Adapter) Name() string {
	return "anthropic"
}

// Complete sends a synchronous request to the Anthropic Messages API.
func (a *Adapter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	body, err := translateRequest(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: translate request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+messagesPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: create request: %w", err)
	}
	a.setHeaders(httpReq, req)

	start := time.Now()
	httpResp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, &llm.NetworkError{SDKError: llm.SDKError{Msg: fmt.Sprintf("anthropic: %s", err.Error()), Cause: err}}
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, &llm.NetworkError{SDKError: llm.SDKError{Msg: fmt.Sprintf("anthropic: read response: %s", err.Error()), Cause: err}}
	}

	if httpResp.StatusCode != http.StatusOK {
		msg := string(respBody)
		return nil, llm.ErrorFromStatusCode(httpResp.StatusCode, msg, "anthropic")
	}

	resp, err := translateResponse(respBody)
	if err != nil {
		return nil, fmt.Errorf("anthropic: translate response: %w", err)
	}

	resp.Provider = "anthropic"
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
			ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("anthropic: translate request: %w", err)}
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

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+messagesPath, bytes.NewReader(body))
		if err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: err}
			return
		}
		a.setHeaders(httpReq, req)

		httpResp, err := a.httpClient.Do(httpReq)
		if err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: &llm.NetworkError{SDKError: llm.SDKError{Msg: err.Error(), Cause: err}}}
			return
		}
		defer httpResp.Body.Close()

		if httpResp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(httpResp.Body)
			ch <- llm.StreamEvent{Type: llm.EventError, Err: llm.ErrorFromStatusCode(httpResp.StatusCode, string(respBody), "anthropic")}
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

// setHeaders applies standard Anthropic API headers to the request.
func (a *Adapter) setHeaders(httpReq *http.Request, req *llm.Request) {
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

	// Collect and apply beta headers (user-specified + auto-injected).
	if beta := collectBetaHeaders(req); beta != "" {
		httpReq.Header.Set("anthropic-beta", beta)
	}
}

// parseSSE reads SSE events from the response body and emits StreamEvents.
func (a *Adapter) parseSSE(body io.Reader, ch chan<- llm.StreamEvent, emitProviderEvents bool) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024) // Allow up to 1MB lines for large tool call args/thinking blocks.

	// Track content block types by index for proper event emission.
	blockTypes := make(map[int]string)
	var eventType string
	// Track input usage from message_start to include in finish event.
	var inputUsage *anthropicUsage

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
		// from the JSON payload itself. Some servers omit SSE event lines
		// and embed the type in the data object.
		resolvedType := eventType
		if resolvedType == "" {
			var peek struct {
				Type string `json:"type"`
			}
			if json.Unmarshal([]byte(data), &peek) == nil && peek.Type != "" {
				resolvedType = peek.Type
			}
		}
		a.handleSSEData(resolvedType, []byte(data), ch, blockTypes, &inputUsage)
		eventType = ""
	}

	if err := scanner.Err(); err != nil && !isContextError(err) {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("anthropic: SSE scan error: %w", err)}
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

// sseMessageStart is the top-level message_start event.
type sseMessageStart struct {
	Type    string `json:"type"`
	Message struct {
		ID    string         `json:"id"`
		Model string         `json:"model"`
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
}

// sseContentBlockStart signals a new content block.
type sseContentBlockStart struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id,omitempty"`
		Name string `json:"name,omitempty"`
		Text string `json:"text,omitempty"`
	} `json:"content_block"`
}

// sseContentBlockDelta carries incremental content.
type sseContentBlockDelta struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text,omitempty"`
		PartialJSON string `json:"partial_json,omitempty"`
		Thinking    string `json:"thinking,omitempty"`
	} `json:"delta"`
}

// sseContentBlockStop signals end of a content block.
type sseContentBlockStop struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

// sseMessageDelta carries final message-level metadata.
type sseMessageDelta struct {
	Type  string `json:"type"`
	Delta struct {
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// handleSSEData processes a single SSE data payload.
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
	case "message_stop", "ping":
		// No action needed.
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
	}
}

func (a *Adapter) handleSSEBlockDelta(data []byte, ch chan<- llm.StreamEvent) {
	var evt sseContentBlockDelta
	if err := json.Unmarshal(data, &evt); err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("anthropic: parse content_block_delta: %w", err)}
		return
	}
	switch evt.Delta.Type {
	case "text_delta":
		ch <- llm.StreamEvent{Type: llm.EventTextDelta, TextID: fmt.Sprintf("block_%d", evt.Index), Delta: evt.Delta.Text}
	case "input_json_delta":
		ch <- llm.StreamEvent{Type: llm.EventToolCallDelta, Delta: evt.Delta.PartialJSON}
	case "thinking_delta":
		ch <- llm.StreamEvent{Type: llm.EventReasoningDelta, ReasoningDelta: evt.Delta.Thinking}
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
		usage.TotalTokens = (*inputUsage).InputTokens + evt.Usage.OutputTokens
		if (*inputUsage).CacheReadInputTokens > 0 {
			v := (*inputUsage).CacheReadInputTokens
			usage.CacheReadTokens = &v
		}
		if (*inputUsage).CacheCreationInputTokens > 0 {
			v := (*inputUsage).CacheCreationInputTokens
			usage.CacheWriteTokens = &v
		}
	}
	ch <- llm.StreamEvent{Type: llm.EventFinish, FinishReason: &fr, Usage: &usage}
}
