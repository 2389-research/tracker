// ABOUTME: OpenAI Responses API adapter implementing the ProviderAdapter interface.
// ABOUTME: Handles HTTP communication, SSE stream parsing, and request/response lifecycle.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	idleTimeout  time.Duration
	chatgpt      *chatgptMode // set by WithChatGPTAuth; nil in API-key mode
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

// WithStreamIdleTimeout overrides the stream-idle deadline: the maximum time a
// streaming SSE socket may go byte-silent before it is cancelled and surfaced as
// a retryable error (#575). A non-positive value disables the guard.
func WithStreamIdleTimeout(d time.Duration) Option {
	return func(a *Adapter) {
		a.idleTimeout = d
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
		idleTimeout: llm.DefaultStreamIdleTimeout,
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

// Complete sends a synchronous request to the OpenAI Responses API. In
// ChatGPT mode it streams instead, since that backend only streams.
func (a *Adapter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if a.chatgpt != nil {
		return a.completeChatGPT(ctx, req)
	}
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
	resp.RateLimit = openaiRateLimit(httpResp.Header)

	return resp, nil
}

// openaiRateLimit reads OpenAI's x-ratelimit-* response headers into a
// RateLimitInfo (nil when absent). The reset headers are durations relative to
// now (e.g. "1s", "6m0s").
func openaiRateLimit(h http.Header) *llm.RateLimitInfo {
	return llm.RateLimitFromHeaders(h,
		"x-ratelimit-remaining-requests", "x-ratelimit-limit-requests",
		"x-ratelimit-remaining-tokens", "x-ratelimit-limit-tokens",
		"x-ratelimit-reset-requests", llm.ResetDuration)
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
	body, err := a.streamBody(req)
	if err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: err}
		return
	}

	llm.EmitRequestSent(ch, body, emitProviderEvents)

	// Internal stream context: the idle guard cancels it when the socket goes
	// silent past the deadline, unblocking the in-flight read (#575).
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	httpResp, err := a.sendStream(streamCtx, body)
	if err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: err}
		return
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(httpResp.Body)
		ch <- llm.StreamEvent{Type: llm.EventError, Err: a.statusError(httpResp.StatusCode, respBody, httpResp.Header)}
		return
	}

	// Carry rate-limit headers (available before the SSE body) so a traced
	// completion surfaces the same RateLimit as the non-stream Complete path
	// (#617); the accumulator merges it with the later response.created id/model.
	if rl := openaiRateLimit(httpResp.Header); rl != nil {
		ch <- llm.StreamEvent{Type: llm.EventStreamStart, FullResponse: &llm.Response{RateLimit: rl}}
	}

	guard := llm.NewStreamIdleGuard(a.idleTimeout, cancelStream)
	defer guard.Stop()
	a.parseSSE(ctx, httpResp.Body, ch, emitProviderEvents, guard)
}

// streamClient returns an http.Client for streaming: a shallow copy of the
// configured client with the total-request Timeout dropped so a long but
// actively-streaming turn (sonnet reaches ~304.5s) is not severed by the total
// cap — the stream-idle deadline is the streaming bound instead (#575, #577).
// In ChatGPT mode the copy also refuses redirects (refuseRedirect), so a 3xx
// surfaces as a status; the caller's own client is never changed.
func (a *Adapter) streamClient() *http.Client {
	c := *a.httpClient
	c.Timeout = 0
	if a.chatgpt != nil {
		c.CheckRedirect = refuseRedirect
	}
	return &c
}

// streamBody builds the streaming request body for the adapter's mode.
func (a *Adapter) streamBody(req *llm.Request) ([]byte, error) {
	if a.chatgpt != nil {
		return a.chatgptBody(req)
	}
	return buildStreamBody(req)
}

// sendStream posts a streaming request body: through sendChatGPT in ChatGPT
// mode, otherwise with the API key and any gateway headers.
func (a *Adapter) sendStream(ctx context.Context, body []byte) (*http.Response, error) {
	if a.chatgpt != nil {
		return a.sendChatGPT(ctx, body)
	}
	return a.postStream(ctx, a.baseURL+responsesPath, body, a.setHeaders)
}

// postStream posts body to target with the streaming client after
// setHeaders runs. A failed send is a retryable *llm.NetworkError.
func (a *Adapter) postStream(ctx context.Context, target string, body []byte, setHeaders func(*http.Request)) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	setHeaders(httpReq)
	httpResp, err := a.streamClient().Do(httpReq)
	if err != nil {
		return nil, &llm.NetworkError{SDKError: llm.SDKError{Msg: err.Error(), Cause: err}}
	}
	return httpResp, nil
}

// statusError maps a non-200 streaming reply for the adapter's mode. In both
// modes a retryable error keeps the Retry-After hint, matching the non-stream
// Complete path so a traced request retries just as well (#605).
func (a *Adapter) statusError(status int, body []byte, h http.Header) error {
	if a.chatgpt != nil {
		return chatgptStatusError(status, body, h)
	}
	return llm.ErrorFromStatusCodeRetryAfter(status, string(body), "openai", llm.ParseRetryAfter(h))
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
func (a *Adapter) parseSSE(ctx context.Context, body io.Reader, ch chan<- llm.StreamEvent, emitProviderEvents bool, guard *llm.StreamIdleGuard) {
	reader := bufio.NewReaderSize(body, 256*1024)

	var eventType string

	for {
		line, err := llm.ReadSSELine(reader, guard)
		process, stop, transient := llm.ClassifySSERead(ctx, err, guard)
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
	resolvedType := llm.ResolveSSEEventType(eventType, data)
	a.handleSSEData(resolvedType, []byte(data), ch)
	return ""
}
