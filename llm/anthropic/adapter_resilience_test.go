// ABOUTME: Regression tests for streaming resilience — a single truncated /
// ABOUTME: over-long / empty content_block_delta must not abort the turn (#573).
package anthropic

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// sseServer streams the given raw SSE event blocks and closes.
func sseServer(t *testing.T, events []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}
		for _, evt := range events {
			fmt.Fprintf(w, "%s\n\n", evt)
			flusher.Flush()
		}
	}))
}

func collect(a *Adapter, t *testing.T) (text strings.Builder, finished bool, fatal error) {
	t.Helper()
	ch := a.Stream(context.Background(), &llm.Request{Model: "claude-opus-4-6", Messages: []llm.Message{llm.UserMessage("Hi")}})
	for evt := range ch {
		switch evt.Type {
		case llm.EventTextDelta:
			text.WriteString(evt.Delta)
		case llm.EventFinish:
			finished = true
		case llm.EventError:
			fatal = evt.Err
		}
	}
	return
}

// TestStream_OverLongDelta: a single content_block_delta larger than the old 1MB
// scanner cap must be delivered in full, not truncated into a fatal
// "unexpected end of JSON input" that aborts the turn (#573).
func TestStream_OverLongDelta(t *testing.T) {
	big := strings.Repeat("x", 2*1024*1024) // 2MB — well over the old 1MB line cap
	events := []string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"m","model":"claude-opus-4-6","role":"assistant","content":[],"usage":{"input_tokens":5,"output_tokens":0}}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + big + `"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}
	srv := sseServer(t, events)
	defer srv.Close()
	text, finished, fatal := collect(New("k", WithBaseURL(srv.URL)), t)
	if fatal != nil {
		t.Fatalf("over-long delta produced a fatal stream error: %v", fatal)
	}
	if text.Len() != len(big) {
		t.Fatalf("over-long delta truncated: got %d bytes, want %d", text.Len(), len(big))
	}
	if !finished {
		t.Fatal("stream did not finish after an over-long delta")
	}
}

// TestStream_MalformedDeltaIsNonFatal: one unparseable delta in the middle of a
// stream must be skipped, not abort the whole turn — surrounding text and the
// finish event still arrive (#573).
func TestStream_MalformedDeltaIsNonFatal(t *testing.T) {
	events := []string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"m","model":"claude-opus-4-6","role":"assistant","content":[],"usage":{"input_tokens":5,"output_tokens":0}}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		// A truncated / malformed delta line (the RedHunt trigger).
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`,
		`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}
	srv := sseServer(t, events)
	defer srv.Close()
	text, finished, fatal := collect(New("k", WithBaseURL(srv.URL)), t)
	if fatal != nil {
		t.Fatalf("malformed delta aborted the turn: %v", fatal)
	}
	if got := text.String(); got != "Hello world" {
		t.Fatalf("surrounding text lost: got %q, want %q", got, "Hello world")
	}
	if !finished {
		t.Fatal("stream did not finish after a malformed delta")
	}
}

// TestStream_EmptyDataLineSkipped: an empty/keep-alive data line must be skipped,
// not dispatched to the delta handler as "unexpected end of JSON input" (#573).
func TestStream_EmptyDataLineSkipped(t *testing.T) {
	events := []string{
		`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"m","model":"claude-opus-4-6","role":"assistant","content":[],"usage":{"input_tokens":5,"output_tokens":0}}}`,
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`data: `,
		`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
	}
	srv := sseServer(t, events)
	defer srv.Close()
	text, finished, fatal := collect(New("k", WithBaseURL(srv.URL)), t)
	if fatal != nil {
		t.Fatalf("empty data line produced a fatal error: %v", fatal)
	}
	if text.String() != "ok" || !finished {
		t.Fatalf("stream did not complete cleanly past an empty data line: text=%q finished=%v", text.String(), finished)
	}
}

// errAfterReader yields r's bytes, then substitutes a transient network-style
// error in place of the clean io.EOF — simulating a connection cut mid-stream.
type errAfterReader struct {
	r   io.Reader
	err error
}

func (e *errAfterReader) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if err == io.EOF {
		return n, e.err
	}
	return n, err
}

// TestStream_TransientReadErrorIsRetryable: a mid-stream read failure (not EOF)
// must surface as a RETRYABLE llm.StreamError, so the retry middleware re-issues
// the completion with the accumulated context instead of the whole node being
// re-run and the episode discarded (#574).
func TestStream_TransientReadErrorIsRetryable(t *testing.T) {
	// A complete line, then a partial line, then a transient read error.
	body := &errAfterReader{
		r:   strings.NewReader("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\ndata: {\"partial"),
		err: io.ErrUnexpectedEOF,
	}
	ch := make(chan llm.StreamEvent, 32)
	a := New("k")
	a.parseSSE(body, ch, false)
	close(ch)

	var streamErr error
	for evt := range ch {
		if evt.Type == llm.EventError {
			streamErr = evt.Err
		}
	}
	if streamErr == nil {
		t.Fatal("expected a stream error for a transient mid-stream read cut")
	}
	var se *llm.StreamError
	if !errorsAs(streamErr, &se) {
		t.Fatalf("transient read error is not a retryable *llm.StreamError: %T (%v)", streamErr, streamErr)
	}
	if !se.Retryable() {
		t.Fatal("StreamError must be Retryable() so the completion (not the node) is retried")
	}
}

func errorsAs(err error, target any) bool { return stderrors.As(err, target) }
