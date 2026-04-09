# OpenAI-Compatible Chat Completions Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an `openai-compat` provider that speaks the OpenAI Chat Completions API, enabling tracker to work with LM Studio, Ollama, vLLM, OpenRouter, and other OpenAI-compatible servers.

**Architecture:** New standalone package `llm/openaicompat/` following the same adapter pattern as `llm/openai/`, `llm/anthropic/`, and `llm/google/`. The Chat Completions wire format is structurally different from the Responses API (messages vs input array, nested tool_calls vs flat function_call items, delta-based SSE vs typed events), so this is a clean separate package — not a mode flag on the existing adapter.

**Tech Stack:** Go, net/http, encoding/json, bufio (SSE parsing). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-04-06-openai-compat-provider-design.md`

---

## File Structure

| File | Responsibility |
|------|---------------|
| `llm/openaicompat/adapter.go` | Adapter struct, New(), Name(), Complete(), Stream(), SSE parsing, setHeaders() |
| `llm/openaicompat/translate.go` | Wire format types, request/response translation, message conversion |
| `llm/openaicompat/translate_test.go` | Unit tests for request serialization, response parsing, message translation |
| `llm/openaicompat/adapter_test.go` | End-to-end SSE streaming tests with mock HTTP server |
| `llm/client.go` | Add `"openai-compat"` to `providerEnvKeys` and `providerPriority` |
| `tracker.go` | Add openai-compat constructor in `buildClient()` |
| `cmd/tracker/run.go` | Add openai-compat constructor in `buildLLMClient()` |

---

## Task 1: Wire format types and request translation

**Files:**
- Create: `llm/openaicompat/translate.go`
- Test: `llm/openaicompat/translate_test.go`

- [ ] **Step 1: Write failing test for basic request translation**

Create `llm/openaicompat/translate_test.go`:

```go
// ABOUTME: Tests for OpenAI Chat Completions request/response translation.
// ABOUTME: Validates message conversion, tool formatting, and response parsing.
package openaicompat

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/llm"
)

func TestTranslateRequest_BasicUserMessage(t *testing.T) {
	req := &llm.Request{
		Model: "qwen/qwen3-coder-next",
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: []llm.ContentPart{{Kind: llm.KindText, Text: "You are helpful."}}},
			{Role: llm.RoleUser, Content: []llm.ContentPart{{Kind: llm.KindText, Text: "Hello"}}},
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest failed: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if raw["model"] != "qwen/qwen3-coder-next" {
		t.Errorf("model = %v, want qwen/qwen3-coder-next", raw["model"])
	}

	msgs, ok := raw["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("messages count = %d, want 2", len(msgs))
	}

	sys := msgs[0].(map[string]any)
	if sys["role"] != "system" || sys["content"] != "You are helpful." {
		t.Errorf("system message = %v", sys)
	}

	usr := msgs[1].(map[string]any)
	if usr["role"] != "user" || usr["content"] != "Hello" {
		t.Errorf("user message = %v", usr)
	}

	// max_tokens defaults to 16384
	if raw["max_tokens"] != float64(16384) {
		t.Errorf("max_tokens = %v, want 16384", raw["max_tokens"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./llm/openaicompat/ -run TestTranslateRequest_BasicUserMessage -v`
Expected: compilation error — package doesn't exist yet.

- [ ] **Step 3: Write translate.go with wire types and request translation**

Create `llm/openaicompat/translate.go`:

```go
// ABOUTME: Request/response translation between unified llm types and OpenAI Chat Completions API format.
// ABOUTME: Handles messages array construction, tool call nesting, and response parsing.
package openaicompat

import (
	"encoding/json"
	"strings"

	"github.com/2389-research/tracker/llm"
)

const defaultMaxTokens = 16384

// --- Wire format types for the Chat Completions API ---

type chatRequest struct {
	Model          string              `json:"model"`
	Messages       []chatMessage       `json:"messages"`
	Tools          []chatTool          `json:"tools,omitempty"`
	ToolChoice     any                 `json:"tool_choice,omitempty"`
	MaxTokens      *int                `json:"max_tokens,omitempty"`
	Temperature    *float64            `json:"temperature,omitempty"`
	TopP           *float64            `json:"top_p,omitempty"`
	Stop           []string            `json:"stop,omitempty"`
	ResponseFormat *chatResponseFormat `json:"response_format,omitempty"`
	Stream         bool                `json:"stream,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatFunctionCall `json:"function"`
}

type chatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type chatResponseFormat struct {
	Type string `json:"type"`
}

// --- Request translation ---

func translateRequest(req *llm.Request) ([]byte, error) {
	cr := chatRequest{
		Model:       req.Model,
		Messages:    translateMessages(req.Messages),
		Tools:       translateToolDefs(req.Tools),
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.StopSequences,
	}

	if req.MaxTokens != nil {
		cr.MaxTokens = req.MaxTokens
	} else {
		v := defaultMaxTokens
		cr.MaxTokens = &v
	}

	if req.ToolChoice != nil {
		cr.ToolChoice = translateToolChoice(req.ToolChoice)
	}

	cr.ResponseFormat = translateResponseFormat(req.ResponseFormat)

	return json.Marshal(cr)
}

func translateMessages(messages []llm.Message) []chatMessage {
	var out []chatMessage
	for _, m := range messages {
		switch m.Role {
		case llm.RoleSystem, llm.RoleDeveloper:
			out = append(out, translateSystemMessage(m))
		case llm.RoleUser:
			out = append(out, translateUserMessage(m))
		case llm.RoleAssistant:
			out = append(out, translateAssistantMessage(m))
		case llm.RoleTool:
			out = append(out, translateToolResultMessages(m)...)
		}
	}
	return out
}

func translateSystemMessage(m llm.Message) chatMessage {
	var parts []string
	for _, p := range m.Content {
		if p.Kind == llm.KindText {
			parts = append(parts, p.Text)
		}
	}
	return chatMessage{Role: "system", Content: strings.Join(parts, "\n")}
}

func translateUserMessage(m llm.Message) chatMessage {
	var parts []string
	for _, p := range m.Content {
		if p.Kind == llm.KindText {
			parts = append(parts, p.Text)
		}
	}
	return chatMessage{Role: "user", Content: strings.Join(parts, "")}
}

func translateAssistantMessage(m llm.Message) chatMessage {
	msg := chatMessage{Role: "assistant"}
	var textParts []string
	for _, p := range m.Content {
		switch p.Kind {
		case llm.KindText:
			textParts = append(textParts, p.Text)
		case llm.KindToolCall:
			if p.ToolCall != nil {
				msg.ToolCalls = append(msg.ToolCalls, chatToolCall{
					ID:   p.ToolCall.ID,
					Type: "function",
					Function: chatFunctionCall{
						Name:      p.ToolCall.Name,
						Arguments: string(p.ToolCall.Arguments),
					},
				})
			}
		}
	}
	msg.Content = strings.Join(textParts, "")
	return msg
}

func translateToolResultMessages(m llm.Message) []chatMessage {
	var out []chatMessage
	for _, p := range m.Content {
		if p.Kind == llm.KindToolResult && p.ToolResult != nil {
			callID := p.ToolResult.ToolCallID
			if callID == "" {
				callID = "call_unknown"
			}
			out = append(out, chatMessage{
				Role:       "tool",
				ToolCallID: callID,
				Content:    p.ToolResult.Content,
			})
		}
	}
	return out
}

func translateToolDefs(tools []llm.ToolDefinition) []chatTool {
	var out []chatTool
	for _, t := range tools {
		out = append(out, chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return out
}

func translateToolChoice(tc *llm.ToolChoice) any {
	switch tc.Mode {
	case "auto":
		return "auto"
	case "none":
		return "none"
	case "required":
		return "required"
	case "named":
		return map[string]any{"type": "function", "function": map[string]string{"name": tc.ToolName}}
	default:
		return tc.Mode
	}
}

func translateResponseFormat(rf *llm.ResponseFormat) *chatResponseFormat {
	if rf == nil {
		return nil
	}
	if rf.Type == "json_object" {
		return &chatResponseFormat{Type: "json_object"}
	}
	// json_schema not supported by most compat servers; silently drop.
	return nil
}

// --- Response translation ---

type chatResponse struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Choices []chatChoice   `json:"choices"`
	Usage   chatUsage      `json:"usage"`
}

type chatChoice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func translateResponse(raw []byte) (*llm.Response, error) {
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return nil, err
	}

	if len(cr.Choices) == 0 {
		return &llm.Response{
			ID:    cr.ID,
			Model: cr.Model,
			Message: llm.Message{
				Role:    llm.RoleAssistant,
				Content: nil,
			},
			FinishReason: llm.FinishReason{Reason: "stop", Raw: "no_choices"},
			Usage:        translateUsage(cr.Usage),
			Raw:          raw,
		}, nil
	}

	choice := cr.Choices[0]
	content := translateChoiceMessage(choice.Message)
	hasTCs := len(choice.Message.ToolCalls) > 0
	fr := translateFinishReason(choice.FinishReason, hasTCs)

	return &llm.Response{
		ID:    cr.ID,
		Model: cr.Model,
		Message: llm.Message{
			Role:    llm.RoleAssistant,
			Content: content,
		},
		FinishReason: fr,
		Usage:        translateUsage(cr.Usage),
		Raw:          raw,
	}, nil
}

func translateChoiceMessage(msg chatMessage) []llm.ContentPart {
	var parts []llm.ContentPart
	if msg.Content != "" {
		parts = append(parts, llm.ContentPart{Kind: llm.KindText, Text: msg.Content})
	}
	for _, tc := range msg.ToolCalls {
		parts = append(parts, llm.ContentPart{
			Kind: llm.KindToolCall,
			ToolCall: &llm.ToolCallData{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: json.RawMessage(tc.Function.Arguments),
			},
		})
	}
	return parts
}

func translateUsage(u chatUsage) llm.Usage {
	return llm.Usage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
	}
}

func translateFinishReason(reason string, hasToolCalls bool) llm.FinishReason {
	if hasToolCalls {
		return llm.FinishReason{Reason: "tool_calls", Raw: reason}
	}
	switch reason {
	case "stop":
		return llm.FinishReason{Reason: "stop", Raw: reason}
	case "length":
		return llm.FinishReason{Reason: "length", Raw: reason}
	case "tool_calls":
		return llm.FinishReason{Reason: "tool_calls", Raw: reason}
	case "content_filter":
		return llm.FinishReason{Reason: "content_filter", Raw: reason}
	default:
		return llm.FinishReason{Reason: reason, Raw: reason}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./llm/openaicompat/ -run TestTranslateRequest_BasicUserMessage -v`
Expected: PASS

- [ ] **Step 5: Write remaining translation tests**

Add to `llm/openaicompat/translate_test.go`:

```go
func TestTranslateRequest_AssistantWithToolCalls(t *testing.T) {
	req := &llm.Request{
		Model: "test-model",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentPart{{Kind: llm.KindText, Text: "read main.go"}}},
			{Role: llm.RoleAssistant, Content: []llm.ContentPart{
				{Kind: llm.KindText, Text: "I'll read that file."},
				{Kind: llm.KindToolCall, ToolCall: &llm.ToolCallData{
					ID:        "call_abc",
					Name:      "read_file",
					Arguments: json.RawMessage(`{"path":"main.go"}`),
				}},
			}},
			{Role: llm.RoleTool, Content: []llm.ContentPart{
				{Kind: llm.KindToolResult, ToolResult: &llm.ToolResultData{
					ToolCallID: "call_abc",
					Content:    "package main\n",
				}},
			}},
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest failed: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	msgs := raw["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages count = %d, want 3", len(msgs))
	}

	// Assistant message should have nested tool_calls
	asst := msgs[1].(map[string]any)
	if asst["role"] != "assistant" {
		t.Errorf("msg[1] role = %v, want assistant", asst["role"])
	}
	tcs, ok := asst["tool_calls"].([]any)
	if !ok || len(tcs) != 1 {
		t.Fatalf("tool_calls count = %d, want 1", len(tcs))
	}
	tc := tcs[0].(map[string]any)
	fn := tc["function"].(map[string]any)
	if fn["name"] != "read_file" {
		t.Errorf("tool call name = %v, want read_file", fn["name"])
	}

	// Tool result message
	tool := msgs[2].(map[string]any)
	if tool["role"] != "tool" {
		t.Errorf("msg[2] role = %v, want tool", tool["role"])
	}
	if tool["tool_call_id"] != "call_abc" {
		t.Errorf("tool_call_id = %v, want call_abc", tool["tool_call_id"])
	}
}

func TestTranslateRequest_ToolDefsWrapping(t *testing.T) {
	req := &llm.Request{
		Model: "test-model",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentPart{{Kind: llm.KindText, Text: "hi"}}},
		},
		Tools: []llm.ToolDefinition{
			{Name: "read_file", Description: "Read a file", Parameters: json.RawMessage(`{"type":"object"}`)},
		},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest failed: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	tools := raw["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools count = %d, want 1", len(tools))
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tool type = %v, want function", tool["type"])
	}
	fn := tool["function"].(map[string]any)
	if fn["name"] != "read_file" {
		t.Errorf("function name = %v, want read_file", fn["name"])
	}
}

func TestTranslateRequest_ResponseFormatJSON(t *testing.T) {
	req := &llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentPart{{Kind: llm.KindText, Text: "hi"}}}},
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest failed: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	rf := raw["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Errorf("response_format type = %v, want json_object", rf["type"])
	}
}

func TestTranslateRequest_JSONSchemaDropped(t *testing.T) {
	req := &llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentPart{{Kind: llm.KindText, Text: "hi"}}}},
		ResponseFormat: &llm.ResponseFormat{Type: "json_schema", JSONSchema: json.RawMessage(`{}`)},
	}

	body, err := translateRequest(req)
	if err != nil {
		t.Fatalf("translateRequest failed: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if raw["response_format"] != nil {
		t.Errorf("response_format should be nil for json_schema, got %v", raw["response_format"])
	}
}

func TestTranslateResponse_TextOnly(t *testing.T) {
	raw := []byte(`{
		"id": "chatcmpl-123",
		"model": "qwen/qwen3-coder-next",
		"choices": [{"index": 0, "message": {"role": "assistant", "content": "Hello!"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`)

	resp, err := translateResponse(raw)
	if err != nil {
		t.Fatalf("translateResponse failed: %v", err)
	}

	if resp.ID != "chatcmpl-123" {
		t.Errorf("ID = %q, want chatcmpl-123", resp.ID)
	}
	if len(resp.Message.Content) != 1 || resp.Message.Content[0].Text != "Hello!" {
		t.Errorf("content = %v, want [Hello!]", resp.Message.Content)
	}
	if resp.FinishReason.Reason != "stop" {
		t.Errorf("finish_reason = %q, want stop", resp.FinishReason.Reason)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

func TestTranslateResponse_WithToolCalls(t *testing.T) {
	raw := []byte(`{
		"id": "chatcmpl-456",
		"model": "test",
		"choices": [{"index": 0, "message": {
			"role": "assistant",
			"content": "",
			"tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "read_file", "arguments": "{\"path\":\"x\"}"}}]
		}, "finish_reason": "tool_calls"}],
		"usage": {"prompt_tokens": 5, "completion_tokens": 10, "total_tokens": 15}
	}`)

	resp, err := translateResponse(raw)
	if err != nil {
		t.Fatalf("translateResponse failed: %v", err)
	}

	if resp.FinishReason.Reason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", resp.FinishReason.Reason)
	}

	// Should have tool call content part (content "" is omitted)
	var tcCount int
	for _, p := range resp.Message.Content {
		if p.Kind == llm.KindToolCall {
			tcCount++
			if p.ToolCall.Name != "read_file" {
				t.Errorf("tool call name = %q, want read_file", p.ToolCall.Name)
			}
		}
	}
	if tcCount != 1 {
		t.Errorf("tool call count = %d, want 1", tcCount)
	}
}

func TestTranslateResponse_EmptyChoices(t *testing.T) {
	raw := []byte(`{"id": "x", "model": "m", "choices": [], "usage": {}}`)
	resp, err := translateResponse(raw)
	if err != nil {
		t.Fatalf("translateResponse failed: %v", err)
	}
	if resp.FinishReason.Reason != "stop" {
		t.Errorf("finish_reason = %q, want stop", resp.FinishReason.Reason)
	}
}
```

- [ ] **Step 6: Run all translation tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./llm/openaicompat/ -run TestTranslate -v`
Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add llm/openaicompat/translate.go llm/openaicompat/translate_test.go
git commit -m "feat: add openai-compat Chat Completions wire format translation"
```

---

## Task 2: Adapter with Complete() and Stream()

**Files:**
- Create: `llm/openaicompat/adapter.go`
- Test: `llm/openaicompat/adapter_test.go`

- [ ] **Step 1: Write failing test for Complete()**

Create `llm/openaicompat/adapter_test.go`:

```go
// ABOUTME: End-to-end tests for the openai-compat adapter using mock HTTP servers.
// ABOUTME: Tests synchronous Complete() and SSE-based Stream() against real HTTP responses.
package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
)

func TestComplete_TextResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}

		// Verify request body is Chat Completions format
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["messages"]; !ok {
			t.Error("request body missing 'messages' field")
		}
		if _, ok := body["input"]; ok {
			t.Error("request body has 'input' field (Responses API format, not Chat Completions)")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-test",
			"model": "test-model",
			"choices": []map[string]any{
				{"index": 0, "message": map[string]any{"role": "assistant", "content": "Hello!"}, "finish_reason": "stop"},
			},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}))
	defer srv.Close()

	adapter := New("test-key", WithBaseURL(srv.URL))
	resp, err := adapter.Complete(context.Background(), &llm.Request{
		Model: "test-model",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentPart{{Kind: llm.KindText, Text: "Hello"}}},
		},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	if resp.ID != "chatcmpl-test" {
		t.Errorf("ID = %q, want chatcmpl-test", resp.ID)
	}
	if resp.Provider != "openai-compat" {
		t.Errorf("Provider = %q, want openai-compat", resp.Provider)
	}
	if len(resp.Message.Content) != 1 || resp.Message.Content[0].Text != "Hello!" {
		t.Errorf("content = %v", resp.Message.Content)
	}
}

func TestComplete_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"invalid api key","type":"invalid_request_error","code":"invalid_api_key"}}`))
	}))
	defer srv.Close()

	adapter := New("bad-key", WithBaseURL(srv.URL))
	_, err := adapter.Complete(context.Background(), &llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentPart{{Kind: llm.KindText, Text: "hi"}}}},
	})
	if err == nil {
		t.Fatal("expected error for 401")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./llm/openaicompat/ -run TestComplete -v`
Expected: compilation error — Adapter not defined yet.

- [ ] **Step 3: Write adapter.go**

Create `llm/openaicompat/adapter.go`:

```go
// ABOUTME: OpenAI Chat Completions API adapter implementing the ProviderAdapter interface.
// ABOUTME: Works with any OpenAI-compatible server (LM Studio, Ollama, vLLM, OpenRouter, etc).
package openaicompat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/2389-research/tracker/llm"
)

const (
	defaultBaseURL    = "https://openrouter.ai/api"
	chatCompletePath  = "/v1/chat/completions"
)

// Adapter implements llm.ProviderAdapter for the OpenAI Chat Completions API.
type Adapter struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures an Adapter.
type Option func(*Adapter)

// WithBaseURL overrides the default API base URL.
func WithBaseURL(url string) Option {
	return func(a *Adapter) {
		a.baseURL = url
	}
}

// WithHTTPClient provides a custom http.Client.
func WithHTTPClient(client *http.Client) Option {
	return func(a *Adapter) {
		a.httpClient = client
	}
}

// New creates a new openai-compat adapter.
func New(apiKey string, opts ...Option) *Adapter {
	a := &Adapter{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
	for _, opt := range opts {
		opt(a)
	}
	a.apiKey = strings.Trim(a.apiKey, "\"'")
	a.baseURL = strings.Trim(a.baseURL, "\"'")
	a.baseURL = strings.TrimSuffix(a.baseURL, "/v1")
	return a
}

// Name returns the provider identifier.
func (a *Adapter) Name() string {
	return "openai-compat"
}

// Complete sends a synchronous request.
func (a *Adapter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	body, err := translateRequest(req)
	if err != nil {
		return nil, fmt.Errorf("openai-compat: translate request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+chatCompletePath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai-compat: create request: %w", err)
	}
	a.setHeaders(httpReq)

	start := time.Now()
	httpResp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, &llm.NetworkError{SDKError: llm.SDKError{Msg: fmt.Sprintf("openai-compat: %s", err.Error()), Cause: err}}
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, &llm.NetworkError{SDKError: llm.SDKError{Msg: fmt.Sprintf("openai-compat: read response: %s", err.Error()), Cause: err}}
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, llm.ErrorFromStatusCode(httpResp.StatusCode, string(respBody), "openai-compat")
	}

	resp, err := translateResponse(respBody)
	if err != nil {
		return nil, fmt.Errorf("openai-compat: translate response: %w", err)
	}

	resp.Provider = "openai-compat"
	resp.Latency = time.Since(start)

	return resp, nil
}

// Stream sends a streaming request and returns a channel of events.
func (a *Adapter) Stream(ctx context.Context, req *llm.Request) <-chan llm.StreamEvent {
	ch := make(chan llm.StreamEvent, 64)

	go func() {
		defer close(ch)

		body, err := translateRequest(req)
		if err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai-compat: translate request: %w", err)}
			return
		}

		// Inject stream: true.
		var bodyMap map[string]any
		if err := json.Unmarshal(body, &bodyMap); err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: err}
			return
		}
		bodyMap["stream"] = true
		body, err = json.Marshal(bodyMap)
		if err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: err}
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+chatCompletePath, bytes.NewReader(body))
		if err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: err}
			return
		}
		a.setHeaders(httpReq)

		httpResp, err := a.httpClient.Do(httpReq)
		if err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: &llm.NetworkError{SDKError: llm.SDKError{Msg: err.Error(), Cause: err}}}
			return
		}
		defer httpResp.Body.Close()

		if httpResp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(httpResp.Body)
			ch <- llm.StreamEvent{Type: llm.EventError, Err: llm.ErrorFromStatusCode(httpResp.StatusCode, string(respBody), "openai-compat")}
			return
		}

		a.parseSSE(httpResp.Body, ch)
	}()

	return ch
}

// Close releases resources.
func (a *Adapter) Close() error {
	return nil
}

func (a *Adapter) setHeaders(httpReq *http.Request) {
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
}

// parseSSE reads Chat Completions SSE events and emits StreamEvents.
func (a *Adapter) parseSSE(body io.Reader, ch chan<- llm.StreamEvent) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	// Track streaming tool calls by index. Chat Completions streams tool call
	// name and arguments as incremental deltas keyed by array index.
	type toolCallAcc struct {
		ID   string
		Name string
		Args strings.Builder
	}
	toolCalls := make(map[int]*toolCallAcc)
	started := false

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			// Emit accumulated tool calls before finishing.
			for _, tc := range toolCalls {
				ch <- llm.StreamEvent{
					Type: llm.EventToolCallEnd,
					ToolCall: &llm.ToolCallData{
						ID:        tc.ID,
						Name:      tc.Name,
						Arguments: json.RawMessage(tc.Args.String()),
					},
				}
			}
			break
		}

		var evt struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Choices []struct {
				Index        int    `json:"index"`
				FinishReason string `json:"finish_reason"`
				Delta        struct {
					Role      string `json:"role"`
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *chatUsage `json:"usage,omitempty"`
		}

		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai-compat: parse SSE: %w", err)}
			continue
		}

		if !started {
			ch <- llm.StreamEvent{Type: llm.EventStreamStart}
			started = true
		}

		if len(evt.Choices) == 0 {
			// Usage-only chunk (some servers send this at the end).
			if evt.Usage != nil {
				usage := translateUsage(*evt.Usage)
				ch <- llm.StreamEvent{
					Type:  llm.EventFinish,
					Usage: &usage,
				}
			}
			continue
		}

		choice := evt.Choices[0]

		// Text delta.
		if choice.Delta.Content != "" {
			ch <- llm.StreamEvent{Type: llm.EventTextDelta, Delta: choice.Delta.Content}
		}

		// Tool call deltas.
		for _, tc := range choice.Delta.ToolCalls {
			acc, exists := toolCalls[tc.Index]
			if !exists {
				acc = &toolCallAcc{}
				toolCalls[tc.Index] = acc
				ch <- llm.StreamEvent{
					Type:     llm.EventToolCallStart,
					ToolCall: &llm.ToolCallData{ID: tc.ID, Name: tc.Function.Name},
				}
			}
			if tc.ID != "" {
				acc.ID = tc.ID
			}
			if tc.Function.Name != "" {
				acc.Name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				acc.Args.WriteString(tc.Function.Arguments)
				ch <- llm.StreamEvent{Type: llm.EventToolCallDelta, Delta: tc.Function.Arguments}
			}
		}

		// Finish reason.
		if choice.FinishReason != "" {
			hasTCs := len(toolCalls) > 0
			fr := translateFinishReason(choice.FinishReason, hasTCs)
			finishEvt := llm.StreamEvent{
				Type:         llm.EventFinish,
				FinishReason: &fr,
			}
			if evt.Usage != nil {
				usage := translateUsage(*evt.Usage)
				finishEvt.Usage = &usage
			}
			ch <- finishEvt
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		ch <- llm.StreamEvent{Type: llm.EventError, Err: fmt.Errorf("openai-compat: SSE scan error: %w", err)}
	}
}
```

- [ ] **Step 4: Run Complete() tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./llm/openaicompat/ -run TestComplete -v`
Expected: all PASS

- [ ] **Step 5: Write Stream() tests**

Add to `llm/openaicompat/adapter_test.go`:

```go
func TestStream_TextResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		chunks := []string{
			`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
			`data: [DONE]`,
		}
		for _, chunk := range chunks {
			fmt.Fprintln(w, chunk)
			fmt.Fprintln(w)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	adapter := New("test-key", WithBaseURL(srv.URL))
	ch := adapter.Stream(context.Background(), &llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentPart{{Kind: llm.KindText, Text: "hi"}}}},
	})

	var text strings.Builder
	var gotStart, gotFinish bool
	for evt := range ch {
		switch evt.Type {
		case llm.EventStreamStart:
			gotStart = true
		case llm.EventTextDelta:
			text.WriteString(evt.Delta)
		case llm.EventFinish:
			gotFinish = true
			if evt.FinishReason == nil || evt.FinishReason.Reason != "stop" {
				t.Errorf("finish reason = %v, want stop", evt.FinishReason)
			}
		case llm.EventError:
			t.Fatalf("unexpected error: %v", evt.Err)
		}
	}

	if !gotStart {
		t.Error("missing EventStreamStart")
	}
	if !gotFinish {
		t.Error("missing EventFinish")
	}
	if text.String() != "Hello world" {
		t.Errorf("text = %q, want %q", text.String(), "Hello world")
	}
}

func TestStream_ToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		chunks := []string{
			`data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\""}}]},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"main.go\"}"}}]},"finish_reason":null}]}`,
			`data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		}
		for _, chunk := range chunks {
			fmt.Fprintln(w, chunk)
			fmt.Fprintln(w)
			flusher.Flush()
		}
	}))
	defer srv.Close()

	adapter := New("test-key", WithBaseURL(srv.URL))
	ch := adapter.Stream(context.Background(), &llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentPart{{Kind: llm.KindText, Text: "read main.go"}}}},
	})

	var gotTCStart, gotTCEnd bool
	var argDeltas strings.Builder
	for evt := range ch {
		switch evt.Type {
		case llm.EventToolCallStart:
			gotTCStart = true
			if evt.ToolCall.Name != "read_file" {
				t.Errorf("tool call name = %q, want read_file", evt.ToolCall.Name)
			}
		case llm.EventToolCallDelta:
			argDeltas.WriteString(evt.Delta)
		case llm.EventToolCallEnd:
			gotTCEnd = true
			if evt.ToolCall.Name != "read_file" {
				t.Errorf("tool call end name = %q, want read_file", evt.ToolCall.Name)
			}
			if string(evt.ToolCall.Arguments) != `{"path":"main.go"}` {
				t.Errorf("arguments = %s, want {\"path\":\"main.go\"}", evt.ToolCall.Arguments)
			}
		case llm.EventError:
			t.Fatalf("unexpected error: %v", evt.Err)
		}
	}

	if !gotTCStart {
		t.Error("missing EventToolCallStart")
	}
	if !gotTCEnd {
		t.Error("missing EventToolCallEnd")
	}
}

func TestStream_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer srv.Close()

	adapter := New("test-key", WithBaseURL(srv.URL))
	ch := adapter.Stream(context.Background(), &llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentPart{{Kind: llm.KindText, Text: "hi"}}}},
	})

	var gotError bool
	for evt := range ch {
		if evt.Type == llm.EventError {
			gotError = true
		}
	}
	if !gotError {
		t.Error("expected error event for 429")
	}
}
```

- [ ] **Step 6: Run all adapter tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./llm/openaicompat/ -v`
Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add llm/openaicompat/adapter.go llm/openaicompat/adapter_test.go
git commit -m "feat: add openai-compat adapter with Complete() and Stream()"
```

---

## Task 3: Provider registration

**Files:**
- Modify: `llm/client.go:95-102` (providerEnvKeys and providerPriority)
- Modify: `tracker.go:244-278` (buildClient constructors)
- Modify: `cmd/tracker/run.go:403-426` (buildLLMClient constructors)

- [ ] **Step 1: Add openai-compat to providerEnvKeys in llm/client.go**

In `llm/client.go`, add to the `providerEnvKeys` map (after the "gemini" entry):

```go
"openai-compat": {"OPENAI_COMPAT_API_KEY"},
```

Note: No OPENAI_API_KEY fallback — avoids silently routing OpenAI keys to the compat endpoint (default: OpenRouter).

And add to `providerPriority` (append at end — lowest priority):

```go
var providerPriority = []string{"anthropic", "openai", "gemini", "openai-compat"}
```

- [ ] **Step 2: Add openai-compat constructor to tracker.go buildClient()**

Add import:
```go
openaicompat "github.com/2389-research/tracker/llm/openaicompat"
```

Add to the `constructors` map in `buildClient()`:
```go
"openai-compat": func(key string) (llm.ProviderAdapter, error) {
    var opts []openaicompat.Option
    if base := os.Getenv("OPENAI_COMPAT_BASE_URL"); base != "" {
        opts = append(opts, openaicompat.WithBaseURL(base))
    }
    return openaicompat.New(key, opts...), nil
},
```

Update the error message:
```go
return nil, fmt.Errorf("unknown provider %q (valid: anthropic, openai, gemini, openai-compat)", provider)
```

- [ ] **Step 3: Add openai-compat constructor to cmd/tracker/run.go buildLLMClient()**

Add import:
```go
openaicompat "github.com/2389-research/tracker/llm/openaicompat"
```

Add to the `constructors` map in `buildLLMClient()`:
```go
"openai-compat": func(key string) (llm.ProviderAdapter, error) {
    var opts []openaicompat.Option
    if base := os.Getenv("OPENAI_COMPAT_BASE_URL"); base != "" {
        opts = append(opts, openaicompat.WithBaseURL(base))
    }
    return openaicompat.New(key, opts...), nil
},
```

- [ ] **Step 4: Build and run full test suite**

Run: `cd /Users/harper/Public/src/2389/tracker && go build ./... && go test ./... -short`
Expected: compiles, all packages pass

- [ ] **Step 5: Commit**

```bash
git add llm/client.go tracker.go cmd/tracker/run.go
git commit -m "feat: register openai-compat provider in client and constructors"
```

---

## Task 4: Update .dip file and validate

**Files:**
- Modify: `/Users/harper/workspace/2389/codegen-tracks/build3/build-codeagent-local.dip`

- [ ] **Step 1: Change provider to openai-compat in build-codeagent-local.dip**

Replace all `provider: openai` occurrences with `provider: openai-compat`. There should be occurrences in:
- `defaults` block
- `ImplementProvider`, `ImplementTools`, `ImplementConversation`, `ImplementAgent`, `ImplementREPL`, `ImplementMain`, `WriteTests`, `FixIssues` (coding agents)
- `QualityReview`, `SecurityReview` (review agents)

- [ ] **Step 2: Rebuild and install tracker**

Run: `cd /Users/harper/Public/src/2389/tracker && go install ./cmd/tracker/`

- [ ] **Step 3: Validate the pipeline**

Run: `cd /Users/harper/workspace/2389/codegen-tracks/build3 && tracker validate build-codeagent-local.dip`
Expected: "valid with N warning(s)" — no errors. DIP108 warnings about unknown models are expected (validator doesn't know local model names).

- [ ] **Step 4: Commit the .dip file change**

```bash
cd /Users/harper/workspace/2389/codegen-tracks/build3
git add build-codeagent-local.dip
git commit -m "feat: switch build-codeagent-local to openai-compat provider"
```

---

## Task 5: End-to-end verification

- [ ] **Step 1: Run full test suite one final time**

Run: `cd /Users/harper/Public/src/2389/tracker && go vet ./... && go test ./... -short -count=1`
Expected: all packages pass, no vet issues

- [ ] **Step 2: Manual smoke test with LM Studio**

Run: `cd /Users/harper/workspace/2389/codegen-tracks/build3 && OPENAI_COMPAT_BASE_URL=http://localhost:1234/v1 OPENAI_COMPAT_API_KEY=lm-studio tracker build-codeagent-local.dip`
Expected: pipeline starts, Start node executes successfully, first agent node connects to LM Studio and gets a response.
