// ABOUTME: Tests for the Anthropic Messages API adapter.
// ABOUTME: Validates request/response translation, SSE stream parsing, and finish reason mapping.
package anthropic

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

// --- translateRequest tests ---

func TestTranslateRequest(t *testing.T) {
	req := &llm.Request{
		Model: "claude-opus-4-6",
		Messages: []llm.Message{
			llm.SystemMessage("You are helpful."),
			llm.UserMessage("Hello"),
		},
		MaxTokens: intPtr(1000),
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// System should be extracted to top-level "system" field
	if _, ok := parsed["system"]; !ok {
		t.Error("expected system field in request body")
	}

	// Messages should only contain user message (no system)
	msgs, ok := parsed["messages"].([]any)
	if !ok {
		t.Fatal("expected messages array")
	}
	if len(msgs) != 1 {
		t.Errorf("expected 1 message (system extracted), got %d", len(msgs))
	}

	if parsed["model"] != "claude-opus-4-6" {
		t.Errorf("expected claude-opus-4-6, got %v", parsed["model"])
	}

	maxTokens, ok := parsed["max_tokens"].(float64)
	if !ok {
		t.Fatal("expected max_tokens field")
	}
	if int(maxTokens) != 1000 {
		t.Errorf("expected 1000, got %v", maxTokens)
	}
}

func TestTranslateRequestDefaultMaxTokens(t *testing.T) {
	req := &llm.Request{
		Model:    "claude-opus-4-6",
		Messages: []llm.Message{llm.UserMessage("Hi")},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	maxTokens, ok := parsed["max_tokens"].(float64)
	if !ok {
		t.Fatal("expected max_tokens")
	}
	if int(maxTokens) != 16384 {
		t.Errorf("expected default 16384, got %v", maxTokens)
	}
}

func TestTranslateRequestDeveloperMessageAsSystem(t *testing.T) {
	req := &llm.Request{
		Model: "claude-opus-4-6",
		Messages: []llm.Message{
			{Role: llm.RoleDeveloper, Content: []llm.ContentPart{{Kind: llm.KindText, Text: "Dev instructions"}}},
			llm.UserMessage("Hello"),
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Developer messages should be extracted to "system" like system messages
	if _, ok := parsed["system"]; !ok {
		t.Error("expected developer message extracted to system field")
	}
}

func TestTranslateRequestMessageAlternation(t *testing.T) {
	req := &llm.Request{
		Model: "claude-opus-4-6",
		Messages: []llm.Message{
			llm.UserMessage("Hello"),
			llm.UserMessage("How are you?"),
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Consecutive user messages should be merged
	msgs, ok := parsed["messages"].([]any)
	if !ok {
		t.Fatal("expected messages array")
	}
	if len(msgs) != 1 {
		t.Errorf("expected 1 merged message, got %d", len(msgs))
	}

	// Merged message should have both content parts
	msg := msgs[0].(map[string]any)
	content := msg["content"].([]any)
	if len(content) != 2 {
		t.Errorf("expected 2 content parts in merged message, got %d", len(content))
	}
}

func TestTranslateRequestToolChoice(t *testing.T) {
	tests := []struct {
		name       string
		toolChoice *llm.ToolChoice
		wantType   string
		wantName   string
		wantOmit   bool
	}{
		{
			name:       "auto",
			toolChoice: &llm.ToolChoice{Mode: "auto"},
			wantType:   "auto",
		},
		{
			name:       "none omitted",
			toolChoice: &llm.ToolChoice{Mode: "none"},
			wantOmit:   true,
		},
		{
			name:       "required maps to any",
			toolChoice: &llm.ToolChoice{Mode: "required"},
			wantType:   "any",
		},
		{
			name:       "named tool",
			toolChoice: &llm.ToolChoice{Mode: "named", ToolName: "get_weather"},
			wantType:   "tool",
			wantName:   "get_weather",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &llm.Request{
				Model:      "claude-opus-4-6",
				Messages:   []llm.Message{llm.UserMessage("Hi")},
				ToolChoice: tt.toolChoice,
				Tools: []llm.ToolDefinition{
					{Name: "get_weather", Description: "Get weather", Parameters: json.RawMessage(`{}`)},
				},
			}

			body, err := translateRequest(req)
			if err != nil {
				t.Fatalf("translateRequest error: %v", err)
			}

			var parsed map[string]any
			if err := json.Unmarshal(body, &parsed); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}

			tc, exists := parsed["tool_choice"]
			if tt.wantOmit {
				if exists {
					t.Error("expected tool_choice to be omitted for none")
				}
				return
			}

			if !exists {
				t.Fatal("expected tool_choice in body")
			}

			tcMap := tc.(map[string]any)
			if tcMap["type"] != tt.wantType {
				t.Errorf("expected type %q, got %v", tt.wantType, tcMap["type"])
			}
			if tt.wantName != "" {
				if tcMap["name"] != tt.wantName {
					t.Errorf("expected name %q, got %v", tt.wantName, tcMap["name"])
				}
			}
		})
	}
}

func TestTranslateRequestToolDefinitions(t *testing.T) {
	req := &llm.Request{
		Model:    "claude-opus-4-6",
		Messages: []llm.Message{llm.UserMessage("What's the weather?")},
		Tools: []llm.ToolDefinition{
			{
				Name:        "get_weather",
				Description: "Get weather for a city",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
			},
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	tools, ok := parsed["tools"].([]any)
	if !ok {
		t.Fatal("expected tools array")
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	tool := tools[0].(map[string]any)
	if tool["name"] != "get_weather" {
		t.Errorf("expected get_weather, got %v", tool["name"])
	}
	if tool["description"] != "Get weather for a city" {
		t.Errorf("expected description, got %v", tool["description"])
	}
	if _, ok := tool["input_schema"]; !ok {
		t.Error("expected input_schema field (Anthropic format)")
	}
}

func TestTranslateRequestToolResultMessage(t *testing.T) {
	req := &llm.Request{
		Model: "claude-opus-4-6",
		Messages: []llm.Message{
			llm.UserMessage("What's the weather?"),
			{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{
					{Kind: llm.KindToolCall, ToolCall: &llm.ToolCallData{ID: "call_1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"SF"}`)}},
				},
			},
			llm.ToolResultMessage("call_1", "72F and sunny", false),
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	msgs := parsed["messages"].([]any)
	// Should have: user, assistant, user (tool result mapped to user role)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	toolResultMsg := msgs[2].(map[string]any)
	if toolResultMsg["role"] != "user" {
		t.Errorf("tool result should have user role, got %v", toolResultMsg["role"])
	}
}

// --- translateResponse tests ---

func TestTranslateResponse(t *testing.T) {
	raw := []byte(`{
		"id": "msg_123",
		"model": "claude-opus-4-6",
		"type": "message",
		"role": "assistant",
		"content": [{"type": "text", "text": "Hello!"}],
		"stop_reason": "end_turn",
		"usage": {
			"input_tokens": 10,
			"output_tokens": 5
		}
	}`)

	resp, err := translateResponse(raw)
	if err != nil {
		t.Fatalf("translateResponse error: %v", err)
	}

	if resp.ID != "msg_123" {
		t.Errorf("expected msg_123, got %q", resp.ID)
	}
	if resp.Text() != "Hello!" {
		t.Errorf("expected Hello!, got %q", resp.Text())
	}
	if resp.FinishReason.Reason != "stop" {
		t.Errorf("expected stop, got %q", resp.FinishReason.Reason)
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("expected 10 input tokens, got %d", resp.Usage.InputTokens)
	}
}

func TestTranslateToolUseResponse(t *testing.T) {
	raw := []byte(`{
		"id": "msg_456",
		"model": "claude-opus-4-6",
		"type": "message",
		"role": "assistant",
		"content": [
			{"type": "text", "text": "Let me check."},
			{"type": "tool_use", "id": "call_789", "name": "get_weather", "input": {"city": "SF"}}
		],
		"stop_reason": "tool_use",
		"usage": {"input_tokens": 20, "output_tokens": 15}
	}`)

	resp, err := translateResponse(raw)
	if err != nil {
		t.Fatalf("translateResponse error: %v", err)
	}

	if resp.FinishReason.Reason != "tool_calls" {
		t.Errorf("expected tool_calls, got %q", resp.FinishReason.Reason)
	}

	toolCalls := resp.ToolCalls()
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].Name != "get_weather" {
		t.Errorf("expected get_weather, got %q", toolCalls[0].Name)
	}
	if toolCalls[0].ID != "call_789" {
		t.Errorf("expected call_789, got %q", toolCalls[0].ID)
	}
}

func TestTranslateResponseThinking(t *testing.T) {
	raw := []byte(`{
		"id": "msg_789",
		"model": "claude-opus-4-6",
		"type": "message",
		"role": "assistant",
		"content": [
			{"type": "thinking", "thinking": "Let me reason about this..."},
			{"type": "text", "text": "The answer is 42."}
		],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 10, "output_tokens": 20}
	}`)

	resp, err := translateResponse(raw)
	if err != nil {
		t.Fatalf("translateResponse error: %v", err)
	}

	if resp.Reasoning() == "" {
		t.Error("expected reasoning text")
	}
	if resp.Text() != "The answer is 42." {
		t.Errorf("expected text, got %q", resp.Text())
	}
}

func TestTranslateResponseRedactedThinking(t *testing.T) {
	raw := []byte(`{
		"id": "msg_790",
		"model": "claude-opus-4-6",
		"type": "message",
		"role": "assistant",
		"content": [
			{"type": "redacted_thinking", "data": "abc123"},
			{"type": "text", "text": "Done."}
		],
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 10, "output_tokens": 5}
	}`)

	resp, err := translateResponse(raw)
	if err != nil {
		t.Fatalf("translateResponse error: %v", err)
	}

	// Should have a redacted thinking part
	found := false
	for _, c := range resp.Message.Content {
		if c.Kind == llm.KindRedactedThinking && c.Thinking != nil && c.Thinking.Redacted {
			found = true
		}
	}
	if !found {
		t.Error("expected redacted thinking content part")
	}
}

func TestTranslateResponseCacheUsage(t *testing.T) {
	raw := []byte(`{
		"id": "msg_cache",
		"model": "claude-opus-4-6",
		"type": "message",
		"role": "assistant",
		"content": [{"type": "text", "text": "Hi"}],
		"stop_reason": "end_turn",
		"usage": {
			"input_tokens": 100,
			"output_tokens": 20,
			"cache_read_input_tokens": 80,
			"cache_creation_input_tokens": 10
		}
	}`)

	resp, err := translateResponse(raw)
	if err != nil {
		t.Fatalf("translateResponse error: %v", err)
	}

	if resp.Usage.CacheReadTokens == nil || *resp.Usage.CacheReadTokens != 80 {
		t.Errorf("expected 80 cache read tokens, got %v", resp.Usage.CacheReadTokens)
	}
	if resp.Usage.CacheWriteTokens == nil || *resp.Usage.CacheWriteTokens != 10 {
		t.Errorf("expected 10 cache write tokens, got %v", resp.Usage.CacheWriteTokens)
	}
}

// --- translateFinishReason tests ---

func TestTranslateFinishReason(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"end_turn", "stop"},
		{"stop_sequence", "stop"},
		{"max_tokens", "length"},
		{"tool_use", "tool_calls"},
		{"unknown_reason", "unknown_reason"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			fr := translateFinishReason(tt.input)
			if fr.Reason != tt.want {
				t.Errorf("translateFinishReason(%q) = %q, want %q", tt.input, fr.Reason, tt.want)
			}
			if fr.Raw != tt.input {
				t.Errorf("expected raw %q, got %q", tt.input, fr.Raw)
			}
		})
	}
}

// --- Adapter integration tests (with httptest) ---

func TestAdapterName(t *testing.T) {
	a := New("test-key")
	if a.Name() != "anthropic" {
		t.Errorf("expected anthropic, got %q", a.Name())
	}
}

func TestAdapterComplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("expected x-api-key header")
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("expected anthropic-version header")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json")
		}
		if r.URL.Path != "/v1/messages" {
			t.Errorf("expected /v1/messages, got %s", r.URL.Path)
		}

		// Verify request body
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		json.Unmarshal(body, &parsed)

		if parsed["model"] != "claude-opus-4-6" {
			t.Errorf("expected claude-opus-4-6 in body")
		}

		// Return response
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "msg_test",
			"model": "claude-opus-4-6",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "Hello from test!"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 5, "output_tokens": 3}
		}`)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	resp, err := a.Complete(context.Background(), &llm.Request{
		Model:    "claude-opus-4-6",
		Messages: []llm.Message{llm.UserMessage("Hi")},
	})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}

	if resp.ID != "msg_test" {
		t.Errorf("expected msg_test, got %q", resp.ID)
	}
	if resp.Text() != "Hello from test!" {
		t.Errorf("expected Hello from test!, got %q", resp.Text())
	}
	if resp.Provider != "anthropic" {
		t.Errorf("expected anthropic provider, got %q", resp.Provider)
	}
}

func TestAdapterCompleteErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error": {"message": "invalid api key"}}`)
	}))
	defer server.Close()

	a := New("bad-key", WithBaseURL(server.URL))
	_, err := a.Complete(context.Background(), &llm.Request{
		Model:    "claude-opus-4-6",
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

func TestAdapterCompleteBetaHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		betaHeader := r.Header.Get("anthropic-beta")
		// Should contain both the auto-injected prompt-caching beta and the user-specified one.
		if betaHeader != "prompt-caching-2024-07-31,max-tokens-3-5-sonnet-2024-07-15" {
			t.Errorf("expected combined beta headers, got %q", betaHeader)
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "msg_beta",
			"model": "claude-opus-4-6",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "ok"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 1, "output_tokens": 1}
		}`)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	_, err := a.Complete(context.Background(), &llm.Request{
		Model:    "claude-opus-4-6",
		Messages: []llm.Message{llm.UserMessage("Hi")},
		ProviderOptions: map[string]any{
			"anthropic": map[string]any{
				"beta_headers": "max-tokens-3-5-sonnet-2024-07-15",
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
}

func TestAdapterStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify stream: true in request
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		json.Unmarshal(body, &parsed)
		if parsed["stream"] != true {
			t.Errorf("expected stream: true in body")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}

		events := []string{
			`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg_stream","model":"claude-opus-4-6","role":"assistant","content":[],"usage":{"input_tokens":5,"output_tokens":0}}}`,
			`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`,
			`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
			`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
			`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
		}

		for _, evt := range events {
			fmt.Fprintf(w, "%s\n\n", evt)
			flusher.Flush()
		}
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	ch := a.Stream(context.Background(), &llm.Request{
		Model:    "claude-opus-4-6",
		Messages: []llm.Message{llm.UserMessage("Hi")},
	})

	var events []llm.StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	if len(events) == 0 {
		t.Fatal("expected stream events")
	}

	// Check we got text deltas
	var textContent strings.Builder
	for _, evt := range events {
		if evt.Type == llm.EventTextDelta {
			textContent.WriteString(evt.Delta)
		}
	}
	if textContent.String() != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", textContent.String())
	}

	// Check we got a finish event
	var gotFinish bool
	for _, evt := range events {
		if evt.Type == llm.EventFinish {
			gotFinish = true
			if evt.FinishReason == nil || evt.FinishReason.Reason != "stop" {
				t.Error("expected stop finish reason")
			}
		}
	}
	if !gotFinish {
		t.Error("expected finish event")
	}
}

func TestAdapterStreamToolUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		events := []string{
			`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg_tool","model":"claude-opus-4-6","role":"assistant","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}`,
			`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"get_weather"}}`,
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`,
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"SF\"}"}}`,
			`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
			`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":10}}`,
			`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
		}

		for _, evt := range events {
			fmt.Fprintf(w, "%s\n\n", evt)
			flusher.Flush()
		}
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	ch := a.Stream(context.Background(), &llm.Request{
		Model:    "claude-opus-4-6",
		Messages: []llm.Message{llm.UserMessage("Weather?")},
	})

	var events []llm.StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	// Check we got tool call events
	var gotToolStart, gotToolEnd bool
	for _, evt := range events {
		if evt.Type == llm.EventToolCallStart {
			gotToolStart = true
			if evt.ToolCall == nil || evt.ToolCall.Name != "get_weather" {
				t.Error("expected get_weather tool call start")
			}
		}
		if evt.Type == llm.EventToolCallEnd {
			gotToolEnd = true
		}
	}
	if !gotToolStart {
		t.Error("expected tool call start event")
	}
	if !gotToolEnd {
		t.Error("expected tool call end event")
	}
}

func TestAdapterClose(t *testing.T) {
	a := New("test-key")
	if err := a.Close(); err != nil {
		t.Errorf("expected no error from Close, got %v", err)
	}
}

// --- Issue 1: Cache control injection tests ---

func TestCacheControlInjection(t *testing.T) {
	req := &llm.Request{
		Model: "claude-opus-4-6",
		Messages: []llm.Message{
			llm.SystemMessage("You are helpful."),
			llm.UserMessage("Hello"),
		},
		Tools: []llm.ToolDefinition{
			{Name: "get_weather", Description: "Get weather", Parameters: json.RawMessage(`{}`)},
			{Name: "get_time", Description: "Get time", Parameters: json.RawMessage(`{}`)},
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Last system block should have cache_control
	system := parsed["system"].([]any)
	lastSystem := system[len(system)-1].(map[string]any)
	cc, ok := lastSystem["cache_control"].(map[string]any)
	if !ok {
		t.Fatal("expected cache_control on last system block")
	}
	if cc["type"] != "ephemeral" {
		t.Errorf("expected ephemeral, got %v", cc["type"])
	}

	// Last tool should have cache_control
	tools := parsed["tools"].([]any)
	lastTool := tools[len(tools)-1].(map[string]any)
	toolCC, ok := lastTool["cache_control"].(map[string]any)
	if !ok {
		t.Fatal("expected cache_control on last tool")
	}
	if toolCC["type"] != "ephemeral" {
		t.Errorf("expected ephemeral, got %v", toolCC["type"])
	}

	// First tool should NOT have cache_control
	firstTool := tools[0].(map[string]any)
	if _, ok := firstTool["cache_control"]; ok {
		t.Error("first tool should not have cache_control")
	}

	// Last user message last content block should have cache_control
	msgs := parsed["messages"].([]any)
	lastMsg := msgs[len(msgs)-1].(map[string]any)
	content := lastMsg["content"].([]any)
	lastContent := content[len(content)-1].(map[string]any)
	contentCC, ok := lastContent["cache_control"].(map[string]any)
	if !ok {
		t.Fatal("expected cache_control on last user message content block")
	}
	if contentCC["type"] != "ephemeral" {
		t.Errorf("expected ephemeral, got %v", contentCC["type"])
	}
}

func TestCacheControlOptOut(t *testing.T) {
	req := &llm.Request{
		Model: "claude-opus-4-6",
		Messages: []llm.Message{
			llm.SystemMessage("You are helpful."),
			llm.UserMessage("Hello"),
		},
		ProviderOptions: map[string]any{
			"anthropic": map[string]any{
				"auto_cache": false,
			},
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// System block should NOT have cache_control when opted out
	system := parsed["system"].([]any)
	lastSystem := system[len(system)-1].(map[string]any)
	if _, ok := lastSystem["cache_control"]; ok {
		t.Error("cache_control should not be present when auto_cache is false")
	}
}

func TestCacheControlBetaHeaderAutoInjected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		betaHeader := r.Header.Get("anthropic-beta")
		if !strings.Contains(betaHeader, "prompt-caching-2024-07-31") {
			t.Errorf("expected prompt-caching beta header, got %q", betaHeader)
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "msg_cache", "model": "claude-opus-4-6", "type": "message",
			"role": "assistant", "content": [{"type": "text", "text": "ok"}],
			"stop_reason": "end_turn", "usage": {"input_tokens": 1, "output_tokens": 1}
		}`)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	_, err := a.Complete(context.Background(), &llm.Request{
		Model:    "claude-opus-4-6",
		Messages: []llm.Message{llm.UserMessage("Hi")},
	})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
}

func TestCacheControlBetaHeaderNotInjectedWhenOptedOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		betaHeader := r.Header.Get("anthropic-beta")
		if betaHeader != "" {
			t.Errorf("expected no beta header when auto_cache is false, got %q", betaHeader)
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "msg_no_cache", "model": "claude-opus-4-6", "type": "message",
			"role": "assistant", "content": [{"type": "text", "text": "ok"}],
			"stop_reason": "end_turn", "usage": {"input_tokens": 1, "output_tokens": 1}
		}`)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	_, err := a.Complete(context.Background(), &llm.Request{
		Model:    "claude-opus-4-6",
		Messages: []llm.Message{llm.UserMessage("Hi")},
		ProviderOptions: map[string]any{
			"anthropic": map[string]any{
				"auto_cache": false,
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
}

// --- Issue 2: Tool choice "none" omits tools array ---

func TestToolChoiceNoneOmitsTools(t *testing.T) {
	req := &llm.Request{
		Model:      "claude-opus-4-6",
		Messages:   []llm.Message{llm.UserMessage("Hi")},
		ToolChoice: &llm.ToolChoice{Mode: "none"},
		Tools: []llm.ToolDefinition{
			{Name: "get_weather", Description: "Get weather", Parameters: json.RawMessage(`{}`)},
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Tools should NOT be present when tool choice is "none"
	if _, ok := parsed["tools"]; ok {
		t.Error("tools array should be omitted when tool_choice mode is 'none'")
	}

	// tool_choice should also be omitted (translateToolChoice returns nil for "none")
	if _, ok := parsed["tool_choice"]; ok {
		t.Error("tool_choice should be omitted for 'none'")
	}
}

// --- Issue 3: Beta headers accept array format ---

func TestBetaHeadersArrayFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		betaHeader := r.Header.Get("anthropic-beta")
		// Should contain prompt-caching (auto) + both user-specified headers
		if !strings.Contains(betaHeader, "prompt-caching-2024-07-31") {
			t.Errorf("missing prompt-caching in beta header: %q", betaHeader)
		}
		if !strings.Contains(betaHeader, "beta-one") {
			t.Errorf("missing beta-one in beta header: %q", betaHeader)
		}
		if !strings.Contains(betaHeader, "beta-two") {
			t.Errorf("missing beta-two in beta header: %q", betaHeader)
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id": "msg_arr", "model": "claude-opus-4-6", "type": "message",
			"role": "assistant", "content": [{"type": "text", "text": "ok"}],
			"stop_reason": "end_turn", "usage": {"input_tokens": 1, "output_tokens": 1}
		}`)
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	_, err := a.Complete(context.Background(), &llm.Request{
		Model:    "claude-opus-4-6",
		Messages: []llm.Message{llm.UserMessage("Hi")},
		ProviderOptions: map[string]any{
			"anthropic": map[string]any{
				// JSON arrays unmarshal to []any in Go
				"beta_headers": []any{"beta-one", "beta-two"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
}

func TestCollectBetaHeadersStringFormat(t *testing.T) {
	req := &llm.Request{
		Model:    "claude-opus-4-6",
		Messages: []llm.Message{llm.UserMessage("Hi")},
		ProviderOptions: map[string]any{
			"anthropic": map[string]any{
				"beta_headers": "custom-beta-header",
			},
		},
	}

	result := collectBetaHeaders(req)
	if !strings.Contains(result, "prompt-caching-2024-07-31") {
		t.Errorf("expected prompt-caching in result: %q", result)
	}
	if !strings.Contains(result, "custom-beta-header") {
		t.Errorf("expected custom-beta-header in result: %q", result)
	}
}

func TestCollectBetaHeadersArrayFormat(t *testing.T) {
	req := &llm.Request{
		Model:    "claude-opus-4-6",
		Messages: []llm.Message{llm.UserMessage("Hi")},
		ProviderOptions: map[string]any{
			"anthropic": map[string]any{
				"beta_headers": []any{"header-a", "header-b"},
			},
		},
	}

	result := collectBetaHeaders(req)
	if result != "prompt-caching-2024-07-31,header-a,header-b" {
		t.Errorf("unexpected beta headers: %q", result)
	}
}

// --- Issue 4: Image URL source format ---

func TestImageURLSourceFormat(t *testing.T) {
	req := &llm.Request{
		Model: "claude-opus-4-6",
		Messages: []llm.Message{
			{
				Role: llm.RoleUser,
				Content: []llm.ContentPart{
					{
						Kind: llm.KindImage,
						Image: &llm.ImageData{
							URL:       "https://example.com/image.png",
							MediaType: "image/png",
						},
					},
				},
			},
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	msgs := parsed["messages"].([]any)
	msg := msgs[0].(map[string]any)
	content := msg["content"].([]any)
	imageBlock := content[0].(map[string]any)

	source := imageBlock["source"].(map[string]any)
	if source["type"] != "url" {
		t.Errorf("expected type 'url', got %v", source["type"])
	}
	if source["url"] != "https://example.com/image.png" {
		t.Errorf("expected url field, got %v", source["url"])
	}
	// "data" field should NOT be present for URL images
	if _, ok := source["data"]; ok {
		t.Error("data field should not be present for URL-based images")
	}
}

func TestImageBase64SourceFormat(t *testing.T) {
	req := &llm.Request{
		Model: "claude-opus-4-6",
		Messages: []llm.Message{
			{
				Role: llm.RoleUser,
				Content: []llm.ContentPart{
					{
						Kind: llm.KindImage,
						Image: &llm.ImageData{
							Data:      []byte("fakeimagebytes"),
							MediaType: "image/png",
						},
					},
				},
			},
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	msgs := parsed["messages"].([]any)
	msg := msgs[0].(map[string]any)
	content := msg["content"].([]any)
	imageBlock := content[0].(map[string]any)

	source := imageBlock["source"].(map[string]any)
	if source["type"] != "base64" {
		t.Errorf("expected type 'base64', got %v", source["type"])
	}
	if _, ok := source["data"]; !ok {
		t.Error("expected data field for base64 images")
	}
	// "url" field should NOT be present for base64 images
	if _, ok := source["url"]; ok {
		t.Error("url field should not be present for base64 images")
	}
}

// --- Thinking signature streaming ---

func TestAdapterStreamThinkingSignature(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		events := []string{
			`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg_think","model":"claude-opus-4-6","role":"assistant","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}`,
			// Thinking block
			`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me analyze this."}}`,
			// Signature delta — must be captured for round-tripping
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"EqQBCgIYAhIM1gbcDa9GJwZA2b3h"}}`,
			`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
			// Text block
			`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"The answer is 42."}}`,
			`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":1}`,
			`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":20}}`,
			`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
		}

		for _, evt := range events {
			fmt.Fprintf(w, "%s\n\n", evt)
			flusher.Flush()
		}
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	ch := a.Stream(context.Background(), &llm.Request{
		Model:    "claude-opus-4-6",
		Messages: []llm.Message{llm.UserMessage("Think about this.")},
	})

	var events []llm.StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	// Verify signature_delta was captured
	var gotSignature bool
	for _, evt := range events {
		if evt.Type == llm.EventReasoningSignature {
			gotSignature = true
			if evt.ReasoningSignature != "EqQBCgIYAhIM1gbcDa9GJwZA2b3h" {
				t.Errorf("expected signature, got %q", evt.ReasoningSignature)
			}
		}
	}
	if !gotSignature {
		t.Error("expected reasoning_signature event from signature_delta")
	}

	// Accumulate and verify round-trip
	acc := llm.NewStreamAccumulator()
	for _, evt := range events {
		acc.Process(evt)
	}
	resp := acc.Response()

	// Thinking part should have signature
	var foundThinking bool
	for _, part := range resp.Message.Content {
		if part.Kind == llm.KindThinking {
			foundThinking = true
			if part.Thinking == nil {
				t.Fatal("expected non-nil ThinkingData")
			}
			if part.Thinking.Text != "Let me analyze this." {
				t.Errorf("thinking text = %q", part.Thinking.Text)
			}
			if part.Thinking.Signature != "EqQBCgIYAhIM1gbcDa9GJwZA2b3h" {
				t.Errorf("thinking signature = %q, want EqQBCgIYAhIM1gbcDa9GJwZA2b3h", part.Thinking.Signature)
			}
		}
	}
	if !foundThinking {
		t.Error("expected thinking content part with signature")
	}
}

func TestAdapterStreamRedactedThinking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		events := []string{
			`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg_redact","model":"claude-opus-4-6","role":"assistant","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}`,
			// Thinking block
			`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Reasoning..."}}`,
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_thinking"}}`,
			`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":0}`,
			// Redacted thinking block — opaque data blob
			`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"opaque_redacted_blob_abc123"}}`,
			`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":1}`,
			// Text block
			`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`,
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"Done."}}`,
			`event: content_block_stop` + "\n" + `data: {"type":"content_block_stop","index":2}`,
			`event: message_delta` + "\n" + `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":15}}`,
			`event: message_stop` + "\n" + `data: {"type":"message_stop"}`,
		}

		for _, evt := range events {
			fmt.Fprintf(w, "%s\n\n", evt)
			flusher.Flush()
		}
	}))
	defer server.Close()

	a := New("test-key", WithBaseURL(server.URL))
	ch := a.Stream(context.Background(), &llm.Request{
		Model:    "claude-opus-4-6",
		Messages: []llm.Message{llm.UserMessage("Think hard.")},
	})

	var events []llm.StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	// Verify redacted_thinking event was emitted
	var gotRedacted bool
	for _, evt := range events {
		if evt.Type == llm.EventRedactedThinking {
			gotRedacted = true
			if evt.ReasoningSignature != "opaque_redacted_blob_abc123" {
				t.Errorf("expected redacted data, got %q", evt.ReasoningSignature)
			}
		}
	}
	if !gotRedacted {
		t.Error("expected redacted_thinking event")
	}

	// Accumulate and verify all parts preserved
	acc := llm.NewStreamAccumulator()
	for _, evt := range events {
		acc.Process(evt)
	}
	resp := acc.Response()

	// Should have: thinking, redacted_thinking, text (in that order)
	if len(resp.Message.Content) != 3 {
		t.Fatalf("expected 3 content parts, got %d", len(resp.Message.Content))
	}

	// Part 0: thinking with signature
	if resp.Message.Content[0].Kind != llm.KindThinking {
		t.Errorf("part[0] kind = %s, want thinking", resp.Message.Content[0].Kind)
	}
	if resp.Message.Content[0].Thinking.Signature != "sig_thinking" {
		t.Errorf("part[0] signature = %q", resp.Message.Content[0].Thinking.Signature)
	}

	// Part 1: redacted_thinking
	if resp.Message.Content[1].Kind != llm.KindRedactedThinking {
		t.Errorf("part[1] kind = %s, want redacted_thinking", resp.Message.Content[1].Kind)
	}
	if resp.Message.Content[1].Thinking.Signature != "opaque_redacted_blob_abc123" {
		t.Errorf("part[1] data = %q", resp.Message.Content[1].Thinking.Signature)
	}

	// Part 2: text
	if resp.Message.Content[2].Kind != llm.KindText {
		t.Errorf("part[2] kind = %s, want text", resp.Message.Content[2].Kind)
	}
	if resp.Message.Content[2].Text != "Done." {
		t.Errorf("part[2] text = %q", resp.Message.Content[2].Text)
	}
}

func intPtr(v int) *int { return &v }
