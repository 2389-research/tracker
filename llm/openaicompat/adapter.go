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

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, &llm.NetworkError{SDKError: llm.SDKError{Msg: fmt.Sprintf("openai-compat: read response: %s", err.Error()), Cause: err}}
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
			respBody, _ := io.ReadAll(httpResp.Body)
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

// parseSSE reads SSE events from the Chat Completions response body and emits
// StreamEvents. Chat Completions SSE format uses "data: {JSON}" lines with a
// "data: [DONE]" sentinel.
func (a *Adapter) parseSSE(body io.Reader, ch chan<- llm.StreamEvent) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	firstChunk := true
	// Tool call accumulators keyed by tool_calls array index.
	toolCalls := make(map[int]*sseToolCallAccum)
	// Deferred finish event — held until [DONE] so tool call ends emit first.
	var deferredFinish *llm.StreamEvent

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		// [DONE] sentinel: emit tool call ends, then deferred finish.
		if data == "[DONE]" {
			a.emitAccumulatedToolCallEnds(toolCalls, ch)
			if deferredFinish != nil {
				ch <- *deferredFinish
			}
			break
		}

		// Parse the JSON chunk.
		var chunk chatStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai-compat: parse SSE chunk: %w", err)}
			continue
		}

		// Emit stream_start on first chunk.
		if firstChunk {
			ch <- llm.StreamEvent{Type: llm.EventStreamStart, Raw: json.RawMessage(data)}
			firstChunk = false
		}

		// Process choices.
		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]

			// Text content delta.
			if choice.Delta.Content != "" {
				ch <- llm.StreamEvent{
					Type:  llm.EventTextDelta,
					Delta: choice.Delta.Content,
				}
			}

			// Tool call deltas.
			for _, tc := range choice.Delta.ToolCalls {
				accum, exists := toolCalls[tc.Index]
				if !exists {
					// First appearance of this index: new tool call.
					accum = &sseToolCallAccum{
						ID:   tc.ID,
						Name: tc.Function.Name,
					}
					toolCalls[tc.Index] = accum
					ch <- llm.StreamEvent{
						Type: llm.EventToolCallStart,
						ToolCall: &llm.ToolCallData{
							ID:   tc.ID,
							Name: tc.Function.Name,
						},
					}
				}
				// Append argument deltas.
				if tc.Function.Arguments != "" {
					accum.Args.WriteString(tc.Function.Arguments)
					ch <- llm.StreamEvent{
						Type:  llm.EventToolCallDelta,
						Delta: tc.Function.Arguments,
					}
				}
			}

			// finish_reason present: defer EventFinish until after tool call ends.
			if choice.FinishReason != nil {
				fr := translateFinishReason(*choice.FinishReason, len(toolCalls) > 0)
				evt := llm.StreamEvent{
					Type:         llm.EventFinish,
					FinishReason: &fr,
				}
				if chunk.Usage != nil {
					usage := translateUsage(*chunk.Usage)
					evt.Usage = &usage
				}
				deferredFinish = &evt
			}
		} else if chunk.Usage != nil {
			// Usage-only chunk (no choices): update deferred finish or create one.
			usage := translateUsage(*chunk.Usage)
			if deferredFinish != nil {
				deferredFinish.Usage = &usage
			} else {
				deferredFinish = &llm.StreamEvent{
					Type:  llm.EventFinish,
					Usage: &usage,
				}
			}
		}
	}

	if err := scanner.Err(); err != nil && !isContextError(err) {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai-compat: SSE scan error: %w", err)}
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
