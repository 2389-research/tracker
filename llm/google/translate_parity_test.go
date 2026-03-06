package google

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/mammoth-lite/llm"
)

func TestParityToolCallsFinishReasonsAndUsage(t *testing.T) {
	body, err := translateRequest(&llm.Request{
		Model: "gemini-3-pro-preview",
		Messages: []llm.Message{
			llm.AssistantMessage("working"),
			llm.ToolResultMessage("edit", "patched", false),
		},
	})
	if err != nil {
		t.Fatalf("translateRequest failed: %v", err)
	}

	var req geminiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if len(req.Contents) != 2 {
		t.Fatalf("content count = %d, want 2", len(req.Contents))
	}
	if len(req.Contents[1].Parts) != 1 || req.Contents[1].Parts[0].FunctionResponse == nil {
		t.Fatalf("tool result content = %+v", req.Contents[1])
	}

	raw := []byte(`{
		"candidates": [
			{
				"content": {
					"role": "model",
					"parts": [
						{"functionCall": {"name": "edit", "args": {"path":"code.txt","old_string":"before","new_string":"after"}}}
					]
				},
				"finishReason": "STOP"
			}
		],
		"usageMetadata": {"promptTokenCount": 9, "candidatesTokenCount": 6, "totalTokenCount": 15},
		"modelVersion": "gemini-3-pro-preview"
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
	if resp.Usage.TotalTokens != 15 {
		t.Fatalf("total tokens = %d, want 15", resp.Usage.TotalTokens)
	}
}
