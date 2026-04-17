// ABOUTME: Tests for OpenAI Responses API request/response format translation.
// ABOUTME: Validates response format (json_object, json_schema) mapping to OpenAI's text.format structure.
package openai

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/llm"
)

func TestTranslateRequestResponseFormatNil(t *testing.T) {
	req := &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Hello")},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}

	if _, ok := m["text"]; ok {
		t.Error("expected no 'text' field when ResponseFormat is nil")
	}
}

func TestTranslateRequestResponseFormatJSONObject(t *testing.T) {
	req := &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Hello")},
		ResponseFormat: &llm.ResponseFormat{
			Type: "json_object",
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}

	text, ok := m["text"].(map[string]any)
	if !ok {
		t.Fatal("expected 'text' field to be present as object")
	}

	format, ok := text["format"].(map[string]any)
	if !ok {
		t.Fatal("expected 'text.format' to be present as object")
	}

	if format["type"] != "json_object" {
		t.Errorf("expected format type 'json_object', got %v", format["type"])
	}

	// json_object mode should not have schema or name fields
	if _, ok := format["schema"]; ok {
		t.Error("expected no 'schema' field for json_object mode")
	}
}

func TestTranslateRequestResponseFormatJSONSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)
	req := &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Hello")},
		ResponseFormat: &llm.ResponseFormat{
			Type:       "json_schema",
			JSONSchema: schema,
			Strict:     true,
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}

	text, ok := m["text"].(map[string]any)
	if !ok {
		t.Fatal("expected 'text' field to be present as object")
	}

	format, ok := text["format"].(map[string]any)
	if !ok {
		t.Fatal("expected 'text.format' to be present as object")
	}

	if format["type"] != "json_schema" {
		t.Errorf("expected format type 'json_schema', got %v", format["type"])
	}

	if format["name"] != "response" {
		t.Errorf("expected format name 'response', got %v", format["name"])
	}

	if format["strict"] != true {
		t.Errorf("expected format strict true, got %v", format["strict"])
	}

	schemaField, ok := format["schema"].(map[string]any)
	if !ok {
		t.Fatal("expected 'schema' field to be present as object")
	}

	if schemaField["type"] != "object" {
		t.Errorf("expected schema type 'object', got %v", schemaField["type"])
	}
}

func TestTranslateRequestResponseFormatText(t *testing.T) {
	req := &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Hello")},
		ResponseFormat: &llm.ResponseFormat{
			Type: "text",
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}

	// "text" type should not add a text.format field since it's the default
	if _, ok := m["text"]; ok {
		t.Error("expected no 'text' field for 'text' response format type")
	}
}

// TestTranslateRequestMultiTurnToolCall verifies the second-turn request format
// when the assistant made a tool call on turn 1 and we're sending results back.
func TestTranslateRequestMultiTurnToolCall(t *testing.T) {
	// Simulate: system, user prompt, assistant (text + tool call), tool result
	req := &llm.Request{
		Model: "gpt-5.4",
		Messages: []llm.Message{
			llm.SystemMessage("You are helpful."),
			llm.UserMessage("List files"),
			{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{
					{Kind: llm.KindText, Text: "I'll list the files for you."},
					{Kind: llm.KindToolCall, ToolCall: &llm.ToolCallData{
						ID:        "call_abc123",
						Name:      "bash",
						Arguments: json.RawMessage(`{"command":"ls -la"}`),
					}},
				},
			},
			llm.ToolResultMessage("call_abc123", "file1.txt\nfile2.txt", false),
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}

	// System message should be extracted to instructions
	instructions, _ := raw["instructions"].(string)
	if instructions != "You are helpful." {
		t.Errorf("instructions = %q, want 'You are helpful.'", instructions)
	}

	input, ok := raw["input"].([]any)
	if !ok {
		t.Fatal("expected input array")
	}

	// Dump for debugging
	prettyJSON, _ := json.MarshalIndent(raw["input"], "", "  ")
	t.Logf("input array:\n%s", prettyJSON)

	// input[0]: user message
	item0 := input[0].(map[string]any)
	if item0["role"] != "user" {
		t.Errorf("input[0] role = %v, want 'user'", item0["role"])
	}

	// input[1]: assistant text (replayed for multi-turn context)
	item1 := input[1].(map[string]any)
	if item1["role"] != "assistant" {
		t.Errorf("input[1] role = %v, want 'assistant'", item1["role"])
	}
	if item1["content"] != "I'll list the files for you." {
		t.Errorf("input[1] content = %v, want assistant text", item1["content"])
	}

	// input[2]: function_call — uses "call_id" not "id" in input
	item2 := input[2].(map[string]any)
	if item2["type"] != "function_call" {
		t.Errorf("input[2] type = %v, want 'function_call'", item2["type"])
	}
	if item2["call_id"] != "call_abc123" {
		t.Errorf("input[2] call_id = %v, want 'call_abc123'", item2["call_id"])
	}
	if _, hasID := item2["id"]; hasID {
		t.Error("function_call input items should use 'call_id', not 'id'")
	}

	// input[3]: function_call_output
	item3 := input[3].(map[string]any)
	if item3["type"] != "function_call_output" {
		t.Errorf("input[3] type = %v, want 'function_call_output'", item3["type"])
	}
	if item3["call_id"] != "call_abc123" {
		t.Errorf("input[3] call_id = %v, want 'call_abc123'", item3["call_id"])
	}

	// 4 items: user, assistant text, function_call, function_call_output
	if len(input) != 4 {
		t.Errorf("input length = %d, want 4 (user, assistant_text, function_call, function_call_output)", len(input))
	}
}

// TestTranslateRequest_EmptyToolResult_KeepsOutputField verifies that a
// function_call_output item always serializes the `output` field, even when
// the tool returned an empty string. OpenRouter's strict Zod validator for
// the OpenAI Responses API rejects the request when `output` is missing
// (issue #114). OpenAI itself is lenient and accepts it, so this bug only
// surfaces on OpenRouter and OpenRouter-proxied models (GLM, Qwen, Kimi).
func TestTranslateRequest_EmptyToolResult_KeepsOutputField(t *testing.T) {
	req := &llm.Request{
		Model: "openai/gpt-4.1",
		Messages: []llm.Message{
			llm.UserMessage("call the tool"),
			llm.ToolResultMessage("call_empty", "", false),
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	input := raw["input"].([]any)
	fco := input[1].(map[string]any)
	if fco["type"] != "function_call_output" {
		t.Fatalf("expected function_call_output, got %v", fco["type"])
	}
	out, present := fco["output"]
	if !present {
		t.Fatal("output field must be present on function_call_output items " +
			"(required by OpenAI Responses API; OpenRouter rejects if missing)")
	}
	if out != "" {
		t.Errorf("output = %v, want empty string", out)
	}
}

// TestTranslateRequest_EmptyArguments_KeepsArgumentsField verifies that a
// function_call item always serializes the `arguments` field as a string.
// A no-argument tool call produces an empty string, which the Responses API
// spec requires to be present — strict validators reject `undefined`.
func TestTranslateRequest_EmptyArguments_KeepsArgumentsField(t *testing.T) {
	req := &llm.Request{
		Model: "openai/gpt-4.1",
		Messages: []llm.Message{
			{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{
					{Kind: llm.KindToolCall, ToolCall: &llm.ToolCallData{
						ID:   "call_noargs",
						Name: "pwd",
						// Arguments is nil → becomes empty string after string(...)
					}},
				},
			},
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	input := raw["input"].([]any)
	fc := input[0].(map[string]any)
	if fc["type"] != "function_call" {
		t.Fatalf("expected function_call, got %v", fc["type"])
	}
	if _, present := fc["name"]; !present {
		t.Error("name field must be present on function_call items")
	}
	if _, present := fc["arguments"]; !present {
		t.Error("arguments field must be present on function_call items (empty string is OK, undefined is not)")
	}
}
