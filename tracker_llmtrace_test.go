// ABOUTME: Tests Config.LLMTrace — raw LLM trace events wired to the run's client (#478 prereq).
package tracker

import (
	"context"
	"sync"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// traceMockAdapter is a minimal ProviderAdapter whose stream produces the trace
// events (start/text/finish) that a client-level observer should see.
type traceMockAdapter struct{ name string }

func (m *traceMockAdapter) Name() string { return m.name }
func (m *traceMockAdapter) Close() error { return nil }
func (m *traceMockAdapter) Complete(context.Context, *llm.Request) (*llm.Response, error) {
	return &llm.Response{Message: llm.AssistantMessage("hello"), FinishReason: llm.FinishReason{Reason: "stop"}}, nil
}

func (m *traceMockAdapter) Stream(context.Context, *llm.Request) <-chan llm.StreamEvent {
	ch := make(chan llm.StreamEvent, 3)
	ch <- llm.StreamEvent{Type: llm.EventStreamStart}
	ch <- llm.StreamEvent{Type: llm.EventTextDelta, Delta: "hello"}
	ch <- llm.StreamEvent{Type: llm.EventFinish, FinishReason: &llm.FinishReason{Reason: "stop"}}
	close(ch)
	return ch
}

// TestConfig_LLMTraceReceivesEvents proves a Config.LLMTrace observer is attached
// to the run's client and receives raw LLM trace events during execution.
func TestConfig_LLMTraceReceivesEvents(t *testing.T) {
	client, err := llm.NewClient(
		llm.WithProvider(&traceMockAdapter{name: "anthropic"}),
		llm.WithDefaultProvider("anthropic"),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	var mu sync.Mutex
	var events int
	rec := llm.TraceObserverFunc(func(llm.TraceEvent) {
		mu.Lock()
		events++
		mu.Unlock()
	})

	if _, err := Run(context.Background(), costDip, Config{
		Format:     "dip",
		WorkingDir: t.TempDir(),
		LLMClient:  client,
		LLMTrace:   rec,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	n := events
	mu.Unlock()
	if n == 0 {
		t.Fatal("expected the LLMTrace observer to receive trace events")
	}
}
