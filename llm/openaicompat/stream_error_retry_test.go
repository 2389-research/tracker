// ABOUTME: #684 — a traced completion retries a gateway's in-stream error whose code
// ABOUTME: Tracker doesn't know, through the real client and retry middleware.
package openaicompat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2389-research/tracker/llm"
)

// TestTracedCompleteRetriesUnknownStreamError: the first request ends in an
// in-stream error with no code, the way a gateway under load fails a request it
// already accepted. The retry middleware sends a second request, which succeeds.
// Agent sessions attach trace observers, so their turns take this streamed path.
func TestTracedCompleteRetriesUnknownStreamError(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		lines := []string{
			`data: {"id":"chatcmpl-retry","model":"compat-real","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`,
			"",
			`data: {"id":"chatcmpl-retry","model":"compat-real","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":1,"total_tokens":9}}`,
			"",
			"data: [DONE]",
			"",
		}
		if requests.Add(1) == 1 {
			lines = []string{`data: {"error":{"message":"scheduler admission timeout"}}`, ""}
		}
		for _, line := range lines {
			fmt.Fprintln(w, line)
		}
	}))
	defer server.Close()

	client, err := llm.NewClient(
		llm.WithProvider(New("test-key", WithBaseURL(server.URL))),
		llm.WithDefaultProvider("openai-compat"),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()
	client.AddMiddleware(llm.NewRetryMiddleware(llm.WithMaxRetries(3), llm.WithBaseDelay(time.Millisecond)))

	resp, err := client.Complete(context.Background(), &llm.Request{
		Model:          "compat-alias",
		Messages:       []llm.Message{llm.UserMessage("hi")},
		TraceObservers: []llm.TraceObserver{llm.TraceObserverFunc(func(llm.TraceEvent) {})},
	})
	if err != nil {
		t.Fatalf("Complete failed after %d request(s): %v", requests.Load(), err)
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("requests = %d, want 2 (the failed one and its retry)", got)
	}
	if got := resp.Text(); got != "hi" {
		t.Errorf("response text = %q, want %q", got, "hi")
	}
}
