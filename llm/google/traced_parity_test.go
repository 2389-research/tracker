// ABOUTME: #605 parity — a traced (streamed) Gemini completion carries the same
// ABOUTME: model as the untraced Complete path, and stream 429s keep Retry-After.
package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// geminiParityServer serves the non-stream generateContent body and the SSE
// streamGenerateContent stream for the SAME completion, branching on the URL
// path. Both report modelVersion "gemini-real-2026". Gemini has no response id,
// so ID stays empty in both paths.
func geminiParityServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, ":streamGenerateContent") {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, strings.Join([]string{
				`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1,"totalTokenCount":6},"modelVersion":"gemini-real-2026"}`,
				"",
			}, "\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],`+
			`"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1,"totalTokenCount":6},"modelVersion":"gemini-real-2026"}`)
	}))
}

func TestTracedUntracedParity_Gemini(t *testing.T) {
	server := geminiParityServer()
	defer server.Close()

	client, err := llm.NewClient(
		llm.WithProvider(New("test-key", WithBaseURL(server.URL))),
		llm.WithDefaultProvider("gemini"),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	untraced, err := client.Complete(context.Background(), &llm.Request{
		Model: "gemini-alias", Messages: []llm.Message{llm.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("untraced: %v", err)
	}
	traced, err := client.Complete(context.Background(), &llm.Request{
		Model: "gemini-alias", Messages: []llm.Message{llm.UserMessage("hi")},
		TraceObservers: []llm.TraceObserver{llm.TraceObserverFunc(func(llm.TraceEvent) {})},
	})
	if err != nil {
		t.Fatalf("traced: %v", err)
	}

	if untraced.Model != "gemini-real-2026" || traced.Model != untraced.Model {
		t.Errorf("Model parity: untraced %q traced %q", untraced.Model, traced.Model)
	}
	if traced.ID != untraced.ID {
		t.Errorf("ID parity: untraced %q traced %q", untraced.ID, traced.ID)
	}
}

func TestStreamStatusErrorCarriesRetryAfter_Gemini(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "31")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"slow down"}}`)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	var streamErr error
	for evt := range a.Stream(context.Background(), &llm.Request{
		Model: "gemini-alias", Messages: []llm.Message{llm.UserMessage("hi")},
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
	if rle.RetryAfter == nil || *rle.RetryAfter != 31 {
		t.Errorf("RetryAfter = %v, want 31", rle.RetryAfter)
	}
}
