// ABOUTME: Tests for OpenAI Chat Completions API request/response translation.
// ABOUTME: Validates message format, tool wrapping, response format, and finish reason mapping.
package openaicompat

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/llm"
)

func TestTranslateRequest_BasicUserMessage(t *testing.T) {
	req := &llm.Request{
		Model: "gpt-4.1",
		Messages: []llm.Message{
			llm.SystemMessage("You are helpful."),
			llm.UserMessage("Hello"),
		},
	}

	body, err := translateRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}

	// Chat Completions uses "messages" array, NOT "input"
	if _, ok := raw["input"]; ok {
		t.Error("Chat Completions must not have 'input' field (that's Responses API)")
	}

	msgs, ok := raw["messages"].([]any)
	if !ok {
		t.Fatal("expected 'messages' array")
	}

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(msgs))
	}

	// System message stays in messages array (not extracted to instructions)
	msg0 := msgs[0].(map[string]any)
	if msg0["role"] != "system" {
		t.Errorf("messages[0] role = %v, want 'system'", msg0["role"])
	}
	if msg0["content"] != "You are helpful." {
		t.Errorf("messages[0] content = %v, want 'You are helpful.'", msg0["content"])
	}

	msg1 := msgs[1].(map[string]any)
	if msg1["role"] != "user" {
		t.Errorf("messages[1] role = %v, want 'user'", msg1["role"])
	}
	if msg1["content"] != "Hello" {
		t.Errorf("messages[1] content = %v, want 'Hello'", msg1["content"])
	}

	// Model
	if raw["model"] != "gpt-4.1" {
		t.Errorf("model = %v, want 'gpt-4.1'", raw["model"])
	}

	// Default max_tokens
	maxTokens, ok := raw["max_tokens"].(float64)
	if !ok {
		t.Fatal("expected 'max_tokens' to be present")
	}
	if int(maxTokens) != 16384 {
		t.Errorf("max_tokens = %v, want 16384", maxTokens)
	}

	// No instructions field (that's Responses API)
	if _, ok := raw["instructions"]; ok {
		t.Error("Chat Completions must not have 'instructions' field")
	}
}

func TestTranslateRequest_AssistantWithToolCalls(t *testing.T) {
	req := &llm.Request{
		Model: "gpt-4.1",
		Messages: []llm.Message{
			llm.UserMessage("List files"),
			{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{
					{Kind: llm.KindText, Text: "I'll list files."},
					{Kind: llm.KindToolCall, ToolCall: &llm.ToolCallData{
						ID:        "call_abc123",
						Name:      "bash",
						Arguments: json.RawMessage(`{"command":"ls"}`),
					}},
				},
			},
			llm.ToolResultMessage("call_abc123", "file1.txt\nfile2.txt", false),
		},
	}

	body, err := translateRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}

	msgs := raw["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	// messages[1]: assistant message with nested tool_calls
	asstMsg := msgs[1].(map[string]any)
	if asstMsg["role"] != "assistant" {
		t.Errorf("messages[1] role = %v, want 'assistant'", asstMsg["role"])
	}
	if asstMsg["content"] != "I'll list files." {
		t.Errorf("messages[1] content = %v, want assistant text", asstMsg["content"])
	}

	// tool_calls nested in the assistant message
	toolCalls, ok := asstMsg["tool_calls"].([]any)
	if !ok {
		t.Fatal("expected 'tool_calls' array in assistant message")
	}
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}

	tc := toolCalls[0].(map[string]any)
	if tc["id"] != "call_abc123" {
		t.Errorf("tool_call id = %v, want 'call_abc123'", tc["id"])
	}
	if tc["type"] != "function" {
		t.Errorf("tool_call type = %v, want 'function'", tc["type"])
	}

	fn := tc["function"].(map[string]any)
	if fn["name"] != "bash" {
		t.Errorf("function name = %v, want 'bash'", fn["name"])
	}
	if fn["arguments"] != `{"command":"ls"}` {
		t.Errorf("function arguments = %v, want json", fn["arguments"])
	}

	// messages[2]: tool result with tool_call_id
	toolMsg := msgs[2].(map[string]any)
	if toolMsg["role"] != "tool" {
		t.Errorf("messages[2] role = %v, want 'tool'", toolMsg["role"])
	}
	if toolMsg["tool_call_id"] != "call_abc123" {
		t.Errorf("messages[2] tool_call_id = %v, want 'call_abc123'", toolMsg["tool_call_id"])
	}
	if toolMsg["content"] != "file1.txt\nfile2.txt" {
		t.Errorf("messages[2] content = %v, want tool output", toolMsg["content"])
	}
}

func TestTranslateRequest_ToolDefsWrapping(t *testing.T) {
	params := json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"}}}`)
	req := &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("hi")},
		Tools: []llm.ToolDefinition{
			{Name: "bash", Description: "Run a shell command", Parameters: params},
		},
	}

	body, err := translateRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}

	tools, ok := raw["tools"].([]any)
	if !ok {
		t.Fatal("expected 'tools' array")
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	tool := tools[0].(map[string]any)
	// Chat Completions wraps in {type:"function", function:{...}}
	if tool["type"] != "function" {
		t.Errorf("tool type = %v, want 'function'", tool["type"])
	}

	fn, ok := tool["function"].(map[string]any)
	if !ok {
		t.Fatal("expected 'function' object nested in tool")
	}
	if fn["name"] != "bash" {
		t.Errorf("function name = %v, want 'bash'", fn["name"])
	}
	if fn["description"] != "Run a shell command" {
		t.Errorf("function description = %v, want 'Run a shell command'", fn["description"])
	}
	if fn["parameters"] == nil {
		t.Error("expected function parameters to be present")
	}
}

func TestTranslateRequest_ResponseFormatJSON(t *testing.T) {
	req := &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("hi")},
		ResponseFormat: &llm.ResponseFormat{
			Type: "json_object",
		},
	}

	body, err := translateRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}

	rf, ok := raw["response_format"].(map[string]any)
	if !ok {
		t.Fatal("expected 'response_format' object")
	}
	if rf["type"] != "json_object" {
		t.Errorf("response_format type = %v, want 'json_object'", rf["type"])
	}
}

func TestTranslateRequest_JSONSchemaDropped(t *testing.T) {
	schema := json.RawMessage(`{"type":"object"}`)
	req := &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("hi")},
		ResponseFormat: &llm.ResponseFormat{
			Type:       "json_schema",
			JSONSchema: schema,
			Strict:     true,
		},
	}

	body, err := translateRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}

	// json_schema is silently dropped for Chat Completions compat endpoints
	if _, ok := raw["response_format"]; ok {
		t.Error("expected response_format to be omitted for json_schema type")
	}
}

func TestTranslateResponse_TextOnly(t *testing.T) {
	respJSON := `{
		"id": "chatcmpl-123",
		"model": "gpt-4.1",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": "Hello there!"
			},
			"finish_reason": "stop"
		}],
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 5,
			"total_tokens": 15
		}
	}`

	resp, err := translateResponse([]byte(respJSON))
	if err != nil {
		t.Fatal(err)
	}

	if resp.ID != "chatcmpl-123" {
		t.Errorf("id = %v, want 'chatcmpl-123'", resp.ID)
	}
	if resp.Model != "gpt-4.1" {
		t.Errorf("model = %v, want 'gpt-4.1'", resp.Model)
	}
	if resp.Message.Role != llm.RoleAssistant {
		t.Errorf("role = %v, want assistant", resp.Message.Role)
	}
	if resp.Message.Text() != "Hello there!" {
		t.Errorf("text = %v, want 'Hello there!'", resp.Message.Text())
	}
	if resp.FinishReason.Reason != "stop" {
		t.Errorf("finish_reason = %v, want 'stop'", resp.FinishReason.Reason)
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("input_tokens = %d, want 10", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 5 {
		t.Errorf("output_tokens = %d, want 5", resp.Usage.OutputTokens)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("total_tokens = %d, want 15", resp.Usage.TotalTokens)
	}
}

func TestTranslateResponse_WithToolCalls(t *testing.T) {
	respJSON := `{
		"id": "chatcmpl-456",
		"model": "gpt-4.1",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": null,
				"tool_calls": [{
					"id": "call_xyz",
					"type": "function",
					"function": {
						"name": "bash",
						"arguments": "{\"command\":\"ls\"}"
					}
				}]
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {
			"prompt_tokens": 20,
			"completion_tokens": 10,
			"total_tokens": 30
		}
	}`

	resp, err := translateResponse([]byte(respJSON))
	if err != nil {
		t.Fatal(err)
	}

	if resp.FinishReason.Reason != "tool_calls" {
		t.Errorf("finish_reason = %v, want 'tool_calls'", resp.FinishReason.Reason)
	}

	calls := resp.Message.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}

	if calls[0].ID != "call_xyz" {
		t.Errorf("tool call id = %v, want 'call_xyz'", calls[0].ID)
	}
	if calls[0].Name != "bash" {
		t.Errorf("tool call name = %v, want 'bash'", calls[0].Name)
	}
	if string(calls[0].Arguments) != `{"command":"ls"}` {
		t.Errorf("tool call arguments = %v, want json", string(calls[0].Arguments))
	}
}

func TestTranslateRequest_StreamFlagAndOptions(t *testing.T) {
	req := &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("hi")},
	}

	// Non-streaming: no stream field or stream_options.
	body, err := translateRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["stream"]; ok {
		t.Error("non-streaming request must not have 'stream' field")
	}
	if _, ok := raw["stream_options"]; ok {
		t.Error("non-streaming request must not have 'stream_options' field")
	}

	// Streaming: stream=true and stream_options.include_usage=true.
	body, err = translateRequest(req, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["stream"] != true {
		t.Errorf("stream = %v, want true", raw["stream"])
	}
	so, ok := raw["stream_options"].(map[string]any)
	if !ok {
		t.Fatal("expected 'stream_options' object in streaming request")
	}
	if so["include_usage"] != true {
		t.Errorf("stream_options.include_usage = %v, want true", so["include_usage"])
	}
}

func TestTranslateResponse_EmptyChoices(t *testing.T) {
	respJSON := `{
		"id": "chatcmpl-789",
		"model": "gpt-4.1",
		"choices": [],
		"usage": {
			"prompt_tokens": 5,
			"completion_tokens": 0,
			"total_tokens": 5
		}
	}`

	resp, err := translateResponse([]byte(respJSON))
	if err != nil {
		t.Fatal(err)
	}

	// Empty choices should give stop finish reason with empty message
	if resp.FinishReason.Reason != "stop" {
		t.Errorf("finish_reason = %v, want 'stop'", resp.FinishReason.Reason)
	}
	if resp.Message.Text() != "" {
		t.Errorf("text = %v, want empty", resp.Message.Text())
	}
}
