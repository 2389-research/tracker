// ABOUTME: #605 parity — a traced (streamed) Anthropic completion carries the
// ABOUTME: same id/model as the untraced Complete path, and stream 429s keep Retry-After.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// anthropicParityServer serves the non-stream Messages response and the SSE
// stream for the SAME logical completion, branching on the request's stream
// flag. Both carry id "msg_parity" and model "claude-real-2026".
func anthropicParityServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		if parsed["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			for _, evt := range []string{
				`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg_parity","model":"claude-real-2026","role":"assistant","content":[],"usage":{"input_tokens":5,"output_tokens":0}}}`,
				`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
				`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
				`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
				`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
			} {
				fmt.Fprintf(w, "%s\n\n", evt)
				flusher.Flush()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg_parity","model":"claude-real-2026","type":"message","role":"assistant",`+
			`"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":1}}`)
	}))
}

func TestTracedUntracedParity_Anthropic(t *testing.T) {
	server := anthropicParityServer(t)
	defer server.Close()

	client, err := llm.NewClient(
		llm.WithProvider(New("test-key", WithBaseURL(server.URL))),
		llm.WithDefaultProvider("anthropic"),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	untraced, err := client.Complete(context.Background(), &llm.Request{
		Model: "claude-alias", Messages: []llm.Message{llm.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("untraced: %v", err)
	}
	traced, err := client.Complete(context.Background(), &llm.Request{
		Model: "claude-alias", Messages: []llm.Message{llm.UserMessage("hi")},
		TraceObservers: []llm.TraceObserver{llm.TraceObserverFunc(func(llm.TraceEvent) {})},
	})
	if err != nil {
		t.Fatalf("traced: %v", err)
	}

	if untraced.ID != "msg_parity" || traced.ID != untraced.ID {
		t.Errorf("ID parity: untraced %q traced %q", untraced.ID, traced.ID)
	}
	if untraced.Model != "claude-real-2026" || traced.Model != untraced.Model {
		t.Errorf("Model parity: untraced %q traced %q", untraced.Model, traced.Model)
	}
}

func TestStreamStatusErrorCarriesRetryAfter_Anthropic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "17")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"slow down"}}`)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	var streamErr error
	for evt := range a.Stream(context.Background(), &llm.Request{
		Model: "claude-alias", Messages: []llm.Message{llm.UserMessage("hi")},
	}) {
		if evt.Err != nil {
			streamErr = evt.Err
		}
	}
	assertRetryAfter(t, streamErr, 17)
}

// assertRetryAfter fails unless err is (or wraps) a RateLimitError whose
// RetryAfter equals want seconds.
func assertRetryAfter(t *testing.T, err error, want float64) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a stream error, got nil")
	}
	var rle *llm.RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("expected *llm.RateLimitError, got %T: %v", err, err)
	}
	if rle.RetryAfter == nil {
		t.Fatalf("RetryAfter is nil, want %v", want)
	}
	if *rle.RetryAfter != want {
		t.Errorf("RetryAfter = %v, want %v", *rle.RetryAfter, want)
	}
}
