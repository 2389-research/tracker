// ABOUTME: Tests for the top-level tracker convenience API.
// ABOUTME: Validates Config defaulting, auto-wiring, Run(), NewEngine(), and error paths.
package tracker

import (
	"context"
	"os"
	"testing"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
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

const simpleDip = `workflow test
  start: s
  exit: e

  agent s
    label: Start

  agent e
    label: Exit

  edges
    s -> e
`

func TestNewEngine_InvalidDOT(t *testing.T) {
	_, err := NewEngine("not valid dot {{{", Config{Format: "dot"})
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
		Format:    "dot",
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

func TestNewEngine_DipFormat(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	engine, err := NewEngine(simpleDip, Config{
		Format:    "dip",
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

func TestNewEngine_AutoDetectDOT(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	// Empty format with "digraph" prefix should auto-detect as DOT.
	engine, err := NewEngine(simpleDOT, Config{
		LLMClient: client,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer engine.Close()
}

func TestNewEngine_UnknownFormat(t *testing.T) {
	_, err := NewEngine("anything", Config{Format: "yaml"})
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
}

func TestEngine_CloseIdempotent(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	engine, err := NewEngine(simpleDOT, Config{Format: "dot", LLMClient: client})
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
		Format:    "dot",
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

func TestRun_DipPipeline(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	result, err := Run(context.Background(), simpleDip, Config{
		Format:    "dip",
		LLMClient: client,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status=success, got %q", result.Status)
	}
}

func TestNewEngine_DefaultsWorkingDir(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	// Zero-value WorkingDir should succeed (defaults to cwd).
	// Verify by also constructing with an explicit WorkingDir and
	// confirming both succeed without error.
	engine1, err := NewEngine(simpleDOT, Config{Format: "dot", LLMClient: client})
	if err != nil {
		t.Fatalf("default WorkingDir: unexpected error: %v", err)
	}
	defer engine1.Close()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}

	engine2, err := NewEngine(simpleDOT, Config{
		Format:     "dot",
		LLMClient:  client,
		WorkingDir: cwd,
	})
	if err != nil {
		t.Fatalf("explicit WorkingDir: unexpected error: %v", err)
	}
	defer engine2.Close()
}

func TestRun_WithInitialContext(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	result, err := Run(context.Background(), simpleDOT, Config{
		Format:    "dot",
		LLMClient: client,
		Context:   map[string]string{"goal": "test the library"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %q", result.Status)
	}
}

func TestRun_WithEventHandler(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	var events []pipeline.PipelineEvent
	handler := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		events = append(events, evt)
	})

	result, err := Run(context.Background(), simpleDOT, Config{
		Format:       "dot",
		LLMClient:    client,
		EventHandler: handler,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %q", result.Status)
	}
	if len(events) == 0 {
		t.Error("expected at least one pipeline event")
	}
}

func TestNewEngine_ValidationError(t *testing.T) {
	badGraph := `digraph test {
		start [shape=Mdiamond];
		orphan [shape=box];
		start -> orphan;
	}`

	_, err := NewEngine(badGraph, Config{
		Format: "dot",
		LLMClient: &stubCompleter{
			response: &llm.Response{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	})
	if err == nil {
		t.Fatal("expected validation error for graph without exit node")
	}
}

func TestRun_InvalidDOT(t *testing.T) {
	_, err := Run(context.Background(), "not dot at all!!!", Config{Format: "dot"})
	if err == nil {
		t.Fatal("expected error for invalid DOT")
	}
}

func TestNewEngine_InvalidProvider(t *testing.T) {
	_, err := NewEngine(simpleDOT, Config{
		Format:   "dot",
		Provider: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestRun_WithRetryPolicy(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	result, err := Run(context.Background(), simpleDOT, Config{
		Format:      "dot",
		LLMClient:   client,
		RetryPolicy: "aggressive",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %q", result.Status)
	}
}
