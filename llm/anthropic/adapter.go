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
	"os"
	"strings"
	"time"

	"github.com/2389-research/tracker/internal/diag"
	"github.com/2389-research/tracker/llm"
)

const (
	defaultBaseURL      = "https://api.anthropic.com"
	anthropicAPIVersion = "2023-06-01"
	messagesPath        = "/v1/messages"
)

// Adapter implements llm.ProviderAdapter for the Anthropic Messages API.
type Adapter struct {
	apiKey       string
	baseURL      string
	httpClient   *http.Client
	extraHeaders map[string]string
	idleTimeout  time.Duration
}

// Option configures an Adapter.
type Option func(*Adapter)

// WithBaseURL overrides the default Anthropic API base URL.
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

// New creates a new Anthropic adapter with the given API key and options.
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
		return nil, llm.ErrorFromStatusCodeRetryAfter(httpResp.StatusCode, string(respBody), "anthropic", llm.ParseRetryAfter(httpResp.Header))
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
	diag.Warnf("[anthropic] WARNING: empty response (0 output tokens, no text, no tool calls) — status=%d stop_reason=%s model=%s request_id=%s raw_length=%d",
		httpResp.StatusCode, resp.FinishReason.Raw, resp.Model, httpResp.Header.Get("Request-Id"), len(respBody))
	if os.Getenv("TRACKER_DEBUG") != "" {
		preview := string(respBody)
		if len(preview) > 200 {
			preview = preview[:200] + "...(truncated)"
		}
		diag.Warnf("[anthropic] raw response preview (%d bytes): %s", len(respBody), preview)
	}
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

	// Internal stream context: the idle guard cancels it when the socket goes
	// silent past the deadline, unblocking the in-flight read (#575).
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	httpReq, err := http.NewRequestWithContext(streamCtx, http.MethodPost, a.baseURL+messagesPath, bytes.NewReader(body))
	if err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: err}
		return
	}
	a.setHeaders(httpReq, req)

	httpResp, err := a.streamClient().Do(httpReq)
	if err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: &llm.NetworkError{SDKError: llm.SDKError{Msg: err.Error(), Cause: err}}}
		return
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(httpResp.Body)
		// Preserve the Retry-After hint on the streaming error path, matching the
		// non-stream Complete path so a traced request retries just as well (#605).
		ch <- llm.StreamEvent{Type: llm.EventError, Err: llm.ErrorFromStatusCodeRetryAfter(httpResp.StatusCode, string(respBody), "anthropic", llm.ParseRetryAfter(httpResp.Header))}
		return
	}

	guard := llm.NewStreamIdleGuard(a.idleTimeout, cancelStream)
	defer guard.Stop()
	a.parseSSE(ctx, httpResp.Body, ch, emitProviderEvents, guard)
}

// streamClient returns an http.Client for streaming: a shallow copy of the
// configured client with the total-request Timeout dropped so a long but
// actively-streaming turn (sonnet reaches ~304.5s) is not severed by the total
// cap — the stream-idle deadline is the streaming bound instead (#575, #577).
func (a *Adapter) streamClient() *http.Client {
	c := *a.httpClient
	c.Timeout = 0
	return &c
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

	// Apply extra headers (e.g., gateway auth tokens).
	for k, v := range a.extraHeaders {
		httpReq.Header.Set(k, v)
	}
}

// parseSSE reads SSE events from the response body and emits StreamEvents.
//
// It uses bufio.Reader.ReadBytes (not bufio.Scanner) so a single SSE `data:`
// line has NO fixed size cap — very large tool_use inputs / thinking deltas
// (which can exceed 1MB) are read in full rather than truncated into a fatal
// "unexpected end of JSON input" that aborts the whole turn (#573).
func (a *Adapter) parseSSE(ctx context.Context, body io.Reader, ch chan<- llm.StreamEvent, emitProviderEvents bool, guard *llm.StreamIdleGuard) {
	reader := bufio.NewReaderSize(body, 256*1024)

	// Track content block types by index for proper event emission.
	blockTypes := make(map[int]string)
	var eventType string
	// Track input usage from message_start to include in finish event.
	var inputUsage *anthropicUsage

	for {
		line, err := llm.ReadSSELine(reader, guard)
		process, stop, transient := classifySSERead(err, ctx, guard)
		if process && len(line) > 0 {
			eventType = a.processSSELine(strings.TrimRight(string(line), "\r\n"), eventType, ch, emitProviderEvents, blockTypes, &inputUsage)
		}
		if transient != nil {
			// A transient mid-stream read failure — surface a RETRYABLE StreamError
			// so the retry middleware re-issues the completion with the accumulated
			// history, resuming the episode rather than re-running the node (#574).
			ch <- llm.StreamEvent{Type: llm.EventError, Err: &llm.StreamError{
				SDKError: llm.SDKError{Msg: fmt.Sprintf("anthropic: SSE read error: %v", transient), Cause: transient},
			}}
		}
		if stop {
			return
		}
	}
}

// classifySSERead interprets a bufio.Reader.ReadBytes error. process reports
// whether the returned line is safe to handle (a full line, or the clean tail at
// EOF — never a partial line left by a transient failure); stop reports whether
// to end the loop; transient is a retryable read failure to surface (nil for a
// clean EOF / caller-cancellation end).
//
// A context error whose cause is the idle guard firing (the caller context is
// still live) is an idle hang: it MUST surface as a retryable ErrStreamIdle, not
// fold into a clean stop — otherwise the channel closes with no error and no
// finish and the turn is silently truncated (#576). A context error while the
// caller context is itself done is a genuine caller/shutdown cancel and stops
// cleanly.
func classifySSERead(err error, callerCtx context.Context, guard *llm.StreamIdleGuard) (process, stop bool, transient error) {
	if err == nil {
		return true, false, nil
	}
	if errors.Is(err, io.EOF) {
		return true, true, nil
	}
	if isContextError(err) {
		if callerCtx.Err() == nil && guard.Fired() {
			return false, true, llm.ErrStreamIdle
		}
		return true, true, nil
	}
	return false, true, err
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
	// Skip empty / keep-alive data lines — dispatching "" to a block-delta handler
	// is exactly the "unexpected end of JSON input" that used to abort the turn (#573).
	if strings.TrimSpace(data) == "" {
		return eventType
	}
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
