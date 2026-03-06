package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/2389-research/mammoth-lite/agent/tools"
	"github.com/2389-research/mammoth-lite/llm"
)

type inspectingCompleter struct {
	requests  []*llm.Request
	responses []*llm.Response
}

func (c *inspectingCompleter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	c.requests = append(c.requests, req)
	if len(c.responses) == 0 {
		return &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		}, nil
	}
	resp := c.responses[0]
	c.responses = c.responses[1:]
	return resp, nil
}

func TestParityUnknownToolReturnsErrorResultNotSessionFailure(t *testing.T) {
	client := &inspectingCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "missing_tool",
							Arguments: json.RawMessage(`{}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("I handled the missing tool error."),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	sess := mustNewSession(t, client, DefaultConfig())
	result, err := sess.Run(context.Background(), "Call a missing tool and recover")
	if err != nil {
		t.Fatalf("session should not fail on unknown tool: %v", err)
	}
	if result.Turns != 2 {
		t.Fatalf("turns = %d, want 2", result.Turns)
	}
	if len(client.requests) < 2 {
		t.Fatalf("expected 2 requests, got %d", len(client.requests))
	}

	var sawErrorResult bool
	for _, part := range client.requests[1].Messages[len(client.requests[1].Messages)-1].Content {
		if part.Kind == llm.KindToolResult && part.ToolResult != nil && part.ToolResult.IsError {
			sawErrorResult = strings.Contains(part.ToolResult.Content, "unknown tool")
		}
	}
	if !sawErrorResult {
		t.Fatal("expected unknown tool to be returned as an error tool result")
	}
}

func TestParitySessionRunsToolLoopUntilNaturalCompletion(t *testing.T) {
	client := &inspectingCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "echo_tool",
							Arguments: json.RawMessage(`{}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("All done."),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	tool := &stubTool{name: "echo_tool", output: "ok"}
	sess := mustNewSession(t, client, DefaultConfig(), WithTools(tool))
	result, err := sess.Run(context.Background(), "Use a tool and finish")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Turns != 2 {
		t.Fatalf("turns = %d, want 2", result.Turns)
	}
	if result.MaxTurnsUsed {
		t.Fatal("expected natural completion, not turn exhaustion")
	}
}

var _ tools.Tool = (*stubTool)(nil)
