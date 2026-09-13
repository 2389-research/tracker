// ABOUTME: Streams compatible completions under an idle deadline instead of an absolute time cap.
// ABOUTME: Preserves completion-marker validation and reports silent hangs as retryable errors.
package openaicompat

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/2389-research/tracker/llm"
)

// WithStreamIdleTimeout bounds raw-byte silence during a streaming request.
// A non-positive duration disables the idle guard; caller cancellation still applies.
func WithStreamIdleTimeout(d time.Duration) Option {
	return func(a *Adapter) { a.idleTimeout = d }
}

// Stream sends a streaming request and returns a channel of events.
func (a *Adapter) Stream(ctx context.Context, req *llm.Request) <-chan llm.StreamEvent {
	ch := make(chan llm.StreamEvent, 64)
	go func() {
		defer close(ch)
		a.runStream(ctx, req, ch)
	}()
	return ch
}

func (a *Adapter) runStream(ctx context.Context, req *llm.Request, ch chan<- llm.StreamEvent) {
	body, err := translateRequest(req, true)
	if err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai-compat: translate request: %w", err)}
		return
	}
	llm.EmitRequestSent(ch, body, llm.RequestIsTraced(req))
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	guard := llm.NewStreamIdleGuard(a.idleTimeout, cancel)
	defer guard.Stop()
	httpReq, err := http.NewRequestWithContext(streamCtx, http.MethodPost, a.baseURL+chatCompletePath, bytes.NewReader(body))
	if err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: err}
		return
	}
	a.setHeaders(httpReq)
	resp, err := a.streamClient().Do(httpReq)
	if err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: streamRequestError(ctx, guard, err)}
		return
	}
	defer func() { _ = resp.Body.Close() }()
	guard.Reset()
	bodyReader := &streamProgressReader{reader: resp.Body, guard: guard}
	if resp.StatusCode != http.StatusOK {
		response, _ := io.ReadAll(io.LimitReader(bodyReader, maxResponseSize))
		ch <- llm.StreamEvent{Type: llm.EventError, Err: llm.ErrorFromStatusCodeRetryAfter(resp.StatusCode, string(response), "openai-compat", llm.ParseRetryAfter(resp.Header))}
		return
	}
	a.parseSSE(ctx, bodyReader, ch, guard)
}

// streamClient copies configuration without changing the non-streaming timeout,
// transport, redirect policy, or cookie jar. Active streams use the idle guard.
func (a *Adapter) streamClient() *http.Client {
	client := *a.httpClient
	client.Timeout = 0
	return &client
}

type streamProgressReader struct {
	reader io.Reader
	guard  *llm.StreamIdleGuard
}

func (r *streamProgressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.guard.Reset()
	}
	return n, err
}

func streamRequestError(ctx context.Context, guard *llm.StreamIdleGuard, err error) error {
	if ctx.Err() == nil && guard.Fired() {
		return &llm.StreamError{SDKError: llm.SDKError{Msg: "openai-compat: " + llm.ErrStreamIdle.Error(), Cause: llm.ErrStreamIdle}}
	}
	return &llm.NetworkError{SDKError: llm.SDKError{Msg: err.Error(), Cause: err}}
}

// parseSSE keeps the existing 1MiB line cap and requires the [DONE] sentinel.
// Raw bytes reset the idle guard even during partial lines or reasoning frames.
func (a *Adapter) parseSSE(ctx context.Context, body io.Reader, ch chan<- llm.StreamEvent, guard *llm.StreamIdleGuard) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	st := &sseState{firstChunk: true, toolCalls: make(map[int]*sseToolCallAccum)}
	for scanner.Scan() {
		if done := a.processSSELine(scanner.Text(), st, ch); done {
			break
		}
	}
	if err := streamReadError(ctx, guard, scanner.Err(), st.sawDone); err != nil {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: err}
	}
}

func streamReadError(ctx context.Context, guard *llm.StreamIdleGuard, err error, sawDone bool) error {
	if ctx.Err() == nil && guard.Fired() {
		return streamRequestError(ctx, guard, err)
	}
	if err != nil && !isContextError(err) {
		return fmt.Errorf("openai-compat: SSE scan error: %w", err)
	}
	if !sawDone {
		return fmt.Errorf("openai-compat: stream ended before completion ([DONE] not received) — response truncated")
	}
	return nil
}
