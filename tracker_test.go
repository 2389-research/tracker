// ABOUTME: Tests for the top-level tracker convenience API.
// ABOUTME: Validates Config defaulting, auto-wiring, Run(), NewEngine(), and error paths.
package tracker

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// stubCompleter returns canned responses for testing.
type stubCompleter struct {
	response *llm.Response
}

func (s *stubCompleter) Complete(_ context.Context, _ *llm.Request) (*llm.Response, error) {
	return s.response, nil
}

const simpleDOT = `digraph test {
	start [shape=Mdiamond];
	finish [shape=Msquare];
	start -> finish;
}`

func TestNewEngine_InvalidDOT(t *testing.T) {
	_, err := NewEngine("not valid dot {{{", Config{})
	if err == nil {
		t.Fatal("expected error for invalid DOT source")
	}
}

func TestNewEngine_ValidDOT(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	engine, err := NewEngine(simpleDOT, Config{
		LLMClient: client,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer engine.Close()

	if engine.inner == nil {
		t.Fatal("expected inner engine to be set")
	}
}

func TestEngine_CloseIdempotent(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	engine, err := NewEngine(simpleDOT, Config{LLMClient: client})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := engine.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestRun_SimplePipeline(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	result, err := Run(context.Background(), simpleDOT, Config{
		LLMClient: client,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "success" {
		t.Errorf("expected status=success, got %q", result.Status)
	}
	if result.RunID == "" {
		t.Error("expected non-empty RunID")
	}
	if result.EngineResult == nil {
		t.Error("expected EngineResult to be set")
	}
}
