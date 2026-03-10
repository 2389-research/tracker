// ABOUTME: Tests for the codergen handler that invokes Layer 2 agent sessions.
// ABOUTME: Uses a mock Completer to verify session creation, prompt passing, and result capture.
package handlers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/agent"
	agentexec "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

type fakeCompleter struct {
	responseText string
	err          error
}

func (f *fakeCompleter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &llm.Response{
		Message:      llm.AssistantMessage(f.responseText),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
	}, nil
}

type scriptedCompleter struct {
	responses []*llm.Response
	index     int
}

func (s *scriptedCompleter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if s.index >= len(s.responses) {
		return nil, context.DeadlineExceeded
	}
	resp := s.responses[s.index]
	s.index++
	return resp, nil
}

func TestCodergenHandlerName(t *testing.T) {
	h := NewCodergenHandler(nil, "")
	if h.Name() != "codergen" {
		t.Errorf("expected 'codergen', got %q", h.Name())
	}
}

func TestCodergenHandlerMissingPrompt(t *testing.T) {
	client := &fakeCompleter{responseText: "done"}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{}}
	pctx := pipeline.NewPipelineContext()
	_, err := h.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
}

func TestCodergenHandlerSuccess(t *testing.T) {
	client := &fakeCompleter{responseText: "Hello, World!"}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "Write hello world"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected 'success', got %q", outcome.Status)
	}
	// The handler returns last_response via ContextUpdates for the engine
	// to merge, rather than writing directly to pctx.
	lastResp := outcome.ContextUpdates[pipeline.ContextKeyLastResponse]
	if lastResp != "Hello, World!" {
		t.Errorf("expected 'Hello, World!', got %q", lastResp)
	}
}

func TestCodergenHandlerCapturesOutcomeInContext(t *testing.T) {
	client := &fakeCompleter{responseText: "completed task"}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "do something"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.ContextUpdates[pipeline.ContextKeyLastResponse] != "completed task" {
		t.Errorf("expected context update for last_response")
	}
}

func TestCodergenHandlerLLMError(t *testing.T) {
	client := &fakeCompleter{err: context.DeadlineExceeded}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "do something"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("handler should not return error on LLM failure, got: %v", err)
	}
	if outcome.Status != pipeline.OutcomeRetry {
		t.Errorf("expected 'retry', got %q", outcome.Status)
	}
}

func TestCodergenHandlerConfigurationErrorIsFatal(t *testing.T) {
	cfgErr := &llm.ConfigurationError{SDKError: llm.SDKError{Msg: `unknown provider: "gemini"`}}
	client := &fakeCompleter{err: cfgErr}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "do something"}}
	pctx := pipeline.NewPipelineContext()
	_, err := h.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected hard error for ConfigurationError, got nil")
	}
}

func TestCodergenHandlerAutoStatusSuccess(t *testing.T) {
	client := &fakeCompleter{responseText: "STATUS:success\nAll tests pass."}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "run tests", "auto_status": "true"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected 'success', got %q", outcome.Status)
	}
}

func TestCodergenHandlerAutoStatusFail(t *testing.T) {
	client := &fakeCompleter{responseText: "STATUS:fail\nTests failed."}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "run tests", "auto_status": "true"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected 'fail', got %q", outcome.Status)
	}
}

func TestCodergenHandlerAutoStatusRetry(t *testing.T) {
	client := &fakeCompleter{responseText: "STATUS:retry\nNeed more context."}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "analyze code", "auto_status": "true"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeRetry {
		t.Errorf("expected 'retry', got %q", outcome.Status)
	}
}

func TestCodergenHandlerSystemPrompt(t *testing.T) {
	client := &fakeCompleter{responseText: "done"}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "do work", "system_prompt": "You are helpful."}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected 'success', got %q", outcome.Status)
	}
}

func TestCodergenHandlerWritesArtifacts(t *testing.T) {
	workdir := t.TempDir()
	client := &fakeCompleter{responseText: "Hello, World!"}
	h := NewCodergenHandler(client, workdir)
	node := &pipeline.Node{
		ID:      "gen",
		Shape:   "box",
		Handler: "codergen",
		Attrs:   map[string]string{"prompt": "Write hello world"},
	}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Fatalf("expected success, got %q", outcome.Status)
	}

	promptBytes, err := os.ReadFile(filepath.Join(workdir, "gen", "prompt.md"))
	if err != nil {
		t.Fatalf("expected prompt artifact: %v", err)
	}
	if string(promptBytes) != "Write hello world" {
		t.Fatalf("prompt artifact = %q", string(promptBytes))
	}

	responseBytes, err := os.ReadFile(filepath.Join(workdir, "gen", "response.md"))
	if err != nil {
		t.Fatalf("expected response artifact: %v", err)
	}
	response := string(responseBytes)
	if !containsAll(response, "TURN 1", "TEXT:", "Hello, World!") {
		t.Fatalf("response artifact = %q", response)
	}

	statusBytes, err := os.ReadFile(filepath.Join(workdir, "gen", "status.json"))
	if err != nil {
		t.Fatalf("expected status artifact: %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal(statusBytes, &status); err != nil {
		t.Fatalf("status artifact should be valid json: %v", err)
	}
	if status["outcome"] != pipeline.OutcomeSuccess {
		t.Fatalf("status outcome = %v", status["outcome"])
	}
}

func TestCodergenHandlerForwardsAgentEvents(t *testing.T) {
	workdir := t.TempDir()
	client := &scriptedCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{
						{
							Kind: llm.KindToolCall,
							ToolCall: &llm.ToolCallData{
								ID:        "call_1",
								Name:      "read",
								Arguments: json.RawMessage(`{"path":"go.mod"}`),
							},
						},
					},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}
	h := NewCodergenHandler(client, workdir)

	var got []agent.EventType
	h.eventHandler = agent.EventHandlerFunc(func(evt agent.Event) {
		got = append(got, evt.Type)
	})

	node := &pipeline.Node{
		ID:      "gen",
		Shape:   "box",
		Handler: "codergen",
		Attrs:   map[string]string{"prompt": "inspect repo"},
	}
	pctx := pipeline.NewPipelineContext()

	if _, err := h.Execute(context.Background(), node, pctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !containsAgentEvent(got, agent.EventToolCallStart) {
		t.Fatalf("expected forwarded tool call start event, got %v", got)
	}
	if !containsAgentEvent(got, agent.EventToolCallEnd) {
		t.Fatalf("expected forwarded tool call end event, got %v", got)
	}
}

func containsAgentEvent(events []agent.EventType, want agent.EventType) bool {
	for _, evt := range events {
		if evt == want {
			return true
		}
	}
	return false
}

func TestCodergenHandlerExpandsGoalFromContext(t *testing.T) {
	workdir := t.TempDir()
	client := &fakeCompleter{responseText: "ok"}
	h := NewCodergenHandler(client, workdir)
	node := &pipeline.Node{
		ID:      "gen",
		Shape:   "box",
		Handler: "codergen",
		Attrs:   map[string]string{"prompt": "Plan for $goal"},
	}
	pctx := pipeline.NewPipelineContext()
	pctx.Set(pipeline.ContextKeyGoal, "ship a hello world script")

	_, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	promptBytes, err := os.ReadFile(filepath.Join(workdir, "gen", "prompt.md"))
	if err != nil {
		t.Fatalf("expected prompt artifact: %v", err)
	}
	if string(promptBytes) != "Plan for ship a hello world script" {
		t.Fatalf("expanded prompt = %q", string(promptBytes))
	}
}

func TestCodergenHandlerWritesTranscriptForToolOnlyRun(t *testing.T) {
	workdir := t.TempDir()
	client := &scriptedCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "write",
							Arguments: json.RawMessage(`{"path":"note.txt","content":"hello from tool"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.Message{Role: llm.RoleAssistant},
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}
	h := NewCodergenHandler(client, workdir)
	h.env = agentexec.NewLocalEnvironment(workdir)
	node := &pipeline.Node{
		ID:      "gen",
		Shape:   "box",
		Handler: "codergen",
		Attrs:   map[string]string{"prompt": "create a note"},
	}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Fatalf("expected success, got %q", outcome.Status)
	}

	written, err := os.ReadFile(filepath.Join(workdir, "note.txt"))
	if err != nil {
		t.Fatalf("expected tool-created file: %v", err)
	}
	if string(written) != "hello from tool" {
		t.Fatalf("tool-created file = %q", string(written))
	}

	responseBytes, err := os.ReadFile(filepath.Join(workdir, "gen", "response.md"))
	if err != nil {
		t.Fatalf("expected response artifact: %v", err)
	}
	response := string(responseBytes)
	if response == "" {
		t.Fatal("expected non-empty response transcript")
	}
	if !containsAll(response,
		"TURN 1",
		"TOOL CALL: write",
		`{"path":"note.txt","content":"hello from tool"}`,
		"TOOL RESULT: write",
		"wrote 15 bytes to note.txt",
	) {
		t.Fatalf("response artifact missing tool transcript:\n%s", response)
	}

	if outcome.ContextUpdates[pipeline.ContextKeyLastResponse] != "" {
		t.Fatalf("expected empty last_response for tool-only run, got %q", outcome.ContextUpdates[pipeline.ContextKeyLastResponse])
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
