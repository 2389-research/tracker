// ABOUTME: Tests for the OpenAI Responses API adapter.
// ABOUTME: Validates request/response translation, SSE stream parsing, and finish reason mapping.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// --- Request translation tests ---

func TestTranslateRequestBasic(t *testing.T) {
	req := &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Hello")},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}

	if raw["model"] != "gpt-4.1" {
		t.Errorf("expected model gpt-4.1, got %v", raw["model"])
	}

	input, ok := raw["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("expected 1 input item, got %v", raw["input"])
	}

	item := input[0].(map[string]any)
	if item["role"] != "user" || item["content"] != "Hello" {
		t.Errorf("unexpected input item: %v", item)
	}
}

func TestTranslateRequestSystemToInstructions(t *testing.T) {
	req := &llm.Request{
		Model: "gpt-4.1",
		Messages: []llm.Message{
			llm.SystemMessage("Be helpful"),
			{Role: llm.RoleDeveloper, Content: []llm.ContentPart{{Kind: llm.KindText, Text: "Also be concise"}}},
			llm.UserMessage("Hello"),
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

	instructions, ok := raw["instructions"].(string)
	if !ok {
		t.Fatal("expected instructions string")
	}
	if !strings.Contains(instructions, "Be helpful") || !strings.Contains(instructions, "Also be concise") {
		t.Errorf("instructions should contain both system and developer text, got: %s", instructions)
	}

	// System/developer messages should NOT be in the input array.
	input := raw["input"].([]any)
	if len(input) != 1 {
		t.Errorf("expected 1 input item (user only), got %d", len(input))
	}
}

func TestTranslateRequestDefaultMaxOutputTokens(t *testing.T) {
	req := &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Hi")},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(body, &raw)

	maxTokens, ok := raw["max_output_tokens"].(float64)
	if !ok || int(maxTokens) != 16384 {
		t.Errorf("expected max_output_tokens 16384, got %v", raw["max_output_tokens"])
	}
}

func TestTranslateRequestToolDefinitions(t *testing.T) {
	req := &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Hi")},
		Tools: []llm.ToolDefinition{
			{
				Name:        "read_file",
				Description: "Read a file",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
			},
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(body, &raw)

	tools := raw["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("expected tool type 'function', got %v", tool["type"])
	}
	if tool["name"] != "read_file" {
		t.Errorf("expected tool name 'read_file', got %v", tool["name"])
	}
}

func TestTranslateRequestToolCallInInput(t *testing.T) {
	req := &llm.Request{
		Model: "gpt-4.1",
		Messages: []llm.Message{
			llm.UserMessage("Hello"),
			{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{
					{Kind: llm.KindToolCall, ToolCall: &llm.ToolCallData{
						ID:        "call_123",
						Name:      "read_file",
						Arguments: json.RawMessage(`{"path":"foo.txt"}`),
					}},
				},
			},
			llm.ToolResultMessage("call_123", "file contents", false),
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(body, &raw)

	input := raw["input"].([]any)
	if len(input) != 3 {
		t.Fatalf("expected 3 input items, got %d", len(input))
	}

	// Second item should be function_call.
	fc := input[1].(map[string]any)
	if fc["type"] != "function_call" {
		t.Errorf("expected function_call, got %v", fc["type"])
	}
	if fc["call_id"] != "call_123" {
		t.Errorf("expected call_id call_123, got %v", fc["call_id"])
	}
	if fc["name"] != "read_file" {
		t.Errorf("expected name read_file, got %v", fc["name"])
	}

	// Third item should be function_call_output.
	fco := input[2].(map[string]any)
	if fco["type"] != "function_call_output" {
		t.Errorf("expected function_call_output, got %v", fco["type"])
	}
	if fco["call_id"] != "call_123" {
		t.Errorf("expected call_id call_123, got %v", fco["call_id"])
	}
}

func TestTranslateRequestCallIDFallback(t *testing.T) {
	req := &llm.Request{
		Model: "gpt-5.2",
		Messages: []llm.Message{
			llm.UserMessage("hi"),
			{
				Role: llm.RoleTool,
				Content: []llm.ContentPart{{
					Kind: llm.KindToolResult,
					ToolResult: &llm.ToolResultData{
						ToolCallID: "",
						Content:    "result",
					},
				}},
			},
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(body, &raw)
	input := raw["input"].([]any)

	fco := input[1].(map[string]any)
	callID, ok := fco["call_id"]
	if !ok {
		t.Fatal("call_id field must be present on function_call_output items")
	}
	if callID != "call_unknown" {
		t.Errorf("expected fallback 'call_unknown', got %v", callID)
	}

	// Verify user messages don't get a spurious call_id
	userMsg := input[0].(map[string]any)
	if _, has := userMsg["call_id"]; has {
		t.Error("user message should not have call_id field")
	}
}

func TestTranslateRequestFunctionCallUsesCallID(t *testing.T) {
	// The OpenAI Responses API requires function_call input items to use
	// "call_id", not "id". This test verifies that echoing back a tool call
	// from the assistant produces "call_id" in the JSON.
	req := &llm.Request{
		Model: "gpt-5.4",
		Messages: []llm.Message{
			llm.UserMessage("Do something"),
			{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{
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

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(body, &raw)

	input := raw["input"].([]any)
	if len(input) != 3 {
		t.Fatalf("expected 3 input items, got %d", len(input))
	}

	// The function_call item (input[1]) MUST have "call_id", not "id"
	fc := input[1].(map[string]any)
	if fc["type"] != "function_call" {
		t.Errorf("expected function_call, got %v", fc["type"])
	}
	if _, hasID := fc["id"]; hasID {
		t.Error("function_call input items should use 'call_id', not 'id'")
	}
	if fc["call_id"] != "call_abc123" {
		t.Errorf("expected call_id 'call_abc123', got %v", fc["call_id"])
	}
}

func TestStreamToolCallCapturesCallID(t *testing.T) {
	// GPT-5.4 sends call_id on function_call items in SSE events.
	// The adapter must capture call_id and use it as the ToolCallData.ID.
	sseData := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_tc","model":"gpt-5.4"}}`,
		"",
		"event: response.output_item.added",
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_item_001","call_id":"call_real_id","name":"bash"}}`,
		"",
		"event: response.function_call_arguments.delta",
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"cmd\":\"ls\"}"}`,
		"",
		"event: response.output_item.done",
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_item_001","call_id":"call_real_id","name":"bash","arguments":"{\"cmd\":\"ls\"}"}}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"id":"resp_tc","status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15},"output":[{"type":"function_call","id":"fc_item_001","call_id":"call_real_id","name":"bash","arguments":"{\"cmd\":\"ls\"}"}]}}`,
		"",
	}, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseData)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	ch := a.Stream(context.Background(), &llm.Request{
		Model:    "gpt-5.4",
		Messages: []llm.Message{llm.UserMessage("Run ls")},
	})

	var events []llm.StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	// Find the ToolCallStart event
	var toolStart *llm.StreamEvent
	for i := range events {
		if events[i].Type == llm.EventToolCallStart {
			toolStart = &events[i]
			break
		}
	}
	if toolStart == nil {
		t.Fatal("expected ToolCallStart event")
	}
	// The ID should be call_id ("call_real_id"), not the item id ("fc_item_001")
	if toolStart.ToolCall.ID != "call_real_id" {
		t.Errorf("expected tool call ID 'call_real_id' (from call_id), got %q", toolStart.ToolCall.ID)
	}

	// Find the ToolCallEnd event — it should carry the complete tool call data
	var toolEnd *llm.StreamEvent
	for i := range events {
		if events[i].Type == llm.EventToolCallEnd {
			toolEnd = &events[i]
			break
		}
	}
	if toolEnd == nil {
		t.Fatal("expected ToolCallEnd event")
	}
	if toolEnd.ToolCall == nil {
		t.Fatal("ToolCallEnd should include ToolCall data")
	}
	if toolEnd.ToolCall.ID != "call_real_id" {
		t.Errorf("ToolCallEnd ID = %q, want 'call_real_id'", toolEnd.ToolCall.ID)
	}
	if toolEnd.ToolCall.Name != "bash" {
		t.Errorf("ToolCallEnd Name = %q, want 'bash'", toolEnd.ToolCall.Name)
	}
}

func TestTranslateRequestToolChoiceModes(t *testing.T) {
	tests := []struct {
		mode     string
		toolName string
		want     any
	}{
		{"auto", "", "auto"},
		{"none", "", "none"},
		{"required", "", "required"},
		{"named", "read_file", map[string]string{"type": "function", "name": "read_file"}},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			req := &llm.Request{
				Model:    "gpt-4.1",
				Messages: []llm.Message{llm.UserMessage("Hi")},
				ToolChoice: &llm.ToolChoice{
					Mode:     tt.mode,
					ToolName: tt.toolName,
				},
			}

			body, err := translateRequest(req)
			if err != nil {
				t.Fatal(err)
			}

			var raw map[string]any
			json.Unmarshal(body, &raw)

			got := raw["tool_choice"]
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tt.want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("tool_choice: got %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestTranslateRequestReasoningEffort(t *testing.T) {
	req := &llm.Request{
		Model:           "o3",
		Messages:        []llm.Message{llm.UserMessage("Think hard")},
		ReasoningEffort: "high",
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(body, &raw)

	reasoning := raw["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" {
		t.Errorf("expected reasoning effort 'high', got %v", reasoning["effort"])
	}
}

func TestTranslateRequestReasoningEffortFromProviderOptions(t *testing.T) {
	req := &llm.Request{
		Model:    "o3",
		Messages: []llm.Message{llm.UserMessage("Think")},
		ProviderOptions: map[string]any{
			"openai": map[string]any{
				"reasoning_effort": "medium",
			},
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(body, &raw)

	reasoning := raw["reasoning"].(map[string]any)
	if reasoning["effort"] != "medium" {
		t.Errorf("expected reasoning effort 'medium', got %v", reasoning["effort"])
	}
}

// --- Response translation tests ---

func TestTranslateResponseBasic(t *testing.T) {
	raw := `{
		"id": "resp_123",
		"model": "gpt-4.1-2025-04-14",
		"output": [
			{
				"type": "message",
				"role": "assistant",
				"content": [{"type": "output_text", "text": "Hello!"}]
			}
		],
		"usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
		"status": "completed"
	}`

	resp, err := translateResponse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	if resp.ID != "resp_123" {
		t.Errorf("expected id resp_123, got %s", resp.ID)
	}
	if resp.Model != "gpt-4.1-2025-04-14" {
		t.Errorf("expected model gpt-4.1-2025-04-14, got %s", resp.Model)
	}
	if resp.Text() != "Hello!" {
		t.Errorf("expected text 'Hello!', got %q", resp.Text())
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
	if resp.FinishReason.Reason != "stop" {
		t.Errorf("expected finish reason 'stop', got %q", resp.FinishReason.Reason)
	}
}

func TestTranslateResponseToolCall(t *testing.T) {
	raw := `{
		"id": "resp_456",
		"model": "gpt-4.1",
		"output": [
			{
				"type": "function_call",
				"id": "call_789",
				"name": "read_file",
				"arguments": "{\"path\":\"foo.txt\"}"
			}
		],
		"usage": {"input_tokens": 20, "output_tokens": 10, "total_tokens": 30},
		"status": "completed"
	}`

	resp, err := translateResponse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	calls := resp.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].ID != "call_789" {
		t.Errorf("expected tool call ID 'call_789', got %q", calls[0].ID)
	}
	if calls[0].Name != "read_file" {
		t.Errorf("expected tool name 'read_file', got %q", calls[0].Name)
	}
	if resp.FinishReason.Reason != "tool_calls" {
		t.Errorf("expected finish reason 'tool_calls', got %q", resp.FinishReason.Reason)
	}
}

func TestTranslateResponseToolCallPrefersCallID(t *testing.T) {
	// When the API returns both id and call_id on a function_call output item,
	// translateResponse must prefer call_id (the callable reference) over id
	// (the item-level identifier).
	raw := `{
		"id": "resp_dual",
		"model": "gpt-5.4",
		"output": [
			{
				"type": "function_call",
				"id": "fc_item_001",
				"call_id": "call_real_id",
				"name": "bash",
				"arguments": "{\"cmd\":\"ls\"}"
			}
		],
		"usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
		"status": "completed"
	}`

	resp, err := translateResponse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	calls := resp.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].ID != "call_real_id" {
		t.Errorf("expected tool call ID 'call_real_id' (from call_id), got %q", calls[0].ID)
	}
}

func TestTranslateResponseReasoning(t *testing.T) {
	raw := `{
		"id": "resp_r1",
		"model": "o3",
		"output": [
			{
				"type": "reasoning",
				"summary": [
					{"type": "summary_text", "text": "Let me think about this..."}
				]
			},
			{
				"type": "message",
				"role": "assistant",
				"content": [{"type": "output_text", "text": "The answer is 42."}]
			}
		],
		"usage": {"input_tokens": 10, "output_tokens": 50, "total_tokens": 60, "output_tokens_details": {"reasoning_tokens": 40}},
		"status": "completed"
	}`

	resp, err := translateResponse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	reasoning := resp.Reasoning()
	if reasoning != "Let me think about this..." {
		t.Errorf("expected reasoning text 'Let me think about this...', got %q", reasoning)
	}
	if resp.Usage.ReasoningTokens == nil || *resp.Usage.ReasoningTokens != 40 {
		t.Errorf("expected reasoning tokens 40, got %v", resp.Usage.ReasoningTokens)
	}
}

// --- Finish reason tests ---

func TestTranslateFinishReason(t *testing.T) {
	tests := []struct {
		status     string
		hasCalls   bool
		incomplete *incompleteDetails
		wantReason string
	}{
		{"completed", false, nil, "stop"},
		{"completed", true, nil, "tool_calls"},
		{"incomplete", false, &incompleteDetails{Reason: "max_output_tokens"}, "length"},
		{"incomplete", false, &incompleteDetails{Reason: "content_filter"}, "content_filter"},
		{"failed", false, nil, "error"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_%v", tt.status, tt.hasCalls), func(t *testing.T) {
			fr := translateFinishReason(tt.status, tt.hasCalls, tt.incomplete)
			if fr.Reason != tt.wantReason {
				t.Errorf("got reason %q, want %q", fr.Reason, tt.wantReason)
			}
			if fr.Raw != tt.status {
				t.Errorf("got raw %q, want %q", fr.Raw, tt.status)
			}
		})
	}
}

// --- Adapter integration tests (httptest) ---

func TestAdapterComplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header.
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer auth, got %q", r.Header.Get("Authorization"))
		}

		// Verify request body.
		body, _ := io.ReadAll(r.Body)
		var raw map[string]any
		json.Unmarshal(body, &raw)

		if raw["model"] != "gpt-4.1" {
			t.Errorf("expected model gpt-4.1, got %v", raw["model"])
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{
			"id": "resp_test",
			"model": "gpt-4.1-2025-04-14",
			"output": [
				{
					"type": "message",
					"role": "assistant",
					"content": [{"type": "output_text", "text": "Hello from OpenAI!"}]
				}
			],
			"usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
			"status": "completed"
		}`)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	resp, err := a.Complete(context.Background(), &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Hello")},
	})
	if err != nil {
		t.Fatal(err)
	}

	if resp.Text() != "Hello from OpenAI!" {
		t.Errorf("expected 'Hello from OpenAI!', got %q", resp.Text())
	}
	if resp.Provider != "openai" {
		t.Errorf("expected provider 'openai', got %q", resp.Provider)
	}
}

func TestAdapterCompleteErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error": {"message": "invalid api key"}}`)
	}))
	defer server.Close()

	a := New("bad-key", WithBaseURL(server.URL))
	_, err := a.Complete(context.Background(), &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Hi")},
	})
	if err == nil {
		t.Fatal("expected error for 401")
	}

	var authErr *llm.AuthenticationError
	if !errors.As(err, &authErr) {
		t.Errorf("expected AuthenticationError, got %T: %v", err, err)
	}
}

func TestAdapterStream(t *testing.T) {
	sseData := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_s1","model":"gpt-4.1"}}`,
		"",
		"event: response.output_item.added",
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message"}}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Hello"}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":" world"}`,
		"",
		"event: response.output_item.done",
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message"}}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"id":"resp_s1","status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,"output_tokens_details":{"reasoning_tokens":3}},"output":[]}}`,
		"",
	}, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseData)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	ch := a.Stream(context.Background(), &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Hello")},
	})

	var events []llm.StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	// Should have: StreamStart, TextStart, TextDelta("Hello"), TextDelta(" world"), TextEnd, Finish
	if len(events) < 6 {
		t.Fatalf("expected at least 6 events, got %d: %+v", len(events), events)
	}

	if events[0].Type != llm.EventStreamStart {
		t.Errorf("first event should be StreamStart, got %v", events[0].Type)
	}
	if events[1].Type != llm.EventTextStart {
		t.Errorf("second event should be TextStart, got %v", events[1].Type)
	}
	if events[2].Delta != "Hello" {
		t.Errorf("expected delta 'Hello', got %q", events[2].Delta)
	}
	if events[3].Delta != " world" {
		t.Errorf("expected delta ' world', got %q", events[3].Delta)
	}
	if events[4].Type != llm.EventTextEnd {
		t.Errorf("fifth event should be TextEnd, got %v", events[4].Type)
	}
	if events[5].Type != llm.EventFinish {
		t.Errorf("last event should be Finish, got %v", events[5].Type)
	}
	if events[5].Usage == nil || events[5].Usage.ReasoningTokens == nil || *events[5].Usage.ReasoningTokens != 3 {
		t.Fatalf("expected finish usage reasoning tokens 3, got %+v", events[5].Usage)
	}
}

func TestAdapterStreamToolCall(t *testing.T) {
	sseData := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_tc","model":"gpt-4.1"}}`,
		"",
		"event: response.output_item.added",
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"call_abc","name":"read_file"}}`,
		"",
		"event: response.function_call_arguments.delta",
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"path\":"}`,
		"",
		"event: response.function_call_arguments.delta",
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"\"foo.txt\"}"}`,
		"",
		"event: response.output_item.done",
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"call_abc","name":"read_file","arguments":"{\"path\":\"foo.txt\"}"}}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"id":"resp_tc","status":"completed","usage":{"input_tokens":20,"output_tokens":15,"total_tokens":35},"output":[{"type":"function_call","id":"call_abc","name":"read_file","arguments":"{\"path\":\"foo.txt\"}"}]}}`,
		"",
	}, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseData)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	ch := a.Stream(context.Background(), &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Read foo.txt")},
	})

	var events []llm.StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	// StreamStart, ToolCallStart, ToolCallDelta x2, ToolCallEnd, Finish
	if len(events) < 6 {
		t.Fatalf("expected at least 6 events, got %d", len(events))
	}

	if events[1].Type != llm.EventToolCallStart {
		t.Errorf("expected ToolCallStart, got %v", events[1].Type)
	}
	if events[1].ToolCall == nil || events[1].ToolCall.Name != "read_file" {
		t.Errorf("expected tool call name 'read_file', got %+v", events[1].ToolCall)
	}
	if events[2].Type != llm.EventToolCallDelta {
		t.Errorf("expected ToolCallDelta, got %v", events[2].Type)
	}

	// Finish should have tool_calls reason.
	lastEvt := events[len(events)-1]
	if lastEvt.FinishReason == nil || lastEvt.FinishReason.Reason != "tool_calls" {
		t.Errorf("expected finish reason 'tool_calls', got %+v", lastEvt.FinishReason)
	}
}

func TestAdapterStreamWithoutEventLines(t *testing.T) {
	// SSE data with only "data:" lines, no "event:" lines.
	// The type must be inferred from the JSON payload's "type" field.
	sseData := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_s2","model":"gpt-4.1"}}`,
		"",
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message"}}`,
		"",
		`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Hello"}`,
		"",
		`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":" world"}`,
		"",
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message"}}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_s2","status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15},"output":[]}}`,
		"",
	}, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseData)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	ch := a.Stream(context.Background(), &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Hello")},
	})

	var events []llm.StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	if len(events) < 6 {
		t.Fatalf("expected at least 6 events, got %d: %+v", len(events), events)
	}

	if events[0].Type != llm.EventStreamStart {
		t.Errorf("first event should be StreamStart, got %v", events[0].Type)
	}
	if events[2].Delta != "Hello" {
		t.Errorf("expected delta 'Hello', got %q", events[2].Delta)
	}
	if events[5].Type != llm.EventFinish {
		t.Errorf("last event should be Finish, got %v", events[5].Type)
	}
}

func TestAdapterName(t *testing.T) {
	a := New("key")
	if a.Name() != "openai" {
		t.Errorf("expected 'openai', got %q", a.Name())
	}
}

func TestAdapterBaseURLWithV1Suffix(t *testing.T) {
	// When OPENAI_BASE_URL includes /v1 (the standard convention),
	// the adapter must not produce a double /v1/v1 path.
	var requestPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "resp_001",
			"output": [
				{
					"type": "message",
					"role": "assistant",
					"content": [{"type": "output_text", "text": "ok"}]
				}
			],
			"usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
			"status": "completed"
		}`)
	}))
	defer server.Close()

	// Simulate OPENAI_BASE_URL=http://host:port/v1
	a := New("test-key", WithBaseURL(server.URL+"/v1"))
	_, err := a.Complete(context.Background(), &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Hi")},
	})
	if err != nil {
		t.Fatal(err)
	}

	if requestPath != "/v1/responses" {
		t.Errorf("expected request path /v1/responses, got %q", requestPath)
	}
}

func TestAdapterProviderOptions(t *testing.T) {
	req := &llm.Request{
		Model:    "gpt-4.1",
		Messages: []llm.Message{llm.UserMessage("Hi")},
		ProviderOptions: map[string]any{
			"openai": map[string]any{
				"store": true,
			},
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	json.Unmarshal(body, &raw)

	if raw["store"] != true {
		t.Errorf("expected store=true from provider options, got %v", raw["store"])
	}
}
