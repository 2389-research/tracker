package handlers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	agentexec "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

type codergenInspectingCompleter struct {
	requests  []*llm.Request
	responses []*llm.Response
}

func (c *codergenInspectingCompleter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	c.requests = append(c.requests, req)
	resp := c.responses[0]
	c.responses = c.responses[1:]
	return resp, nil
}

func TestParityCodergenUsesExecutionEnvironmentAndTools(t *testing.T) {
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "input.txt"), []byte("hello from file"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	client := &codergenInspectingCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"input.txt"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("Used the tool result."),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	h := NewCodergenHandler(client, workdir)
	h.env = agentexec.NewLocalEnvironment(workdir)
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "Read the file"}}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Fatalf("status = %q, want success", outcome.Status)
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(client.requests))
	}

	var sawFileContent bool
	lastMsg := client.requests[1].Messages[len(client.requests[1].Messages)-1]
	for _, part := range lastMsg.Content {
		if part.Kind == llm.KindToolResult && part.ToolResult != nil && part.ToolResult.Content == "hello from file" {
			sawFileContent = true
		}
	}
	if !sawFileContent {
		t.Fatal("expected second request to contain successful read tool result")
	}
}

func TestParityCodergenDoesNotForceSingleTurn(t *testing.T) {
	workdir := t.TempDir()
	client := &codergenInspectingCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "bash",
							Arguments: json.RawMessage(`{"command":"echo hi"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("Finished after the tool call."),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	h := NewCodergenHandler(client, workdir)
	h.env = agentexec.NewLocalEnvironment(workdir)
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "Run a command"}}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.ContextUpdates[pipeline.ContextKeyLastResponse] != "Finished after the tool call." {
		t.Fatalf("last_response = %q", outcome.ContextUpdates[pipeline.ContextKeyLastResponse])
	}
	if len(client.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(client.requests))
	}
}
