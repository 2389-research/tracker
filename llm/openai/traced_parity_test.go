// ABOUTME: #605 parity — a traced (streamed) OpenAI completion carries the same
// ABOUTME: id/model as the untraced Complete path, and stream 429s keep Retry-After.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// openaiParityServer serves the non-stream Responses body and the SSE stream for
// the SAME logical completion (id "resp_parity", model "gpt-parity-2026"),
// branching on the request's stream flag.
func openaiParityServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		if parsed["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, strings.Join([]string{
				"event: response.created",
				`data: {"type":"response.created","response":{"id":"resp_parity","model":"gpt-parity-2026"}}`,
				"",
				"event: response.output_item.added",
				`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message"}}`,
				"",
				"event: response.output_text.delta",
				`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hi"}`,
				"",
				"event: response.output_item.done",
				`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message"}}`,
				"",
				"event: response.completed",
				`data: {"type":"response.completed","response":{"id":"resp_parity","status":"completed","usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11},"output":[]}}`,
				"",
			}, "\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"resp_parity","model":"gpt-parity-2026","status":"completed",`+
			`"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],`+
			`"usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11}}`)
	}))
}

func TestTracedUntracedParity_OpenAI(t *testing.T) {
	server := openaiParityServer(t)
	defer server.Close()

	client, err := llm.NewClient(
		llm.WithProvider(New("test-key", WithBaseURL(server.URL))),
		llm.WithDefaultProvider("openai"),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	untraced, err := client.Complete(context.Background(), &llm.Request{
		Model: "gpt-alias", Messages: []llm.Message{llm.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("untraced: %v", err)
	}
	traced, err := client.Complete(context.Background(), &llm.Request{
		Model: "gpt-alias", Messages: []llm.Message{llm.UserMessage("hi")},
		TraceObservers: []llm.TraceObserver{llm.TraceObserverFunc(func(llm.TraceEvent) {})},
	})
	if err != nil {
		t.Fatalf("traced: %v", err)
	}

	if untraced.ID != "resp_parity" || traced.ID != untraced.ID {
		t.Errorf("ID parity: untraced %q traced %q", untraced.ID, traced.ID)
	}
	if untraced.Model != "gpt-parity-2026" || traced.Model != untraced.Model {
		t.Errorf("Model parity: untraced %q traced %q", untraced.Model, traced.Model)
	}
}

func TestStreamStatusErrorCarriesRetryAfter_OpenAI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "23")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"slow down"}}`)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	var streamErr error
	for evt := range a.Stream(context.Background(), &llm.Request{
		Model: "gpt-alias", Messages: []llm.Message{llm.UserMessage("hi")},
	}) {
		if evt.Err != nil {
			streamErr = evt.Err
		}
	}
	if streamErr == nil {
		t.Fatal("expected a stream error, got nil")
	}
	var rle *llm.RateLimitError
	if !errors.As(streamErr, &rle) {
		t.Fatalf("expected *llm.RateLimitError, got %T: %v", streamErr, streamErr)
	}
	if rle.RetryAfter == nil || *rle.RetryAfter != 23 {
		t.Errorf("RetryAfter = %v, want 23", rle.RetryAfter)
	}
}
