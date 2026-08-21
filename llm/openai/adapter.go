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
	apiKey       string
	baseURL      string
	httpClient   *http.Client
	extraHeaders map[string]string
}

// Option configures an Adapter.
type Option func(*Adapter)

// WithBaseURL overrides the default OpenAI API base URL.
func WithBaseURL(url string) Option {
	return func(a *Adapter) {
		a.baseURL = url
	}
}

// WithExtraHeaders adds custom headers to every request. Useful for gateway
// authentication (e.g., cf-aig-token for Cloudflare AI Gateway).
func WithExtraHeaders(headers map[string]string) Option {
	return func(a *Adapter) {
		a.extraHeaders = headers
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
		return nil, llm.ErrorFromStatusCodeRetryAfter(httpResp.StatusCode, msg, "openai", llm.ParseRetryAfter(httpResp.Header))
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
	emitProviderEvents := llm.RequestIsTraced(req)
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

	llm.EmitRequestSent(ch, body, emitProviderEvents)

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
}

// buildStreamBody translates the request to JSON and injects stream:true.
func buildStreamBody(req *llm.Request) ([]byte, error) {
	body, err := translateRequest(req)
	if err != nil {
		return nil, fmt.Errorf("openai: translate request: %w", err)
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

// setHeaders applies standard OpenAI API headers to the request.
func (a *Adapter) setHeaders(httpReq *http.Request) {
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

	// Apply extra headers (e.g., gateway auth tokens).
	for k, v := range a.extraHeaders {
		httpReq.Header.Set(k, v)
	}
}

// parseSSE reads SSE events from the response body and emits StreamEvents.
//
// It uses bufio.Reader.ReadBytes (not bufio.Scanner) so a single SSE `data:`
// line has NO fixed size cap — a very large delta is read in full rather than
// truncated into a fatal parse error that aborts the turn (#573).
func (a *Adapter) parseSSE(body io.Reader, ch chan<- llm.StreamEvent, emitProviderEvents bool) {
	reader := bufio.NewReaderSize(body, 256*1024)

	var eventType string

	for {
		line, err := reader.ReadBytes('\n')
		process, stop, transient := classifySSERead(err)
		if process && len(line) > 0 {
			eventType = a.processSSELine(strings.TrimRight(string(line), "\r\n"), eventType, ch, emitProviderEvents)
		}
		if transient != nil {
			// A transient mid-stream read failure — surface a RETRYABLE StreamError
			// so the completion is retried with accumulated context (#574).
			ch <- llm.StreamEvent{Type: llm.EventError, Err: &llm.StreamError{
				SDKError: llm.SDKError{Msg: fmt.Sprintf("openai: SSE read error: %v", transient), Cause: transient},
			}}
		}
		if stop {
			return
		}
	}
}

// classifySSERead interprets a bufio.Reader.ReadBytes error. process reports
// whether the returned line is safe to handle; stop reports whether to end the
// loop; transient is a retryable read failure to surface (nil for a clean
// EOF / context-cancellation end).
func classifySSERead(err error) (process, stop bool, transient error) {
	if err == nil {
		return true, false, nil
	}
	if errors.Is(err, io.EOF) || isContextError(err) {
		return true, true, nil
	}
	return false, true, err
}

// processSSELine handles a single SSE scanner line and returns the (possibly updated) event type.
func (a *Adapter) processSSELine(line, eventType string, ch chan<- llm.StreamEvent, emitProviderEvents bool) string {
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
	a.handleSSEData(resolvedType, []byte(data), ch)
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
