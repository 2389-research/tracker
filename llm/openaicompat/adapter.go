// ABOUTME: OpenAI Chat Completions compatible adapter implementing the ProviderAdapter interface.
// ABOUTME: Handles HTTP transport, SSE stream parsing with tool call accumulation, and error mapping.
package openaicompat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/2389-research/tracker/llm"
)

const (
	defaultBaseURL   = "https://openrouter.ai/api"
	chatCompletePath = "/v1/chat/completions"
	// maxResponseSize caps response body reads to prevent OOM from malicious servers.
	maxResponseSize = 10 * 1024 * 1024 // 10MB
)

// Adapter implements llm.ProviderAdapter for any OpenAI Chat Completions compatible API.
type Adapter struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures an Adapter.
type Option func(*Adapter)

// WithBaseURL overrides the default API base URL.
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

// New creates a new OpenAI-compatible adapter with the given API key and options.
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
	// Normalize base URL: strip trailing /v1 suffix since chatCompletePath
	// already includes the /v1 prefix. Environment variables conventionally
	// include /v1 (e.g. http://localhost:9999/v1), which would cause a double
	// /v1/v1 path without this normalization.
	a.baseURL = strings.TrimSuffix(a.baseURL, "/v1")
	return a
}

// Name returns the provider identifier.
func (a *Adapter) Name() string {
	return "openai-compat"
}

// Complete sends a synchronous request to the Chat Completions API.
func (a *Adapter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	body, err := translateRequest(req, false)
	if err != nil {
		return nil, fmt.Errorf("openai-compat: translate request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+chatCompletePath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai-compat: create request: %w", err)
	}
	a.setHeaders(httpReq)

	start := time.Now()
	httpResp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, &llm.NetworkError{SDKError: llm.SDKError{Msg: fmt.Sprintf("openai-compat: %s", err.Error()), Cause: err}}
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, maxResponseSize+1))
	if err != nil {
		return nil, &llm.NetworkError{SDKError: llm.SDKError{Msg: fmt.Sprintf("openai-compat: read response: %s", err.Error()), Cause: err}}
	}
	if int64(len(respBody)) > maxResponseSize {
		return nil, &llm.InvalidRequestError{ProviderError: llm.ProviderError{
			SDKError: llm.SDKError{Msg: "openai-compat: response body exceeds 10MB limit"},
			Provider: "openai-compat",
		}}
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, llm.ErrorFromStatusCode(httpResp.StatusCode, string(respBody), "openai-compat")
	}

	resp, err := translateResponse(respBody)
	if err != nil {
		return nil, fmt.Errorf("openai-compat: translate response: %w", err)
	}

	resp.Provider = "openai-compat"
	resp.Latency = time.Since(start)

	return resp, nil
}

// Stream sends a streaming request and returns a channel of events.
func (a *Adapter) Stream(ctx context.Context, req *llm.Request) <-chan llm.StreamEvent {
	ch := make(chan llm.StreamEvent, 64)

	go func() {
		defer close(ch)

		body, err := translateRequest(req, true)
		if err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai-compat: translate request: %w", err)}
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+chatCompletePath, bytes.NewReader(body))
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
			respBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, maxResponseSize))
			ch <- llm.StreamEvent{Type: llm.EventError, Err: llm.ErrorFromStatusCode(httpResp.StatusCode, string(respBody), "openai-compat")}
			return
		}

		a.parseSSE(httpResp.Body, ch)
	}()

	return ch
}

// Close releases resources held by the adapter.
func (a *Adapter) Close() error {
	return nil
}

// setHeaders applies standard Chat Completions API headers to the request.
func (a *Adapter) setHeaders(httpReq *http.Request) {
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
}

// sseToolCallAccum tracks a tool call being streamed across multiple SSE deltas.
type sseToolCallAccum struct {
	ID   string
	Name string
	Args strings.Builder
}

// sseState holds the mutable state threaded through SSE event processing.
type sseState struct {
	firstChunk     bool
	textStarted    bool
	toolCalls      map[int]*sseToolCallAccum
	deferredFinish *llm.StreamEvent
}

// parseSSE reads SSE events from the Chat Completions response body and emits
// StreamEvents. Chat Completions SSE format uses "data: {JSON}" lines with a
// "data: [DONE]" sentinel.
func (a *Adapter) parseSSE(body io.Reader, ch chan<- llm.StreamEvent) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	st := &sseState{
		firstChunk: true,
		toolCalls:  make(map[int]*sseToolCallAccum),
	}

	for scanner.Scan() {
		if done := a.processSSELine(scanner.Text(), st, ch); done {
			break
		}
	}

	if err := scanner.Err(); err != nil && !isContextError(err) {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai-compat: SSE scan error: %w", err)}
	}
}

// processSSELine processes a single SSE scan line and returns true when [DONE] is reached.
func (a *Adapter) processSSELine(line string, st *sseState, ch chan<- llm.StreamEvent) bool {
	if !strings.HasPrefix(line, "data: ") {
		return false
	}
	data := strings.TrimPrefix(line, "data: ")
	if data == "[DONE]" {
		a.handleSSEDone(st, ch)
		return true
	}
	return a.processSSEDataPayload(data, st, ch)
}

// processSSEDataPayload parses and dispatches a non-DONE SSE data payload.
// Returns false always (only [DONE] terminates the stream).
func (a *Adapter) processSSEDataPayload(data string, st *sseState, ch chan<- llm.StreamEvent) bool {
	if strings.Contains(data, `"error"`) && a.tryEmitSSEError(data, ch) {
		return false
	}

	var chunk chatStreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai-compat: parse SSE chunk: %w", err)}
		return false
	}

	if st.firstChunk {
		ch <- llm.StreamEvent{Type: llm.EventStreamStart, Raw: json.RawMessage(data)}
		st.firstChunk = false
	}

	if len(chunk.Choices) > 0 {
		a.handleSSEChoice(chunk, st, ch)
	} else if chunk.Usage != nil {
		a.handleSSEUsageOnly(chunk, st)
	}
	return false
}

// handleSSEDone handles the [DONE] sentinel: emits text end, tool call ends, then deferred finish.
func (a *Adapter) handleSSEDone(st *sseState, ch chan<- llm.StreamEvent) {
	if st.textStarted {
		ch <- llm.StreamEvent{Type: llm.EventTextEnd, TextID: "text"}
	}
	a.emitAccumulatedToolCallEnds(st.toolCalls, ch)
	if st.deferredFinish != nil {
		ch <- *st.deferredFinish
	}
}

// tryEmitSSEError attempts to parse and emit an error embedded in the SSE stream.
// Returns true if an error was found and emitted.
func (a *Adapter) tryEmitSSEError(data string, ch chan<- llm.StreamEvent) bool {
	var errChunk chatStreamError
	if err := json.Unmarshal([]byte(data), &errChunk); err == nil && errChunk.Error.Message != "" {
		ch <- llm.StreamEvent{
			Type: llm.EventError,
			Err:  sseErrorToTyped(errChunk.Error.Code, errChunk.Error.Message),
		}
		return true
	}
	return false
}

// handleSSEChoice processes a chunk that contains choices (text or tool call deltas).
func (a *Adapter) handleSSEChoice(chunk chatStreamChunk, st *sseState, ch chan<- llm.StreamEvent) {
	choice := chunk.Choices[0]

	if choice.Delta.Content != "" {
		if !st.textStarted {
			ch <- llm.StreamEvent{Type: llm.EventTextStart, TextID: "text"}
			st.textStarted = true
		}
		ch <- llm.StreamEvent{Type: llm.EventTextDelta, TextID: "text", Delta: choice.Delta.Content}
	}

	for _, tc := range choice.Delta.ToolCalls {
		a.handleSSEToolCallDelta(tc, st, ch)
	}

	if choice.FinishReason != nil {
		a.handleSSEFinishReason(choice, chunk, st)
	}
}

// handleSSEToolCallDelta processes a single tool call delta, creating or updating the accumulator.
func (a *Adapter) handleSSEToolCallDelta(tc chatStreamToolCall, st *sseState, ch chan<- llm.StreamEvent) {
	accum, exists := st.toolCalls[tc.Index]
	if !exists {
		accum = &sseToolCallAccum{ID: tc.ID, Name: tc.Function.Name}
		st.toolCalls[tc.Index] = accum
		ch <- llm.StreamEvent{
			Type:     llm.EventToolCallStart,
			ToolCall: &llm.ToolCallData{ID: tc.ID, Name: tc.Function.Name},
		}
	}
	if tc.Function.Arguments != "" {
		accum.Args.WriteString(tc.Function.Arguments)
		ch <- llm.StreamEvent{Type: llm.EventToolCallDelta, Delta: tc.Function.Arguments}
	}
}

// handleSSEFinishReason builds and defers the finish event until [DONE].
func (a *Adapter) handleSSEFinishReason(choice chatStreamChoice, chunk chatStreamChunk, st *sseState) {
	fr := translateFinishReason(*choice.FinishReason, len(st.toolCalls) > 0)
	evt := llm.StreamEvent{Type: llm.EventFinish, FinishReason: &fr}
	if chunk.Usage != nil {
		usage := translateUsage(*chunk.Usage)
		evt.Usage = &usage
	}
	st.deferredFinish = &evt
}

// handleSSEUsageOnly processes a usage-only chunk (no choices), updating the deferred finish.
func (a *Adapter) handleSSEUsageOnly(chunk chatStreamChunk, st *sseState) {
	usage := translateUsage(*chunk.Usage)
	if st.deferredFinish != nil {
		st.deferredFinish.Usage = &usage
	} else {
		st.deferredFinish = &llm.StreamEvent{Type: llm.EventFinish, Usage: &usage}
	}
}

// emitAccumulatedToolCallEnds emits EventToolCallEnd for each accumulated tool
// call in index order, with the complete ID, name, and assembled arguments.
func (a *Adapter) emitAccumulatedToolCallEnds(toolCalls map[int]*sseToolCallAccum, ch chan<- llm.StreamEvent) {
	indices := make([]int, 0, len(toolCalls))
	for idx := range toolCalls {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	for _, idx := range indices {
		accum := toolCalls[idx]
		ch <- llm.StreamEvent{
			Type: llm.EventToolCallEnd,
			ToolCall: &llm.ToolCallData{
				ID:        accum.ID,
				Name:      accum.Name,
				Arguments: json.RawMessage(accum.Args.String()),
			},
		}
	}
}

// isContextError returns true for context cancellation/deadline errors that
// are expected during normal shutdown and should not surface as SSE errors.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// sseErrorToTyped maps an error code from an SSE stream event to a typed error.
func sseErrorToTyped(code, message string) error {
	base := llm.ProviderError{
		SDKError:  llm.SDKError{Msg: "openai-compat: " + message},
		Provider:  "openai-compat",
		ErrorCode: code,
	}
	if err := sseErrorToTypedAuthQuota(code, base); err != nil {
		return err
	}
	return sseErrorToTypedRequestServer(code, base)
}

// sseErrorToTypedAuthQuota maps auth, quota, and not-found error codes to typed errors.
func sseErrorToTypedAuthQuota(code string, base llm.ProviderError) error {
	switch code {
	case "insufficient_quota":
		return &llm.QuotaExceededError{ProviderError: base}
	case "invalid_api_key", "authentication_error":
		return &llm.AuthenticationError{ProviderError: base}
	case "model_not_found":
		return &llm.NotFoundError{ProviderError: base}
	}
	return nil
}

// sseErrorToTypedRequestServer maps request, content, rate-limit, and server error codes.
func sseErrorToTypedRequestServer(code string, base llm.ProviderError) error {
	switch code {
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

// --- SSE chunk types for the Chat Completions streaming format ---

// chatStreamChunk represents a single SSE data payload in Chat Completions streaming.
type chatStreamChunk struct {
	ID      string             `json:"id"`
	Choices []chatStreamChoice `json:"choices"`
	Usage   *chatUsage         `json:"usage,omitempty"`
}

// chatStreamChoice represents a choice in a streaming chunk.
type chatStreamChoice struct {
	Index        int             `json:"index"`
	Delta        chatStreamDelta `json:"delta"`
	FinishReason *string         `json:"finish_reason"`
}

// chatStreamDelta holds the incremental content in a streaming chunk.
type chatStreamDelta struct {
	Role      string               `json:"role,omitempty"`
	Content   string               `json:"content,omitempty"`
	ToolCalls []chatStreamToolCall `json:"tool_calls,omitempty"`
}

// chatStreamToolCall represents a tool call delta in a streaming chunk.
type chatStreamToolCall struct {
	Index    int                    `json:"index"`
	ID       string                 `json:"id,omitempty"`
	Type     string                 `json:"type,omitempty"`
	Function chatStreamFunctionCall `json:"function"`
}

// chatStreamFunctionCall holds incremental function call data in a streaming chunk.
type chatStreamFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// chatStreamError represents an error object embedded in a 200 SSE stream.
type chatStreamError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}
