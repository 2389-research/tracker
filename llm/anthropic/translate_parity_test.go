package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/mammoth-lite/llm"
)

func TestParityToolCallsFinishReasonsAndCacheUsage(t *testing.T) {
	body, err := translateRequest(&llm.Request{
		Model: "claude-sonnet-4-5",
		Messages: []llm.Message{
			llm.AssistantMessage("working"),
			llm.ToolResultMessage("call_1", "patched", false),
		},
	})
	if err != nil {
		t.Fatalf("translateRequest failed: %v", err)
	}

	var req anthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(req.Messages))
	}
	if req.Messages[1].Role != "user" || len(req.Messages[1].Content) != 1 || req.Messages[1].Content[0].Type != "tool_result" {
		t.Fatalf("tool result message = %+v", req.Messages[1])
	}

	raw := []byte(`{
		"id": "msg_1",
		"model": "claude-sonnet-4-5",
		"role": "assistant",
		"content": [
			{"type": "tool_use", "id": "call_1", "name": "edit", "input": {"path":"code.txt","old_string":"before","new_string":"after"}}
		],
		"stop_reason": "tool_use",
		"usage": {
			"input_tokens": 10,
			"output_tokens": 4,
			"cache_read_input_tokens": 80,
			"cache_creation_input_tokens": 12
		}
	}`)

	resp, err := translateResponse(raw)
	if err != nil {
		t.Fatalf("translateResponse failed: %v", err)
	}
	if resp.FinishReason.Reason != "tool_calls" {
		t.Fatalf("finish reason = %q, want tool_calls", resp.FinishReason.Reason)
	}
	if len(resp.ToolCalls()) != 1 || resp.ToolCalls()[0].Name != "edit" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls())
	}
	if resp.Usage.TotalTokens != 14 {
		t.Fatalf("total tokens = %d, want 14", resp.Usage.TotalTokens)
	}
	if resp.Usage.CacheReadTokens == nil || *resp.Usage.CacheReadTokens != 80 {
		t.Fatalf("cache read tokens = %v, want 80", resp.Usage.CacheReadTokens)
	}
	if resp.Usage.CacheWriteTokens == nil || *resp.Usage.CacheWriteTokens != 12 {
		t.Fatalf("cache write tokens = %v, want 12", resp.Usage.CacheWriteTokens)
	}
}
