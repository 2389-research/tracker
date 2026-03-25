// ABOUTME: Google Gemini API adapter implementing the ProviderAdapter interface.
// ABOUTME: Handles HTTP communication, SSE stream parsing, and request/response lifecycle.
package google

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
	defaultBaseURL = "https://generativelanguage.googleapis.com"
)

// Adapter implements llm.ProviderAdapter for the Google Gemini API.
type Adapter struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures an Adapter.
type Option func(*Adapter)

// WithBaseURL overrides the default Gemini API base URL.
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

// New creates a new Gemini adapter with the given API key and options.
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
	return "gemini"
}

// generateContentURL builds the full URL for a given model.
func (a *Adapter) generateContentURL(model string) string {
	return fmt.Sprintf("%s/v1beta/models/%s:generateContent", a.baseURL, model)
}

// streamGenerateContentURL builds the streaming URL for a given model.
func (a *Adapter) streamGenerateContentURL(model string) string {
	return fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse", a.baseURL, model)
}

// Complete sends a synchronous request to the Gemini API.
func (a *Adapter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	body, err := translateRequest(req)
	if err != nil {
		return nil, fmt.Errorf("google: translate request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.generateContentURL(req.Model), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("google: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", a.apiKey)

	start := time.Now()
	httpResp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, &llm.NetworkError{SDKError: llm.SDKError{Msg: fmt.Sprintf("google: %s", err.Error()), Cause: err}}
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, &llm.NetworkError{SDKError: llm.SDKError{Msg: fmt.Sprintf("google: read response: %s", err.Error()), Cause: err}}
	}

	if httpResp.StatusCode != http.StatusOK {
		msg := string(respBody)
		return nil, llm.ErrorFromStatusCode(httpResp.StatusCode, msg, "gemini")
	}

	resp, err := translateResponse(respBody)
	if err != nil {
		return nil, fmt.Errorf("google: translate response: %w", err)
	}

	resp.Provider = "gemini"
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
			ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("google: translate request: %w", err)}
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.streamGenerateContentURL(req.Model), bytes.NewReader(body))
		if err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: err}
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("x-goog-api-key", a.apiKey)

		httpResp, err := a.httpClient.Do(httpReq)
		if err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: &llm.NetworkError{SDKError: llm.SDKError{Msg: err.Error(), Cause: err}}}
			return
		}
		defer httpResp.Body.Close()

		if httpResp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(httpResp.Body)
			ch <- llm.StreamEvent{Type: llm.EventError, Err: llm.ErrorFromStatusCode(httpResp.StatusCode, string(respBody), "gemini")}
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

// parseSSE reads SSE events from the Gemini streaming response.
// Gemini sends each chunk as a complete JSON object in an SSE data line.
// geminiStreamState tracks mutable state across SSE chunks.
type geminiStreamState struct {
	first      bool
	textActive bool
	textID     string
}

func (a *Adapter) parseSSE(body io.Reader, ch chan<- llm.StreamEvent, emitProviderEvents bool) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	state := &geminiStreamState{first: true, textID: "gemini_text_0"}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := []byte(strings.TrimPrefix(line, "data: "))
		if emitProviderEvents {
			ch <- llm.StreamEvent{Type: llm.EventProviderEvent, Raw: data}
		}

		var chunk geminiResponse
		if err := json.Unmarshal(data, &chunk); err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("google: parse SSE chunk: %w", err)}
			return
		}

		if state.first {
			ch <- llm.StreamEvent{Type: llm.EventStreamStart, Raw: data}
			state.first = false
		}

		if len(chunk.Candidates) == 0 {
			continue
		}

		a.processCandidate(chunk.Candidates[0], &chunk, ch, state)
	}

	if err := scanner.Err(); err != nil && !isContextError(err) {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("google: SSE scan error: %w", err)}
	}
}

// processCandidate handles a single candidate from a Gemini SSE chunk.
func (a *Adapter) processCandidate(candidate geminiCandidate, chunk *geminiResponse, ch chan<- llm.StreamEvent, state *geminiStreamState) {
	for _, part := range candidate.Content.Parts {
		a.processGeminiPart(part, ch, state)
	}

	if candidate.FinishReason != "" {
		if state.textActive {
			ch <- llm.StreamEvent{Type: llm.EventTextEnd, TextID: state.textID}
			state.textActive = false
		}
		fr := translateFinishReason(candidate.FinishReason, hasToolCallParts(candidate.Content.Parts))
		var usage *llm.Usage
		if chunk.UsageMetadata != nil {
			usage = &llm.Usage{
				InputTokens:  chunk.UsageMetadata.PromptTokenCount,
				OutputTokens: chunk.UsageMetadata.CandidatesTokenCount,
				TotalTokens:  chunk.UsageMetadata.TotalTokenCount,
			}
		}
		ch <- llm.StreamEvent{Type: llm.EventFinish, FinishReason: &fr, Usage: usage}
	}
}

// processGeminiPart emits stream events for a single content part.
func (a *Adapter) processGeminiPart(part geminiPart, ch chan<- llm.StreamEvent, state *geminiStreamState) {
	if part.Text != "" {
		if !state.textActive {
			ch <- llm.StreamEvent{Type: llm.EventTextStart, TextID: state.textID}
			state.textActive = true
		}
		ch <- llm.StreamEvent{Type: llm.EventTextDelta, TextID: state.textID, Delta: part.Text}
	}
	if part.FunctionCall != nil {
		if state.textActive {
			ch <- llm.StreamEvent{Type: llm.EventTextEnd, TextID: state.textID}
			state.textActive = false
		}
		argsJSON, _ := json.Marshal(part.FunctionCall.Args)
		ch <- llm.StreamEvent{
			Type: llm.EventToolCallStart,
			ToolCall: &llm.ToolCallData{
				ID:             syntheticID(),
				Name:           part.FunctionCall.Name,
				Arguments:      argsJSON,
				ThoughtSigData: part.ThoughtSignature,
			},
		}
		ch <- llm.StreamEvent{Type: llm.EventToolCallEnd}
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

func hasToolCallParts(parts []geminiPart) bool {
	for _, p := range parts {
		if p.FunctionCall != nil {
			return true
		}
	}
	return false
}
