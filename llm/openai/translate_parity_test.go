package openai

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/llm"
)

func TestParityToolCallsFinishReasonsAndUsage(t *testing.T) {
	body, err := translateRequest(&llm.Request{
		Model: "gpt-5.2-codex",
		Messages: []llm.Message{
			// Assistant text is not echoed back in the Responses API input;
			// only function_call items from assistant messages are included.
			llm.AssistantMessage("prelude"),
			llm.ToolResultMessage("call_1", "patched", false),
		},
	})
	if err != nil {
		t.Fatalf("translateRequest failed: %v", err)
	}

	var req openaiRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	// Only the tool result survives; assistant text is dropped.
	if len(req.Input) != 1 {
		t.Fatalf("input item count = %d, want 1", len(req.Input))
	}
	if req.Input[0].Type != "function_call_output" || req.Input[0].CallID != "call_1" {
		t.Fatalf("tool result input = %+v", req.Input[0])
	}

	raw := []byte(`{
		"id": "resp_1",
		"model": "gpt-5.2-codex",
		"status": "completed",
		"output": [
			{"type": "function_call", "id": "call_1", "name": "apply_patch", "arguments": "{\"patch\":\"*** Begin Patch\\n*** End Patch\\n\"}"}
		],
		"usage": {"input_tokens": 11, "output_tokens": 7, "total_tokens": 18}
	}`)

	resp, err := translateResponse(raw)
	if err != nil {
		t.Fatalf("translateResponse failed: %v", err)
	}
	if resp.FinishReason.Reason != "tool_calls" {
		t.Fatalf("finish reason = %q, want tool_calls", resp.FinishReason.Reason)
	}
	if len(resp.ToolCalls()) != 1 || resp.ToolCalls()[0].Name != "apply_patch" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls())
	}
	if resp.Usage.TotalTokens != 18 {
		t.Fatalf("total tokens = %d, want 18", resp.Usage.TotalTokens)
	}
}
