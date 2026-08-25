// ABOUTME: #605 parity — a traced (streamed) openai-compat completion carries the
// ABOUTME: same id/model as untraced Complete, and stream 429s keep Retry-After.
package openaicompat

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

// compatParityServer serves the non-stream chat/completions body and the SSE
// stream for the SAME completion (id "chatcmpl-parity", model
// "compat-real-2026"), branching on the request's stream flag.
func compatParityServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		if parsed["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			for _, line := range []string{
				`data: {"id":"chatcmpl-parity","model":"compat-real-2026","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
				"",
				`data: {"id":"chatcmpl-parity","model":"compat-real-2026","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
				"",
				`data: {"id":"chatcmpl-parity","model":"compat-real-2026","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":1,"total_tokens":9}}`,
				"",
				"data: [DONE]",
				"",
			} {
				fmt.Fprintln(w, line)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chatcmpl-parity","model":"compat-real-2026",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":8,"completion_tokens":1,"total_tokens":9}}`)
	}))
}

func TestTracedUntracedParity_Compat(t *testing.T) {
	server := compatParityServer(t)
	defer server.Close()

	client, err := llm.NewClient(
		llm.WithProvider(New("test-key", WithBaseURL(server.URL))),
		llm.WithDefaultProvider("openai-compat"),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	untraced, err := client.Complete(context.Background(), &llm.Request{
		Model: "compat-alias", Messages: []llm.Message{llm.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("untraced: %v", err)
	}
	traced, err := client.Complete(context.Background(), &llm.Request{
		Model: "compat-alias", Messages: []llm.Message{llm.UserMessage("hi")},
		TraceObservers: []llm.TraceObserver{llm.TraceObserverFunc(func(llm.TraceEvent) {})},
	})
	if err != nil {
		t.Fatalf("traced: %v", err)
	}

	if untraced.ID != "chatcmpl-parity" || traced.ID != untraced.ID {
		t.Errorf("ID parity: untraced %q traced %q", untraced.ID, traced.ID)
	}
	if untraced.Model != "compat-real-2026" || traced.Model != untraced.Model {
		t.Errorf("Model parity: untraced %q traced %q", untraced.Model, traced.Model)
	}
}

func TestStreamStatusErrorCarriesRetryAfter_Compat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "13")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"slow down"}}`)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	var streamErr error
	for evt := range a.Stream(context.Background(), &llm.Request{
		Model: "compat-alias", Messages: []llm.Message{llm.UserMessage("hi")},
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
	if rle.RetryAfter == nil || *rle.RetryAfter != 13 {
		t.Errorf("RetryAfter = %v, want 13", rle.RetryAfter)
	}
}
