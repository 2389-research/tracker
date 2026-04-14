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
	"log"
	"net/http"
	"os"
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
		return nil, llm.ErrorFromStatusCode(httpResp.StatusCode, string(respBody), "anthropic")
	}

	resp, err := translateResponse(respBody)
	if err != nil {
		return nil, fmt.Errorf("anthropic: translate response: %w", err)
	}

	resp.Provider = "anthropic"
	resp.Latency = time.Since(start)

	logEmptyResponseIfNeeded(resp, httpResp, respBody)

	return resp, nil
}

// logEmptyResponseIfNeeded logs a warning when the response has no content.
func logEmptyResponseIfNeeded(resp *llm.Response, httpResp *http.Response, respBody []byte) {
	if resp.Usage.OutputTokens != 0 || resp.Text() != "" || len(resp.ToolCalls()) != 0 {
		return
	}
	log.Printf("[anthropic] WARNING: empty response (0 output tokens, no text, no tool calls) — status=%d stop_reason=%s model=%s request_id=%s raw_length=%d",
		httpResp.StatusCode, resp.FinishReason.Raw, resp.Model, httpResp.Header.Get("Request-Id"), len(respBody))
	if os.Getenv("TRACKER_DEBUG") != "" {
		preview := string(respBody)
		if len(preview) > 200 {
			preview = preview[:200] + "...(truncated)"
		}
		log.Printf("[anthropic] raw response preview (%d bytes): %s", len(respBody), preview)
	}
}

// Stream sends a streaming request and returns a channel of events.
func (a *Adapter) Stream(ctx context.Context, req *llm.Request) <-chan llm.StreamEvent {
	ch := make(chan llm.StreamEvent, 64)
	emitProviderEvents := shouldEmitProviderEvents(req)
	go func() {
		defer close(ch)
		a.streamRequest(ctx, req, ch, emitProviderEvents)
	}()
	return ch
}

// streamRequest performs the HTTP request and streams events to ch.
func (a *Adapter) streamRequest(ctx context.Context, req *llm.Request, ch chan<- llm.StreamEvent, emitProviderEvents bool) {
	body, err := buildStreamBody(req)
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
}

// buildStreamBody translates the request to JSON and injects stream:true.
func buildStreamBody(req *llm.Request) ([]byte, error) {
	body, err := translateRequest(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: translate request: %w", err)
	}
	var bodyMap map[string]any
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		return nil, err
	}
	bodyMap["stream"] = true
	return json.Marshal(bodyMap)
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
		eventType = a.processSSELine(scanner.Text(), eventType, ch, emitProviderEvents, blockTypes, &inputUsage)
	}

	if err := scanner.Err(); err != nil && !isContextError(err) {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("anthropic: SSE scan error: %w", err)}
	}
}

// processSSELine handles a single SSE scanner line and returns the (possibly updated) event type.
func (a *Adapter) processSSELine(line, eventType string, ch chan<- llm.StreamEvent, emitProviderEvents bool, blockTypes map[int]string, inputUsage **anthropicUsage) string {
	if strings.HasPrefix(line, "event: ") {
		return strings.TrimPrefix(line, "event: ")
	}
	if !strings.HasPrefix(line, "data: ") {
		return eventType
	}
	data := strings.TrimPrefix(line, "data: ")
	if emitProviderEvents {
		ch <- llm.StreamEvent{Type: llm.EventProviderEvent, Raw: json.RawMessage(data)}
	}
	resolvedType := resolveSSEEventType(eventType, data)
	a.handleSSEData(resolvedType, []byte(data), ch, blockTypes, inputUsage)
	return ""
}

// resolveSSEEventType returns the SSE event type. When no "event:" header preceded
// the data, it falls back to extracting the type from the JSON payload itself.
func resolveSSEEventType(headerType, data string) string {
	if headerType != "" {
		return headerType
	}
	var peek struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(data), &peek) == nil && peek.Type != "" {
		return peek.Type
	}
	return ""
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
		Data string `json:"data,omitempty"` // redacted_thinking opaque blob
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
		Signature   string `json:"signature,omitempty"` // thinking block signature
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
	case "redacted_thinking":
		// Redacted thinking blocks carry an opaque data blob that must be round-tripped.
		ch <- llm.StreamEvent{Type: llm.EventRedactedThinking, ReasoningSignature: evt.ContentBlock.Data}
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
