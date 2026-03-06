// ABOUTME: Tests for the conformance CLI binary subcommand dispatch.
// ABOUTME: Covers subcommand dispatch, Tier 1 (LLM), Tier 2 (agent), and input/output format behaviors.
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
)

func TestNoSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance"}, nil, &stdout, &stderr)

	if code == 0 {
		t.Fatal("expected non-zero exit code when no subcommand given")
	}

	if stderr.Len() == 0 {
		t.Fatal("expected usage message on stderr")
	}
}

func TestUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "bogus-command"}, nil, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON on stdout, got: %s (err: %v)", stdout.String(), err)
	}

	errMsg, ok := result["error"].(string)
	if !ok {
		t.Fatal("expected 'error' key in JSON response")
	}
	if errMsg != "not implemented: bogus-command" {
		t.Fatalf("unexpected error message: %s", errMsg)
	}
}

func TestListModels(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "list-models"}, nil, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
	}

	var models []map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &models); err != nil {
		t.Fatalf("expected JSON array on stdout, got: %s (err: %v)", stdout.String(), err)
	}

	if len(models) == 0 {
		t.Fatal("expected at least one model in list")
	}

	// Verify each model has an "id" and "provider" field.
	for i, m := range models {
		if _, ok := m["id"]; !ok {
			t.Fatalf("model %d missing 'id' field", i)
		}
		if _, ok := m["provider"]; !ok {
			t.Fatalf("model %d missing 'provider' field", i)
		}
	}

	// Check that known model IDs are present.
	ids := make(map[string]bool)
	for _, m := range models {
		ids[m["id"].(string)] = true
	}

	expectedIDs := []string{"claude-opus-4-6", "gpt-5.2", "gemini-3-pro-preview"}
	for _, expected := range expectedIDs {
		if !ids[expected] {
			t.Errorf("expected model ID %q in list", expected)
		}
	}
}

func TestClientFromEnvWithKeys(t *testing.T) {
	// Set a mock API key so the client-from-env subcommand can find it.
	t.Setenv("ANTHROPIC_API_KEY", "test-key-anthropic")
	t.Setenv("OPENAI_API_KEY", "test-key-openai")
	// Clear google key to test partial provider detection.
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "client-from-env"}, nil, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON on stdout, got: %s (err: %v)", stdout.String(), err)
	}

	if result["status"] != "ok" {
		t.Fatalf("expected status 'ok', got %v", result["status"])
	}

	providers, ok := result["providers"].([]interface{})
	if !ok {
		t.Fatal("expected 'providers' to be an array")
	}

	providerSet := make(map[string]bool)
	for _, p := range providers {
		providerSet[p.(string)] = true
	}

	if !providerSet["anthropic"] {
		t.Error("expected 'anthropic' in providers list")
	}
	if !providerSet["openai"] {
		t.Error("expected 'openai' in providers list")
	}
	if providerSet["google"] {
		t.Error("did not expect 'google' in providers list (no key set)")
	}
}

func TestClientFromEnvNoKeys(t *testing.T) {
	// Clear all API keys.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "client-from-env"}, nil, &stdout, &stderr)

	if code == 0 {
		t.Fatal("expected non-zero exit code when no keys configured")
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON on stdout, got: %s (err: %v)", stdout.String(), err)
	}

	if _, ok := result["error"]; !ok {
		t.Fatal("expected 'error' key in JSON response")
	}
}

func TestUnknownSubcommandNotImplemented(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "totally-fake"}, nil, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1 for unknown command, got %d", code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON, got: %s (err: %v)", stdout.String(), err)
	}
	if result["error"] != "not implemented: totally-fake" {
		t.Fatalf("unexpected error: %v", result["error"])
	}
}

// --- benchRequest parsing tests ---

func TestParseBenchRequestSimpleContent(t *testing.T) {
	input := `{
		"model": "gpt-4o",
		"provider": "openai",
		"messages": [{"role": "user", "content": "Say hello"}],
		"max_tokens": 100
	}`

	var br benchRequest
	if err := json.Unmarshal([]byte(input), &br); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if br.Model != "gpt-4o" {
		t.Fatalf("expected model gpt-4o, got %s", br.Model)
	}
	if br.Provider != "openai" {
		t.Fatalf("expected provider openai, got %s", br.Provider)
	}
	if br.MaxTokens == nil || *br.MaxTokens != 100 {
		t.Fatal("expected max_tokens 100")
	}
	if len(br.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(br.Messages))
	}

	msg := br.Messages[0]
	if msg.Role != "user" {
		t.Fatalf("expected role user, got %s", string(msg.Role))
	}
	if len(msg.Content) != 1 || msg.Content[0].Kind != llm.KindText || msg.Content[0].Text != "Say hello" {
		t.Fatalf("expected text content 'Say hello', got %+v", msg.Content)
	}
}

func TestParseBenchRequestArrayContent(t *testing.T) {
	input := `{
		"model": "claude-opus-4-6",
		"provider": "anthropic",
		"messages": [{"role": "user", "content": [{"kind": "text", "text": "Hello world"}]}],
		"max_tokens": 50
	}`

	var br benchRequest
	if err := json.Unmarshal([]byte(input), &br); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if len(br.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(br.Messages))
	}

	msg := br.Messages[0]
	if len(msg.Content) != 1 || msg.Content[0].Kind != llm.KindText || msg.Content[0].Text != "Hello world" {
		t.Fatalf("expected text content 'Hello world', got %+v", msg.Content)
	}
}

func TestParseBenchRequestMultipleMessages(t *testing.T) {
	input := `{
		"model": "gpt-4o",
		"provider": "openai",
		"messages": [
			{"role": "system", "content": "You are helpful"},
			{"role": "user", "content": "Hi"}
		]
	}`

	var br benchRequest
	if err := json.Unmarshal([]byte(input), &br); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if len(br.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(br.Messages))
	}

	if br.Messages[0].Role != "system" || br.Messages[0].Content[0].Text != "You are helpful" {
		t.Fatalf("unexpected system message: %+v", br.Messages[0])
	}
	if br.Messages[1].Role != "user" || br.Messages[1].Content[0].Text != "Hi" {
		t.Fatalf("unexpected user message: %+v", br.Messages[1])
	}
}

func TestParseBenchRequestWithTools(t *testing.T) {
	input := `{
		"model": "gpt-4o",
		"provider": "openai",
		"messages": [{"role": "user", "content": "What is the weather?"}],
		"tools": [{"name": "get_weather", "description": "Get weather", "parameters": {"type": "object"}}]
	}`

	var br benchRequest
	if err := json.Unmarshal([]byte(input), &br); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if len(br.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(br.Tools))
	}
	if br.Tools[0].Name != "get_weather" {
		t.Fatalf("expected tool name get_weather, got %s", br.Tools[0].Name)
	}
}

func TestParseBenchRequestWithResponseSchema(t *testing.T) {
	input := `{
		"model": "gpt-4o",
		"provider": "openai",
		"messages": [{"role": "user", "content": "Generate a person"}],
		"response_schema": {"type": "object", "properties": {"name": {"type": "string"}}}
	}`

	var br benchRequest
	if err := json.Unmarshal([]byte(input), &br); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if br.ResponseSchema == nil {
		t.Fatal("expected response_schema to be set")
	}
}

func TestBenchRequestToLLMRequest(t *testing.T) {
	maxTokens := 100
	br := benchRequest{
		Model:    "gpt-4o",
		Provider: "openai",
		Messages: []benchMessage{
			{Role: "user", Content: []llm.ContentPart{{Kind: llm.KindText, Text: "Hi"}}},
		},
		MaxTokens: &maxTokens,
	}

	req := br.toLLMRequest()

	if req.Model != "gpt-4o" {
		t.Fatalf("expected model gpt-4o, got %s", req.Model)
	}
	if req.Provider != "openai" {
		t.Fatalf("expected provider openai, got %s", req.Provider)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 100 {
		t.Fatal("expected max_tokens 100")
	}
	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	if req.Messages[0].Text() != "Hi" {
		t.Fatalf("expected text Hi, got %s", req.Messages[0].Text())
	}
}

func TestBenchRequestToLLMRequestWithResponseSchema(t *testing.T) {
	schema := json.RawMessage(`{"type": "object"}`)
	br := benchRequest{
		Model:    "gpt-4o",
		Provider: "openai",
		Messages: []benchMessage{
			{Role: "user", Content: []llm.ContentPart{{Kind: llm.KindText, Text: "Generate"}}},
		},
		ResponseSchema: &schema,
	}

	req := br.toLLMRequest()

	if req.ResponseFormat == nil {
		t.Fatal("expected ResponseFormat to be set")
	}
	if req.ResponseFormat.Type != "json_schema" {
		t.Fatalf("expected type json_schema, got %s", req.ResponseFormat.Type)
	}
}

// --- formatCompleteResponse tests ---

func TestFormatCompleteResponse(t *testing.T) {
	resp := &llm.Response{
		ID:       "resp-123",
		Model:    "gpt-4o",
		Provider: "openai",
		Message: llm.Message{
			Role:    llm.RoleAssistant,
			Content: []llm.ContentPart{{Kind: llm.KindText, Text: "Hello!"}},
		},
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage: llm.Usage{
			InputTokens:  10,
			OutputTokens: 5,
			TotalTokens:  15,
		},
	}

	result := formatCompleteResponse(resp, "openai")

	if result["id"] != "resp-123" {
		t.Fatalf("expected id resp-123, got %v", result["id"])
	}
	if result["text"] != "Hello!" {
		t.Fatalf("expected text Hello!, got %v", result["text"])
	}
	if result["content"] != "Hello!" {
		t.Fatalf("expected content Hello!, got %v", result["content"])
	}
	if result["model"] != "gpt-4o" {
		t.Fatalf("expected model gpt-4o, got %v", result["model"])
	}
	if result["provider"] != "openai" {
		t.Fatalf("expected provider openai, got %v", result["provider"])
	}
	if result["finish_reason"] != "stop" {
		t.Fatalf("expected finish_reason stop, got %v", result["finish_reason"])
	}

	usage, ok := result["usage"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected usage to be a map, got %T", result["usage"])
	}
	if usage["input_tokens"] != 10 {
		t.Fatalf("expected input_tokens 10, got %v", usage["input_tokens"])
	}
	if usage["output_tokens"] != 5 {
		t.Fatalf("expected output_tokens 5, got %v", usage["output_tokens"])
	}
	if usage["total_tokens"] != 15 {
		t.Fatalf("expected total_tokens 15, got %v", usage["total_tokens"])
	}
}

func TestFormatCompleteResponseWithToolCalls(t *testing.T) {
	resp := &llm.Response{
		ID:       "resp-456",
		Model:    "gpt-4o",
		Provider: "openai",
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				{Kind: llm.KindText, Text: "Let me check"},
				{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCallData{
						ID:        "call-1",
						Name:      "get_weather",
						Arguments: json.RawMessage(`{"location":"NYC"}`),
					},
				},
			},
		},
		FinishReason: llm.FinishReason{Reason: "tool_use"},
		Usage:        llm.Usage{InputTokens: 20, OutputTokens: 10, TotalTokens: 30},
	}

	result := formatCompleteResponse(resp, "openai")

	toolCalls, ok := result["tool_calls"].([]map[string]interface{})
	if !ok {
		t.Fatalf("expected tool_calls to be a slice of maps, got %T", result["tool_calls"])
	}
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0]["id"] != "call-1" {
		t.Fatalf("expected tool call id call-1, got %v", toolCalls[0]["id"])
	}
	if toolCalls[0]["name"] != "get_weather" {
		t.Fatalf("expected tool call name get_weather, got %v", toolCalls[0]["name"])
	}
}

// --- formatStreamEvent tests ---

func TestFormatStreamEventStart(t *testing.T) {
	event := llm.StreamEvent{Type: llm.EventStreamStart}
	result := formatStreamEvent(event)

	if result["type"] != "STREAM_START" {
		t.Fatalf("expected type STREAM_START, got %v", result["type"])
	}
}

func TestFormatStreamEventTextDelta(t *testing.T) {
	event := llm.StreamEvent{Type: llm.EventTextDelta, Delta: "hello"}
	result := formatStreamEvent(event)

	if result["type"] != "TEXT_DELTA" {
		t.Fatalf("expected type TEXT_DELTA, got %v", result["type"])
	}
	if result["text"] != "hello" {
		t.Fatalf("expected text hello, got %v", result["text"])
	}
}

func TestFormatStreamEventFinish(t *testing.T) {
	usage := &llm.Usage{InputTokens: 5, OutputTokens: 3, TotalTokens: 8}
	event := llm.StreamEvent{
		Type:  llm.EventFinish,
		Usage: usage,
	}
	result := formatStreamEvent(event)

	if result["type"] != "FINISH" {
		t.Fatalf("expected type FINISH, got %v", result["type"])
	}
	usageMap, ok := result["usage"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected usage map, got %T", result["usage"])
	}
	if usageMap["input_tokens"] != 5 {
		t.Fatalf("expected input_tokens 5, got %v", usageMap["input_tokens"])
	}
}

// --- Subcommand error handling tests ---

func TestCompleteSubcommandBadJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader("not valid json")
	code := run([]string{"conformance", "complete"}, stdin, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1 for bad JSON, got %d", code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON error on stdout, got: %s (err: %v)", stdout.String(), err)
	}
	if _, ok := result["error"]; !ok {
		t.Fatal("expected 'error' key in JSON response")
	}
}

func TestStreamSubcommandBadJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader("{invalid")
	code := run([]string{"conformance", "stream"}, stdin, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1 for bad JSON, got %d", code)
	}
}

func TestToolCallSubcommandBadJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader("xxx")
	code := run([]string{"conformance", "tool-call"}, stdin, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1 for bad JSON, got %d", code)
	}
}

func TestGenerateObjectSubcommandBadJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader("yyy")
	code := run([]string{"conformance", "generate-object"}, stdin, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1 for bad JSON, got %d", code)
	}
}

func TestCompleteSubcommandNoAPIKey(t *testing.T) {
	// Clear all API keys so client creation fails.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	input := `{"model": "gpt-4o", "provider": "openai", "messages": [{"role": "user", "content": "Hi"}]}`
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "complete"}, strings.NewReader(input), &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1 when no API keys, got %d", code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON error, got: %s", stdout.String())
	}
	if _, ok := result["error"]; !ok {
		t.Fatal("expected error key in response")
	}
}

// --- benchMessage UnmarshalJSON edge cases ---

func TestBenchMessageEmptyContent(t *testing.T) {
	input := `{"role": "user", "content": ""}`
	var msg benchMessage
	if err := json.Unmarshal([]byte(input), &msg); err != nil {
		t.Fatalf("failed to unmarshal empty content: %v", err)
	}
	if len(msg.Content) != 1 || msg.Content[0].Text != "" {
		t.Fatalf("expected single empty text part, got %+v", msg.Content)
	}
}

func TestBenchMessageNullContent(t *testing.T) {
	input := `{"role": "user", "content": null}`
	var msg benchMessage
	if err := json.Unmarshal([]byte(input), &msg); err != nil {
		t.Fatalf("failed to unmarshal null content: %v", err)
	}
	if len(msg.Content) != 0 {
		t.Fatalf("expected no content parts for null, got %d", len(msg.Content))
	}
}

// --- Tier 2 tests ---

func TestSessionCreateNoAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "session-create"}, nil, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1 when no API keys, got %d", code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON error, got: %s", stdout.String())
	}
	if _, ok := result["error"]; !ok {
		t.Fatal("expected error key in response")
	}
}

func TestSessionCreateWithKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "session-create"}, nil, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON on stdout, got: %s (err: %v)", stdout.String(), err)
	}

	if result["status"] != "created" {
		t.Fatalf("expected status 'created', got %v", result["status"])
	}
	if _, ok := result["session_id"]; !ok {
		t.Fatal("expected session_id in response")
	}
	if result["session_id"] == "" {
		t.Fatal("session_id should not be empty")
	}
}

func TestToolDispatchUnknownTool(t *testing.T) {
	input := `{"tool_name": "nonexistent_tool", "arguments": {}}`
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "tool-dispatch"}, strings.NewReader(input), &stdout, &stderr)

	// Should exit 0 (graceful error, not crash).
	if code != 0 {
		t.Fatalf("expected exit code 0 for unknown tool, got %d", code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON on stdout, got: %s (err: %v)", stdout.String(), err)
	}

	errMsg, ok := result["error"].(string)
	if !ok {
		t.Fatal("expected error key in response")
	}
	if !strings.Contains(errMsg, "unknown tool") {
		t.Fatalf("expected 'unknown tool' in error, got %q", errMsg)
	}
}

func TestToolDispatchBadJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "tool-dispatch"}, strings.NewReader("not json"), &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1 for bad JSON, got %d", code)
	}
}

func TestToolDispatchApplyPatch(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	defer os.Chdir(oldWd)

	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	input := `{"tool_name":"apply_patch","arguments":{"patch":"*** Begin Patch\n*** Update File: code.txt\n@@\n-before\n+after\n*** End Patch\n"}}`
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "tool-dispatch"}, strings.NewReader(input), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stdout: %s; stderr: %s", code, stdout.String(), stderr.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON on stdout, got: %s (err: %v)", stdout.String(), err)
	}
	if _, ok := result["error"]; ok {
		t.Fatalf("expected apply_patch to succeed, got error response: %v", result["error"])
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if string(data) != "after\n" {
		t.Fatalf("patched content = %q, want %q", string(data), "after\n")
	}
}

func TestSteeringAcknowledgment(t *testing.T) {
	input := `{"message": "focus on error handling"}`
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "steering"}, strings.NewReader(input), &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON, got: %s (err: %v)", stdout.String(), err)
	}

	if result["status"] != "acknowledged" {
		t.Fatalf("expected status 'acknowledged', got %v", result["status"])
	}
	if result["acknowledged"] != true {
		t.Fatalf("expected acknowledged true, got %v", result["acknowledged"])
	}
	if result["message"] != "focus on error handling" {
		t.Fatalf("expected message echoed back, got %v", result["message"])
	}
}

func TestSteeringBadJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "steering"}, strings.NewReader("bad"), &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1 for bad JSON, got %d", code)
	}
}

func TestEventsOutputFormat(t *testing.T) {
	// Events should work even without API keys (emits synthetic events).
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "events"}, nil, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
	}

	// Parse NDJSON lines.
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 events, got %d lines: %s", len(lines), stdout.String())
	}

	// Each line should have a "type" field.
	for i, line := range lines {
		var evt map[string]interface{}
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			t.Fatalf("event line %d is not valid JSON: %s (err: %v)", i, line, err)
		}
		if _, ok := evt["type"]; !ok {
			t.Fatalf("event line %d missing 'type' field: %s", i, line)
		}
	}
}

func TestProcessInputBadJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "process-input"}, strings.NewReader("zzz"), &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1 for bad JSON, got %d", code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON error, got: %s", stdout.String())
	}
	if _, ok := result["error"]; !ok {
		t.Fatal("expected error key in response")
	}
}

func TestProcessInputNoAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	input := `{"prompt": "Say hello", "model": "gpt-4o", "provider": "openai"}`
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "process-input"}, strings.NewReader(input), &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1 when no API keys, got %d", code)
	}
}

// --- Tier 3 tests ---

func TestParseSimpleDOT(t *testing.T) {
	var stdout, stderr bytes.Buffer
	dotFile := "../../pipeline/testdata/simple.dot"
	code := run([]string{"conformance", "parse", dotFile}, nil, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s; stdout: %s", code, stderr.String(), stdout.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON, got: %s (err: %v)", stdout.String(), err)
	}

	nodes, ok := result["nodes"].([]interface{})
	if !ok {
		t.Fatalf("expected nodes array, got %T", result["nodes"])
	}
	if len(nodes) == 0 {
		t.Fatal("expected at least one node")
	}

	edges, ok := result["edges"].([]interface{})
	if !ok {
		t.Fatalf("expected edges array, got %T", result["edges"])
	}
	if len(edges) == 0 {
		t.Fatal("expected at least one edge")
	}

	// Check nodes have required fields.
	for i, n := range nodes {
		node := n.(map[string]interface{})
		if _, ok := node["id"]; !ok {
			t.Fatalf("node %d missing 'id'", i)
		}
		if _, ok := node["shape"]; !ok {
			t.Fatalf("node %d missing 'shape'", i)
		}
	}
}

func TestParseMissingFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "parse", "/nonexistent/file.dot"}, nil, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1 for missing file, got %d", code)
	}
}

func TestParseNoArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "parse"}, nil, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1 for no file arg, got %d", code)
	}
}

func TestValidateSimpleDOT(t *testing.T) {
	var stdout, stderr bytes.Buffer
	dotFile := "../../pipeline/testdata/simple.dot"
	code := run([]string{"conformance", "validate", dotFile}, nil, &stdout, &stderr)

	// Should always exit 0, even on errors (diagnostics carry the info).
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON, got: %s (err: %v)", stdout.String(), err)
	}

	diagnostics, ok := result["diagnostics"].([]interface{})
	if !ok {
		t.Fatalf("expected diagnostics array, got %T", result["diagnostics"])
	}

	// simple.dot should be valid (no diagnostics).
	if len(diagnostics) != 0 {
		t.Fatalf("expected no diagnostics for valid file, got %d", len(diagnostics))
	}
}

func TestValidateInvalidDOT(t *testing.T) {
	var stdout, stderr bytes.Buffer
	dotFile := "../../pipeline/testdata/cycle.dot"
	code := run([]string{"conformance", "validate", dotFile}, nil, &stdout, &stderr)

	// Should always exit 0.
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("expected JSON, got: %s (err: %v)", stdout.String(), err)
	}

	diagnostics, ok := result["diagnostics"].([]interface{})
	if !ok {
		t.Fatalf("expected diagnostics array, got %T", result["diagnostics"])
	}

	// cycle.dot should have validation errors.
	if len(diagnostics) == 0 {
		t.Fatal("expected diagnostics for invalid file")
	}

	// Each diagnostic should have severity and message.
	for i, d := range diagnostics {
		diag := d.(map[string]interface{})
		if _, ok := diag["severity"]; !ok {
			t.Fatalf("diagnostic %d missing 'severity'", i)
		}
		if _, ok := diag["message"]; !ok {
			t.Fatalf("diagnostic %d missing 'message'", i)
		}
	}
}

func TestListHandlers(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "list-handlers"}, nil, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
	}

	var handlerList []string
	if err := json.Unmarshal(stdout.Bytes(), &handlerList); err != nil {
		t.Fatalf("expected JSON array of strings, got: %s (err: %v)", stdout.String(), err)
	}

	if len(handlerList) == 0 {
		t.Fatal("expected at least one handler")
	}

	// Check for required handlers.
	handlerSet := make(map[string]bool)
	for _, h := range handlerList {
		handlerSet[h] = true
	}

	required := []string{"start", "codergen", "exit", "stack.manager_loop"}
	for _, r := range required {
		if !handlerSet[r] {
			t.Errorf("expected handler %q in list", r)
		}
	}
}

func TestRunNoArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"conformance", "run"}, nil, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1 for no file arg, got %d", code)
	}
}
