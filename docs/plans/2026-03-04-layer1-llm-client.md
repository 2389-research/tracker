# Layer 1: Unified LLM Client — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a multi-provider LLM client library in Go with Anthropic, OpenAI, and Google adapters, channel-based streaming, middleware, and a model catalog.

**Architecture:** Four-layer design (Provider Spec → Provider Utilities → Core Client → High-Level API). Each provider adapter speaks the provider's native API. Streaming via Go channels. Middleware via onion pattern.

**Tech Stack:** Go 1.23+, standard library HTTP, no external dependencies for core.

---

### Task 1: Project Scaffolding

**Files:**
- Create: `go.mod`
- Create: `llm/types.go`

**Step 1: Initialize Go module**

Run: `go mod init github.com/2389-research/mammoth-lite`
Expected: `go.mod` created

**Step 2: Write the core type definitions (test-first — type compilation test)**

Create `llm/types_test.go`:

```go
// ABOUTME: Tests that core LLM types compile and construct correctly.
// ABOUTME: Validates Message, ContentPart, Request, Response, Usage types.
package llm

import (
	"testing"
)

func TestMessageConstruction(t *testing.T) {
	msg := SystemMessage("You are helpful.")
	if msg.Role != RoleSystem {
		t.Errorf("expected RoleSystem, got %v", msg.Role)
	}
	if msg.Text() != "You are helpful." {
		t.Errorf("expected text, got %q", msg.Text())
	}
}

func TestUserMessage(t *testing.T) {
	msg := UserMessage("Hello")
	if msg.Role != RoleUser {
		t.Errorf("expected RoleUser, got %v", msg.Role)
	}
	if msg.Text() != "Hello" {
		t.Errorf("expected Hello, got %q", msg.Text())
	}
}

func TestAssistantMessage(t *testing.T) {
	msg := AssistantMessage("Hi there")
	if msg.Role != RoleAssistant {
		t.Errorf("expected RoleAssistant, got %v", msg.Role)
	}
}

func TestToolResultMessage(t *testing.T) {
	msg := ToolResultMessage("call_123", "72F and sunny", false)
	if msg.Role != RoleTool {
		t.Errorf("expected RoleTool, got %v", msg.Role)
	}
	if msg.ToolCallID != "call_123" {
		t.Errorf("expected call_123, got %q", msg.ToolCallID)
	}
}

func TestUsageAddition(t *testing.T) {
	a := Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150}
	b := Usage{InputTokens: 200, OutputTokens: 100, TotalTokens: 300}
	c := a.Add(b)
	if c.InputTokens != 300 {
		t.Errorf("expected 300 input tokens, got %d", c.InputTokens)
	}
	if c.TotalTokens != 450 {
		t.Errorf("expected 450 total tokens, got %d", c.TotalTokens)
	}
}
```

**Step 3: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/mammoth-lite && go test ./llm/ -v`
Expected: FAIL — types not defined yet

**Step 4: Implement core types**

Create `llm/types.go`:

```go
// ABOUTME: Core data types for the unified LLM client library.
// ABOUTME: Defines Message, ContentPart, Request, Response, Usage, and related types.
package llm

import (
	"encoding/json"
	"strings"
	"time"
)

// Role represents who produced a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleDeveloper Role = "developer"
)

// ContentKind discriminates ContentPart variants.
type ContentKind string

const (
	KindText             ContentKind = "text"
	KindImage            ContentKind = "image"
	KindAudio            ContentKind = "audio"
	KindDocument         ContentKind = "document"
	KindToolCall         ContentKind = "tool_call"
	KindToolResult       ContentKind = "tool_result"
	KindThinking         ContentKind = "thinking"
	KindRedactedThinking ContentKind = "redacted_thinking"
)

// ImageData holds image content as URL or raw bytes.
type ImageData struct {
	URL       string `json:"url,omitempty"`
	Data      []byte `json:"data,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// AudioData holds audio content.
type AudioData struct {
	URL       string `json:"url,omitempty"`
	Data      []byte `json:"data,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

// DocumentData holds document content.
type DocumentData struct {
	URL       string `json:"url,omitempty"`
	Data      []byte `json:"data,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	FileName  string `json:"file_name,omitempty"`
}

// ToolCallData represents a model-initiated tool invocation.
type ToolCallData struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolResultData represents the result of executing a tool call.
type ToolResultData struct {
	ToolCallID    string `json:"tool_call_id"`
	Content       string `json:"content"`
	IsError       bool   `json:"is_error"`
	ImageData     []byte `json:"image_data,omitempty"`
	ImageMediaType string `json:"image_media_type,omitempty"`
}

// ThinkingData holds model reasoning content.
type ThinkingData struct {
	Text      string `json:"text"`
	Signature string `json:"signature,omitempty"`
	Redacted  bool   `json:"redacted,omitempty"`
}

// ContentPart is a tagged union representing one piece of message content.
type ContentPart struct {
	Kind       ContentKind   `json:"kind"`
	Text       string        `json:"text,omitempty"`
	Image      *ImageData    `json:"image,omitempty"`
	Audio      *AudioData    `json:"audio,omitempty"`
	Document   *DocumentData `json:"document,omitempty"`
	ToolCall   *ToolCallData `json:"tool_call,omitempty"`
	ToolResult *ToolResultData `json:"tool_result,omitempty"`
	Thinking   *ThinkingData `json:"thinking,omitempty"`
}

// Message is the fundamental unit of conversation.
type Message struct {
	Role       Role          `json:"role"`
	Content    []ContentPart `json:"content"`
	Name       string        `json:"name,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

// Text returns concatenated text from all text content parts.
func (m Message) Text() string {
	var parts []string
	for _, c := range m.Content {
		if c.Kind == KindText {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "")
}

// ToolCalls extracts all tool call content parts from the message.
func (m Message) ToolCalls() []ToolCallData {
	var calls []ToolCallData
	for _, c := range m.Content {
		if c.Kind == KindToolCall && c.ToolCall != nil {
			calls = append(calls, *c.ToolCall)
		}
	}
	return calls
}

// Convenience constructors
func SystemMessage(text string) Message {
	return Message{Role: RoleSystem, Content: []ContentPart{{Kind: KindText, Text: text}}}
}

func UserMessage(text string) Message {
	return Message{Role: RoleUser, Content: []ContentPart{{Kind: KindText, Text: text}}}
}

func AssistantMessage(text string) Message {
	return Message{Role: RoleAssistant, Content: []ContentPart{{Kind: KindText, Text: text}}}
}

func ToolResultMessage(toolCallID, content string, isError bool) Message {
	return Message{
		Role:       RoleTool,
		ToolCallID: toolCallID,
		Content: []ContentPart{{
			Kind: KindToolResult,
			ToolResult: &ToolResultData{
				ToolCallID: toolCallID,
				Content:    content,
				IsError:    isError,
			},
		}},
	}
}

// ToolChoice controls how the model uses tools.
type ToolChoice struct {
	Mode     string `json:"mode"`
	ToolName string `json:"tool_name,omitempty"`
}

var (
	ToolChoiceAuto     = ToolChoice{Mode: "auto"}
	ToolChoiceNone     = ToolChoice{Mode: "none"}
	ToolChoiceRequired = ToolChoice{Mode: "required"}
)

func ToolChoiceNamed(name string) ToolChoice {
	return ToolChoice{Mode: "named", ToolName: name}
}

// ResponseFormat specifies output format constraints.
type ResponseFormat struct {
	Type       string          `json:"type"`
	JSONSchema json.RawMessage `json:"json_schema,omitempty"`
	Strict     bool            `json:"strict,omitempty"`
}

// ToolDefinition defines a tool the model can call.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Request is the input for Complete() and Stream().
type Request struct {
	Model           string            `json:"model"`
	Messages        []Message         `json:"messages"`
	Provider        string            `json:"provider,omitempty"`
	Tools           []ToolDefinition  `json:"tools,omitempty"`
	ToolChoice      *ToolChoice       `json:"tool_choice,omitempty"`
	ResponseFormat  *ResponseFormat   `json:"response_format,omitempty"`
	Temperature     *float64          `json:"temperature,omitempty"`
	TopP            *float64          `json:"top_p,omitempty"`
	MaxTokens       *int              `json:"max_tokens,omitempty"`
	StopSequences   []string          `json:"stop_sequences,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	ProviderOptions map[string]any    `json:"provider_options,omitempty"`
}

// FinishReason indicates why generation stopped.
type FinishReason struct {
	Reason string `json:"reason"`
	Raw    string `json:"raw,omitempty"`
}

// Usage tracks token consumption.
type Usage struct {
	InputTokens      int      `json:"input_tokens"`
	OutputTokens     int      `json:"output_tokens"`
	TotalTokens      int      `json:"total_tokens"`
	ReasoningTokens  *int     `json:"reasoning_tokens,omitempty"`
	CacheReadTokens  *int     `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int     `json:"cache_write_tokens,omitempty"`
	EstimatedCost    float64  `json:"estimated_cost,omitempty"`
	Raw              any      `json:"raw,omitempty"`
}

// Add combines two Usage values.
func (u Usage) Add(other Usage) Usage {
	result := Usage{
		InputTokens:  u.InputTokens + other.InputTokens,
		OutputTokens: u.OutputTokens + other.OutputTokens,
		TotalTokens:  u.TotalTokens + other.TotalTokens,
	}
	result.ReasoningTokens = addOptionalInt(u.ReasoningTokens, other.ReasoningTokens)
	result.CacheReadTokens = addOptionalInt(u.CacheReadTokens, other.CacheReadTokens)
	result.CacheWriteTokens = addOptionalInt(u.CacheWriteTokens, other.CacheWriteTokens)
	result.EstimatedCost = u.EstimatedCost + other.EstimatedCost
	return result
}

func addOptionalInt(a, b *int) *int {
	if a == nil && b == nil {
		return nil
	}
	va, vb := 0, 0
	if a != nil {
		va = *a
	}
	if b != nil {
		vb = *b
	}
	sum := va + vb
	return &sum
}

// Warning represents a non-fatal issue.
type Warning struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// RateLimitInfo holds rate limit metadata from provider headers.
type RateLimitInfo struct {
	RequestsRemaining *int       `json:"requests_remaining,omitempty"`
	RequestsLimit     *int       `json:"requests_limit,omitempty"`
	TokensRemaining   *int       `json:"tokens_remaining,omitempty"`
	TokensLimit       *int       `json:"tokens_limit,omitempty"`
	ResetAt           *time.Time `json:"reset_at,omitempty"`
}

// Response is the output of Complete().
type Response struct {
	ID           string          `json:"id"`
	Model        string          `json:"model"`
	Provider     string          `json:"provider"`
	Message      Message         `json:"message"`
	FinishReason FinishReason    `json:"finish_reason"`
	Usage        Usage           `json:"usage"`
	Latency      time.Duration   `json:"latency"`
	Raw          json.RawMessage `json:"raw,omitempty"`
	Warnings     []Warning       `json:"warnings,omitempty"`
	RateLimit    *RateLimitInfo  `json:"rate_limit,omitempty"`
}

// Text returns concatenated text from the response message.
func (r Response) Text() string {
	return r.Message.Text()
}

// ToolCalls returns tool calls from the response message.
func (r Response) ToolCalls() []ToolCallData {
	return r.Message.ToolCalls()
}

// Reasoning returns concatenated reasoning/thinking text.
func (r Response) Reasoning() string {
	var parts []string
	for _, c := range r.Message.Content {
		if c.Kind == KindThinking && c.Thinking != nil {
			parts = append(parts, c.Thinking.Text)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "")
}
```

**Step 5: Run tests to verify they pass**

Run: `cd /Users/harper/Public/src/2389/mammoth-lite && go test ./llm/ -v`
Expected: PASS

**Step 6: Commit**

```bash
git add go.mod llm/
git commit -m "feat: add core LLM types (Message, Request, Response, Usage)"
```

---

### Task 2: Stream Event Types

**Files:**
- Create: `llm/stream.go`
- Create: `llm/stream_test.go`

**Step 1: Write failing test**

Create `llm/stream_test.go`:

```go
// ABOUTME: Tests for streaming event types and channel-based streaming.
// ABOUTME: Validates StreamEvent construction and StreamAccumulator.
package llm

import (
	"testing"
)

func TestStreamEventTextDelta(t *testing.T) {
	event := StreamEvent{
		Type:  EventTextDelta,
		Delta: "Hello",
	}
	if event.Type != EventTextDelta {
		t.Errorf("expected TextDelta, got %v", event.Type)
	}
	if event.Delta != "Hello" {
		t.Errorf("expected Hello, got %q", event.Delta)
	}
}

func TestStreamAccumulator(t *testing.T) {
	acc := NewStreamAccumulator()

	acc.Process(StreamEvent{Type: EventStreamStart})
	acc.Process(StreamEvent{Type: EventTextStart, TextID: "t1"})
	acc.Process(StreamEvent{Type: EventTextDelta, Delta: "Hello ", TextID: "t1"})
	acc.Process(StreamEvent{Type: EventTextDelta, Delta: "world", TextID: "t1"})
	acc.Process(StreamEvent{Type: EventTextEnd, TextID: "t1"})
	acc.Process(StreamEvent{
		Type:         EventFinish,
		FinishReason: &FinishReason{Reason: "stop"},
		Usage:        &Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	})

	resp := acc.Response()
	if resp.Text() != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", resp.Text())
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("expected 15 total tokens, got %d", resp.Usage.TotalTokens)
	}
	if resp.FinishReason.Reason != "stop" {
		t.Errorf("expected stop, got %q", resp.FinishReason.Reason)
	}
}
```

**Step 2: Run test to verify failure**

Run: `go test ./llm/ -v -run TestStream`
Expected: FAIL

**Step 3: Implement stream types**

Create `llm/stream.go`:

```go
// ABOUTME: Stream event types and accumulator for channel-based LLM streaming.
// ABOUTME: Defines StreamEvent, StreamEventType, and StreamAccumulator.
package llm

import "strings"

// StreamEventType discriminates stream events.
type StreamEventType string

const (
	EventStreamStart    StreamEventType = "stream_start"
	EventTextStart      StreamEventType = "text_start"
	EventTextDelta      StreamEventType = "text_delta"
	EventTextEnd        StreamEventType = "text_end"
	EventReasoningStart StreamEventType = "reasoning_start"
	EventReasoningDelta StreamEventType = "reasoning_delta"
	EventReasoningEnd   StreamEventType = "reasoning_end"
	EventToolCallStart  StreamEventType = "tool_call_start"
	EventToolCallDelta  StreamEventType = "tool_call_delta"
	EventToolCallEnd    StreamEventType = "tool_call_end"
	EventFinish         StreamEventType = "finish"
	EventError          StreamEventType = "error"
	EventProviderEvent  StreamEventType = "provider_event"
)

// StreamEvent is a single event in an LLM response stream.
type StreamEvent struct {
	Type StreamEventType

	// Text events
	Delta  string
	TextID string

	// Reasoning events
	ReasoningDelta string

	// Tool call events
	ToolCall *ToolCallData

	// Finish event
	FinishReason *FinishReason
	Usage        *Usage
	FullResponse *Response

	// Error event
	Err error

	// Raw provider event
	Raw any
}

// StreamAccumulator collects StreamEvents into a complete Response.
type StreamAccumulator struct {
	textParts      map[string]*strings.Builder
	textOrder      []string
	reasoningParts []string
	toolCalls      []ToolCallData
	finishReason   FinishReason
	usage          Usage
	id             string
	model          string
	provider       string
}

// NewStreamAccumulator creates a new accumulator.
func NewStreamAccumulator() *StreamAccumulator {
	return &StreamAccumulator{
		textParts: make(map[string]*strings.Builder),
	}
}

// Process handles a single stream event.
func (a *StreamAccumulator) Process(event StreamEvent) {
	switch event.Type {
	case EventTextStart:
		if event.TextID != "" {
			a.textParts[event.TextID] = &strings.Builder{}
			a.textOrder = append(a.textOrder, event.TextID)
		}
	case EventTextDelta:
		id := event.TextID
		if id == "" {
			id = "_default"
		}
		if _, ok := a.textParts[id]; !ok {
			a.textParts[id] = &strings.Builder{}
			a.textOrder = append(a.textOrder, id)
		}
		a.textParts[id].WriteString(event.Delta)
	case EventReasoningDelta:
		a.reasoningParts = append(a.reasoningParts, event.ReasoningDelta)
	case EventToolCallEnd:
		if event.ToolCall != nil {
			a.toolCalls = append(a.toolCalls, *event.ToolCall)
		}
	case EventFinish:
		if event.FinishReason != nil {
			a.finishReason = *event.FinishReason
		}
		if event.Usage != nil {
			a.usage = *event.Usage
		}
	}
}

// Response returns the accumulated response.
func (a *StreamAccumulator) Response() Response {
	var content []ContentPart

	// Add text parts in order
	var fullText strings.Builder
	for _, id := range a.textOrder {
		if b, ok := a.textParts[id]; ok {
			fullText.WriteString(b.String())
		}
	}
	if fullText.Len() > 0 {
		content = append(content, ContentPart{Kind: KindText, Text: fullText.String()})
	}

	// Add reasoning
	if len(a.reasoningParts) > 0 {
		reasoning := strings.Join(a.reasoningParts, "")
		content = append(content, ContentPart{
			Kind:     KindThinking,
			Thinking: &ThinkingData{Text: reasoning},
		})
	}

	// Add tool calls
	for i := range a.toolCalls {
		tc := a.toolCalls[i]
		content = append(content, ContentPart{
			Kind:     KindToolCall,
			ToolCall: &tc,
		})
	}

	return Response{
		ID:       a.id,
		Model:    a.model,
		Provider: a.provider,
		Message: Message{
			Role:    RoleAssistant,
			Content: content,
		},
		FinishReason: a.finishReason,
		Usage:        a.usage,
	}
}
```

**Step 4: Run tests**

Run: `go test ./llm/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add llm/stream.go llm/stream_test.go
git commit -m "feat: add stream event types and accumulator"
```

---

### Task 3: Error Types

**Files:**
- Create: `llm/errors.go`
- Create: `llm/errors_test.go`

**Step 1: Write failing test**

Create `llm/errors_test.go`:

```go
// ABOUTME: Tests for the LLM error type hierarchy.
// ABOUTME: Validates error classification, retryability, and HTTP status mapping.
package llm

import (
	"errors"
	"testing"
)

func TestProviderErrorRetryable(t *testing.T) {
	err := &RateLimitError{ProviderError: ProviderError{
		SDKError:   SDKError{Msg: "rate limited"},
		StatusCode: 429,
	}}
	if !err.Retryable() {
		t.Error("RateLimitError should be retryable")
	}
}

func TestAuthErrorNotRetryable(t *testing.T) {
	err := &AuthenticationError{ProviderError: ProviderError{
		SDKError:   SDKError{Msg: "bad key"},
		StatusCode: 401,
	}}
	if err.Retryable() {
		t.Error("AuthenticationError should not be retryable")
	}
}

func TestErrorFromStatusCode(t *testing.T) {
	tests := []struct {
		status    int
		wantType  string
		retryable bool
	}{
		{400, "InvalidRequestError", false},
		{401, "AuthenticationError", false},
		{403, "AccessDeniedError", false},
		{404, "NotFoundError", false},
		{429, "RateLimitError", true},
		{500, "ServerError", true},
		{502, "ServerError", true},
		{503, "ServerError", true},
	}
	for _, tt := range tests {
		err := ErrorFromStatusCode(tt.status, "test error", "anthropic")
		var pe ProviderErrorInterface
		if !errors.As(err, &pe) {
			t.Errorf("status %d: expected ProviderErrorInterface", tt.status)
			continue
		}
		if pe.Retryable() != tt.retryable {
			t.Errorf("status %d: retryable=%v, want %v", tt.status, pe.Retryable(), tt.retryable)
		}
	}
}
```

**Step 2: Run test to verify failure**

Run: `go test ./llm/ -v -run TestProvider`
Expected: FAIL

**Step 3: Implement error types**

Create `llm/errors.go`:

```go
// ABOUTME: Error type hierarchy for the unified LLM client.
// ABOUTME: Provides typed errors with retryability classification and HTTP status mapping.
package llm

import "fmt"

// SDKError is the base error type for all library errors.
type SDKError struct {
	Msg   string
	Cause error
}

func (e *SDKError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Cause)
	}
	return e.Msg
}

func (e *SDKError) Unwrap() error { return e.Cause }

// ProviderErrorInterface is implemented by all provider errors.
type ProviderErrorInterface interface {
	error
	Retryable() bool
	GetProvider() string
	GetStatusCode() int
}

// ProviderError is the base for all provider-originated errors.
type ProviderError struct {
	SDKError
	Provider   string
	StatusCode int
	ErrorCode  string
	RetryAfter *float64
	RawBody    any
}

func (e *ProviderError) GetProvider() string  { return e.Provider }
func (e *ProviderError) GetStatusCode() int   { return e.StatusCode }

// Non-retryable errors

type AuthenticationError struct{ ProviderError }
func (e *AuthenticationError) Retryable() bool { return false }

type AccessDeniedError struct{ ProviderError }
func (e *AccessDeniedError) Retryable() bool { return false }

type NotFoundError struct{ ProviderError }
func (e *NotFoundError) Retryable() bool { return false }

type InvalidRequestError struct{ ProviderError }
func (e *InvalidRequestError) Retryable() bool { return false }

type ContextLengthError struct{ ProviderError }
func (e *ContextLengthError) Retryable() bool { return false }

type QuotaExceededError struct{ ProviderError }
func (e *QuotaExceededError) Retryable() bool { return false }

type ContentFilterError struct{ ProviderError }
func (e *ContentFilterError) Retryable() bool { return false }

type ConfigurationError struct{ SDKError }

// Retryable errors

type RateLimitError struct{ ProviderError }
func (e *RateLimitError) Retryable() bool { return true }

type ServerError struct{ ProviderError }
func (e *ServerError) Retryable() bool { return true }

type RequestTimeoutError struct{ ProviderError }
func (e *RequestTimeoutError) Retryable() bool { return true }

type NetworkError struct{ SDKError }
func (e *NetworkError) Retryable() bool { return true }

type StreamError struct{ SDKError }
func (e *StreamError) Retryable() bool { return true }

// Non-provider errors

type AbortError struct{ SDKError }
type InvalidToolCallError struct{ SDKError }
type NoObjectGeneratedError struct{ SDKError }

// ErrorFromStatusCode creates the appropriate error type from an HTTP status code.
func ErrorFromStatusCode(statusCode int, message, provider string) error {
	base := ProviderError{
		SDKError:   SDKError{Msg: message},
		Provider:   provider,
		StatusCode: statusCode,
	}
	switch statusCode {
	case 400, 422:
		return &InvalidRequestError{ProviderError: base}
	case 401:
		return &AuthenticationError{ProviderError: base}
	case 403:
		return &AccessDeniedError{ProviderError: base}
	case 404:
		return &NotFoundError{ProviderError: base}
	case 408:
		return &RequestTimeoutError{ProviderError: base}
	case 413:
		return &ContextLengthError{ProviderError: base}
	case 429:
		return &RateLimitError{ProviderError: base}
	case 500, 502, 503, 504:
		return &ServerError{ProviderError: base}
	default:
		// Unknown errors default to retryable (conservative choice)
		return &ServerError{ProviderError: base}
	}
}
```

**Step 4: Run tests**

Run: `go test ./llm/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add llm/errors.go llm/errors_test.go
git commit -m "feat: add LLM error type hierarchy with retryability"
```

---

### Task 4: Provider Adapter Interface

**Files:**
- Create: `llm/provider.go`
- Create: `llm/provider_test.go`

**Step 1: Write failing test**

Create `llm/provider_test.go`:

```go
// ABOUTME: Tests for the ProviderAdapter interface contract.
// ABOUTME: Uses a mock adapter to verify the interface works correctly.
package llm

import (
	"context"
	"testing"
)

type mockAdapter struct {
	name     string
	response *Response
	events   []StreamEvent
}

func (m *mockAdapter) Name() string { return m.name }

func (m *mockAdapter) Complete(ctx context.Context, req *Request) (*Response, error) {
	return m.response, nil
}

func (m *mockAdapter) Stream(ctx context.Context, req *Request) <-chan StreamEvent {
	ch := make(chan StreamEvent, len(m.events))
	for _, e := range m.events {
		ch <- e
	}
	close(ch)
	return ch
}

func (m *mockAdapter) Close() error { return nil }

func TestProviderAdapterInterface(t *testing.T) {
	adapter := &mockAdapter{
		name: "test",
		response: &Response{
			ID:           "resp_1",
			Model:        "test-model",
			Provider:     "test",
			Message:      AssistantMessage("Hello"),
			FinishReason: FinishReason{Reason: "stop"},
			Usage:        Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		},
	}

	// Verify it satisfies the interface
	var _ ProviderAdapter = adapter

	resp, err := adapter.Complete(context.Background(), &Request{
		Model:    "test-model",
		Messages: []Message{UserMessage("Hi")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text() != "Hello" {
		t.Errorf("expected Hello, got %q", resp.Text())
	}
}
```

**Step 2: Run test to verify failure**

Run: `go test ./llm/ -v -run TestProviderAdapter`
Expected: FAIL

**Step 3: Implement provider interface**

Create `llm/provider.go`:

```go
// ABOUTME: ProviderAdapter interface that all LLM provider adapters must implement.
// ABOUTME: Defines the contract for Complete() and Stream() operations.
package llm

import "context"

// ProviderAdapter is the interface that every provider adapter must implement.
type ProviderAdapter interface {
	// Name returns the provider identifier (e.g., "openai", "anthropic", "gemini").
	Name() string

	// Complete sends a request and blocks until the model finishes.
	Complete(ctx context.Context, req *Request) (*Response, error)

	// Stream sends a request and returns a channel of StreamEvents.
	Stream(ctx context.Context, req *Request) <-chan StreamEvent

	// Close releases resources (HTTP connections, etc.).
	Close() error
}
```

**Step 4: Run tests**

Run: `go test ./llm/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add llm/provider.go llm/provider_test.go
git commit -m "feat: add ProviderAdapter interface"
```

---

### Task 5: Core Client with Provider Routing

**Files:**
- Create: `llm/client.go`
- Create: `llm/client_test.go`

**Step 1: Write failing test**

Create `llm/client_test.go`:

```go
// ABOUTME: Tests for the core LLM Client with provider routing.
// ABOUTME: Validates client construction, routing, and default provider behavior.
package llm

import (
	"context"
	"testing"
)

func TestClientRouting(t *testing.T) {
	resp1 := &Response{Provider: "provider_a", Message: AssistantMessage("A")}
	resp2 := &Response{Provider: "provider_b", Message: AssistantMessage("B")}

	client, err := NewClient(
		WithProvider("provider_a", &mockAdapter{name: "provider_a", response: resp1}),
		WithProvider("provider_b", &mockAdapter{name: "provider_b", response: resp2}),
		WithDefaultProvider("provider_a"),
	)
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}
	defer client.Close()

	// Route to explicit provider
	r, err := client.Complete(context.Background(), &Request{
		Model:    "model-b",
		Provider: "provider_b",
		Messages: []Message{UserMessage("Hi")},
	})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if r.Text() != "B" {
		t.Errorf("expected B, got %q", r.Text())
	}

	// Route to default provider
	r, err = client.Complete(context.Background(), &Request{
		Model:    "model-a",
		Messages: []Message{UserMessage("Hi")},
	})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if r.Text() != "A" {
		t.Errorf("expected A, got %q", r.Text())
	}
}

func TestClientNoProvider(t *testing.T) {
	client, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	_, err = client.Complete(context.Background(), &Request{
		Model:    "some-model",
		Messages: []Message{UserMessage("Hi")},
	})
	if err == nil {
		t.Fatal("expected error for missing provider")
	}
}

func TestClientUnknownProvider(t *testing.T) {
	client, err := NewClient(
		WithProvider("known", &mockAdapter{name: "known", response: &Response{}}),
		WithDefaultProvider("known"),
	)
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	_, err = client.Complete(context.Background(), &Request{
		Model:    "model",
		Provider: "unknown",
		Messages: []Message{UserMessage("Hi")},
	})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestClientStream(t *testing.T) {
	events := []StreamEvent{
		{Type: EventTextDelta, Delta: "Hello"},
		{Type: EventFinish, FinishReason: &FinishReason{Reason: "stop"}},
	}

	client, err := NewClient(
		WithProvider("test", &mockAdapter{name: "test", events: events}),
		WithDefaultProvider("test"),
	)
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	ch := client.Stream(context.Background(), &Request{
		Model:    "model",
		Messages: []Message{UserMessage("Hi")},
	})

	var received []StreamEvent
	for e := range ch {
		received = append(received, e)
	}
	if len(received) != 2 {
		t.Errorf("expected 2 events, got %d", len(received))
	}
}
```

**Step 2: Run test to verify failure**

Run: `go test ./llm/ -v -run TestClient`
Expected: FAIL

**Step 3: Implement client**

Create `llm/client.go`:

```go
// ABOUTME: Core LLM Client that routes requests to provider adapters.
// ABOUTME: Supports multiple providers, default provider, and middleware.
package llm

import (
	"context"
	"fmt"
	"os"
	"time"
)

// Client routes LLM requests to registered provider adapters.
type Client struct {
	providers       map[string]ProviderAdapter
	defaultProvider string
	middleware      []Middleware
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithProvider registers a provider adapter.
func WithProvider(name string, adapter ProviderAdapter) ClientOption {
	return func(c *Client) {
		c.providers[name] = adapter
	}
}

// WithDefaultProvider sets the default provider.
func WithDefaultProvider(name string) ClientOption {
	return func(c *Client) {
		c.defaultProvider = name
	}
}

// WithMiddleware adds middleware to the client.
func WithMiddleware(mw ...Middleware) ClientOption {
	return func(c *Client) {
		c.middleware = append(c.middleware, mw...)
	}
}

// NewClient creates a new Client with the given options.
func NewClient(opts ...ClientOption) (*Client, error) {
	c := &Client{
		providers: make(map[string]ProviderAdapter),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// NewClientFromEnv creates a Client from environment variables.
// Registers providers whose API keys are present in the environment.
func NewClientFromEnv(constructors map[string]func(apiKey string) (ProviderAdapter, error)) (*Client, error) {
	envKeys := map[string]string{
		"anthropic": "ANTHROPIC_API_KEY",
		"openai":    "OPENAI_API_KEY",
		"gemini":    "GEMINI_API_KEY",
	}
	// Also accept GOOGLE_API_KEY as fallback for gemini
	altKeys := map[string]string{
		"gemini": "GOOGLE_API_KEY",
	}

	c := &Client{
		providers: make(map[string]ProviderAdapter),
	}

	for name, envKey := range envKeys {
		apiKey := os.Getenv(envKey)
		if apiKey == "" {
			if alt, ok := altKeys[name]; ok {
				apiKey = os.Getenv(alt)
			}
		}
		if apiKey == "" {
			continue
		}
		constructor, ok := constructors[name]
		if !ok {
			continue
		}
		adapter, err := constructor(apiKey)
		if err != nil {
			return nil, fmt.Errorf("failed to create %s adapter: %w", name, err)
		}
		c.providers[name] = adapter
		if c.defaultProvider == "" {
			c.defaultProvider = name
		}
	}

	return c, nil
}

// resolveProvider determines which provider to use for a request.
func (c *Client) resolveProvider(req *Request) (ProviderAdapter, error) {
	name := req.Provider
	if name == "" {
		name = c.defaultProvider
	}
	if name == "" {
		return nil, &ConfigurationError{SDKError: SDKError{
			Msg: "no provider specified and no default provider configured",
		}}
	}
	adapter, ok := c.providers[name]
	if !ok {
		return nil, &ConfigurationError{SDKError: SDKError{
			Msg: fmt.Sprintf("unknown provider: %q", name),
		}}
	}
	return adapter, nil
}

// Complete sends a request and blocks until the model finishes.
func (c *Client) Complete(ctx context.Context, req *Request) (*Response, error) {
	adapter, err := c.resolveProvider(req)
	if err != nil {
		return nil, err
	}

	start := time.Now()

	// Build the middleware chain
	handler := func(ctx context.Context, r *Request) (*Response, error) {
		return adapter.Complete(ctx, r)
	}
	for i := len(c.middleware) - 1; i >= 0; i-- {
		handler = c.middleware[i].WrapComplete(handler)
	}

	resp, err := handler(ctx, req)
	if err != nil {
		return nil, err
	}

	resp.Latency = time.Since(start)
	if resp.Provider == "" {
		resp.Provider = adapter.Name()
	}
	return resp, nil
}

// Stream sends a request and returns a channel of StreamEvents.
func (c *Client) Stream(ctx context.Context, req *Request) <-chan StreamEvent {
	adapter, err := c.resolveProvider(req)
	if err != nil {
		ch := make(chan StreamEvent, 1)
		ch <- StreamEvent{Type: EventError, Err: err}
		close(ch)
		return ch
	}

	return adapter.Stream(ctx, req)
}

// Close releases all provider resources.
func (c *Client) Close() error {
	var firstErr error
	for _, adapter := range c.providers {
		if err := adapter.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
```

**Step 4: Run tests**

Run: `go test ./llm/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add llm/client.go llm/client_test.go
git commit -m "feat: add core Client with provider routing"
```

---

### Task 6: Middleware System

**Files:**
- Create: `llm/middleware.go`
- Create: `llm/middleware_test.go`

**Step 1: Write failing test**

Create `llm/middleware_test.go`:

```go
// ABOUTME: Tests for the middleware chain (onion pattern).
// ABOUTME: Validates middleware execution order and request/response interception.
package llm

import (
	"context"
	"testing"
)

func TestMiddlewareOrder(t *testing.T) {
	var order []string

	mw1 := MiddlewareFunc(func(next CompleteHandler) CompleteHandler {
		return func(ctx context.Context, req *Request) (*Response, error) {
			order = append(order, "mw1_before")
			resp, err := next(ctx, req)
			order = append(order, "mw1_after")
			return resp, err
		}
	})

	mw2 := MiddlewareFunc(func(next CompleteHandler) CompleteHandler {
		return func(ctx context.Context, req *Request) (*Response, error) {
			order = append(order, "mw2_before")
			resp, err := next(ctx, req)
			order = append(order, "mw2_after")
			return resp, err
		}
	})

	client, err := NewClient(
		WithProvider("test", &mockAdapter{
			name:     "test",
			response: &Response{Message: AssistantMessage("ok")},
		}),
		WithDefaultProvider("test"),
		WithMiddleware(mw1, mw2),
	)
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	_, err = client.Complete(context.Background(), &Request{
		Model:    "m",
		Messages: []Message{UserMessage("Hi")},
	})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}

	expected := []string{"mw1_before", "mw2_before", "mw2_after", "mw1_after"}
	if len(order) != len(expected) {
		t.Fatalf("expected %d calls, got %d: %v", len(expected), len(order), order)
	}
	for i, want := range expected {
		if order[i] != want {
			t.Errorf("order[%d] = %q, want %q", i, order[i], want)
		}
	}
}
```

**Step 2: Run test to verify failure**

Run: `go test ./llm/ -v -run TestMiddleware`
Expected: FAIL

**Step 3: Implement middleware**

Create `llm/middleware.go`:

```go
// ABOUTME: Middleware system for the LLM client (onion/chain-of-responsibility pattern).
// ABOUTME: Supports request/response interception for logging, caching, rate limiting.
package llm

import "context"

// CompleteHandler is a function that handles a Complete request.
type CompleteHandler func(ctx context.Context, req *Request) (*Response, error)

// Middleware wraps a CompleteHandler with additional behavior.
type Middleware interface {
	WrapComplete(next CompleteHandler) CompleteHandler
}

// MiddlewareFunc adapts a function to the Middleware interface.
type MiddlewareFunc func(next CompleteHandler) CompleteHandler

func (f MiddlewareFunc) WrapComplete(next CompleteHandler) CompleteHandler {
	return f(next)
}
```

**Step 4: Run tests**

Run: `go test ./llm/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add llm/middleware.go llm/middleware_test.go
git commit -m "feat: add middleware system with onion pattern"
```

---

### Task 7: Model Catalog

**Files:**
- Create: `llm/catalog.go`
- Create: `llm/catalog_test.go`

**Step 1: Write failing test**

Create `llm/catalog_test.go`:

```go
// ABOUTME: Tests for the model catalog (ModelInfo registry).
// ABOUTME: Validates model lookup, listing, and filtering by provider/capability.
package llm

import "testing"

func TestGetModelInfo(t *testing.T) {
	info := GetModelInfo("claude-opus-4-6")
	if info == nil {
		t.Fatal("expected model info for claude-opus-4-6")
	}
	if info.Provider != "anthropic" {
		t.Errorf("expected anthropic, got %q", info.Provider)
	}
	if !info.SupportsTools {
		t.Error("claude-opus-4-6 should support tools")
	}
}

func TestGetModelInfoUnknown(t *testing.T) {
	info := GetModelInfo("nonexistent-model")
	if info != nil {
		t.Error("expected nil for unknown model")
	}
}

func TestListModels(t *testing.T) {
	all := ListModels("")
	if len(all) == 0 {
		t.Error("expected at least one model")
	}

	anthropic := ListModels("anthropic")
	if len(anthropic) == 0 {
		t.Error("expected at least one anthropic model")
	}
	for _, m := range anthropic {
		if m.Provider != "anthropic" {
			t.Errorf("expected anthropic, got %q", m.Provider)
		}
	}
}

func TestGetLatestModel(t *testing.T) {
	m := GetLatestModel("anthropic", "")
	if m == nil {
		t.Fatal("expected a latest anthropic model")
	}
	if m.Provider != "anthropic" {
		t.Errorf("expected anthropic, got %q", m.Provider)
	}
}
```

**Step 2: Run test to verify failure**

Run: `go test ./llm/ -v -run TestGet`
Expected: FAIL

**Step 3: Implement catalog**

Create `llm/catalog.go`:

```go
// ABOUTME: Model catalog for known LLM models across providers.
// ABOUTME: Provides lookup, listing, and capability-based model selection.
package llm

// ModelInfo describes a known LLM model.
type ModelInfo struct {
	ID                string   `json:"id"`
	Provider          string   `json:"provider"`
	DisplayName       string   `json:"display_name"`
	ContextWindow     int      `json:"context_window"`
	MaxOutput         int      `json:"max_output,omitempty"`
	SupportsTools     bool     `json:"supports_tools"`
	SupportsVision    bool     `json:"supports_vision"`
	SupportsReasoning bool     `json:"supports_reasoning"`
	InputCostPerM     float64  `json:"input_cost_per_million,omitempty"`
	OutputCostPerM    float64  `json:"output_cost_per_million,omitempty"`
	Aliases           []string `json:"aliases,omitempty"`
}

var defaultCatalog = []ModelInfo{
	// Anthropic
	{ID: "claude-opus-4-6", Provider: "anthropic", DisplayName: "Claude Opus 4.6", ContextWindow: 200000, SupportsTools: true, SupportsVision: true, SupportsReasoning: true},
	{ID: "claude-sonnet-4-5", Provider: "anthropic", DisplayName: "Claude Sonnet 4.5", ContextWindow: 200000, SupportsTools: true, SupportsVision: true, SupportsReasoning: true},

	// OpenAI
	{ID: "gpt-5.2", Provider: "openai", DisplayName: "GPT-5.2", ContextWindow: 1047576, SupportsTools: true, SupportsVision: true, SupportsReasoning: true},
	{ID: "gpt-5.2-mini", Provider: "openai", DisplayName: "GPT-5.2 Mini", ContextWindow: 1047576, SupportsTools: true, SupportsVision: true, SupportsReasoning: true},
	{ID: "gpt-5.2-codex", Provider: "openai", DisplayName: "GPT-5.2 Codex", ContextWindow: 1047576, SupportsTools: true, SupportsVision: true, SupportsReasoning: true},

	// Gemini
	{ID: "gemini-3-pro-preview", Provider: "gemini", DisplayName: "Gemini 3 Pro (Preview)", ContextWindow: 1048576, SupportsTools: true, SupportsVision: true, SupportsReasoning: true},
	{ID: "gemini-3-flash-preview", Provider: "gemini", DisplayName: "Gemini 3 Flash (Preview)", ContextWindow: 1048576, SupportsTools: true, SupportsVision: true, SupportsReasoning: true},
}

// GetModelInfo returns catalog info for a model, or nil if unknown.
func GetModelInfo(modelID string) *ModelInfo {
	for i := range defaultCatalog {
		if defaultCatalog[i].ID == modelID {
			return &defaultCatalog[i]
		}
		for _, alias := range defaultCatalog[i].Aliases {
			if alias == modelID {
				return &defaultCatalog[i]
			}
		}
	}
	return nil
}

// ListModels returns all known models, optionally filtered by provider.
func ListModels(provider string) []ModelInfo {
	var result []ModelInfo
	for _, m := range defaultCatalog {
		if provider == "" || m.Provider == provider {
			result = append(result, m)
		}
	}
	return result
}

// GetLatestModel returns the first (newest) model for a provider,
// optionally filtered by capability ("reasoning", "vision", "tools").
func GetLatestModel(provider, capability string) *ModelInfo {
	for i := range defaultCatalog {
		m := &defaultCatalog[i]
		if m.Provider != provider {
			continue
		}
		switch capability {
		case "reasoning":
			if !m.SupportsReasoning {
				continue
			}
		case "vision":
			if !m.SupportsVision {
				continue
			}
		case "tools":
			if !m.SupportsTools {
				continue
			}
		}
		return m
	}
	return nil
}
```

**Step 4: Run tests**

Run: `go test ./llm/ -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add llm/catalog.go llm/catalog_test.go
git commit -m "feat: add model catalog with lookup and filtering"
```

---

### Task 8: Anthropic Adapter

**Files:**
- Create: `llm/anthropic/adapter.go`
- Create: `llm/anthropic/adapter_test.go`
- Create: `llm/anthropic/translate.go`
- Create: `llm/anthropic/stream.go`

This is the largest task. The Anthropic adapter speaks the Messages API (`/v1/messages`), handles strict message alternation, `cache_control` injection, thinking blocks, and SSE streaming.

**Step 1: Write failing test for request translation**

Create `llm/anthropic/adapter_test.go`:

```go
// ABOUTME: Tests for the Anthropic Messages API adapter.
// ABOUTME: Validates request/response translation and SSE stream parsing.
package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/mammoth-lite/llm"
)

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

	// Anthropic requires max_tokens; should default to 4096
	maxTokens, ok := parsed["max_tokens"].(float64)
	if !ok {
		t.Fatal("expected max_tokens")
	}
	if int(maxTokens) != 4096 {
		t.Errorf("expected default 4096, got %v", maxTokens)
	}
}

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
}

func intPtr(v int) *int { return &v }
```

**Step 2: Run test to verify failure**

Run: `go test ./llm/anthropic/ -v`
Expected: FAIL

**Step 3: Implement Anthropic adapter**

Create `llm/anthropic/translate.go`:

```go
// ABOUTME: Request and response translation for the Anthropic Messages API.
// ABOUTME: Handles system extraction, message alternation, tool use, and thinking blocks.
package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/2389-research/mammoth-lite/llm"
)

// translateRequest converts a unified Request into Anthropic Messages API JSON.
func translateRequest(req *llm.Request) ([]byte, error) {
	body := map[string]any{
		"model": req.Model,
	}

	// Extract system messages
	var systemParts []map[string]any
	var messages []map[string]any

	for _, msg := range req.Messages {
		switch msg.Role {
		case llm.RoleSystem, llm.RoleDeveloper:
			for _, part := range msg.Content {
				if part.Kind == llm.KindText {
					systemParts = append(systemParts, map[string]any{
						"type": "text",
						"text": part.Text,
					})
				}
			}
		case llm.RoleUser:
			messages = append(messages, translateUserMessage(msg))
		case llm.RoleAssistant:
			messages = append(messages, translateAssistantMessage(msg))
		case llm.RoleTool:
			// Tool results go into user messages for Anthropic
			messages = append(messages, translateToolResultMessage(msg))
		}
	}

	// Merge consecutive same-role messages (Anthropic requires alternation)
	messages = mergeConsecutiveRoles(messages)

	if len(systemParts) > 0 {
		body["system"] = systemParts
	}
	body["messages"] = messages

	// max_tokens is required for Anthropic
	if req.MaxTokens != nil {
		body["max_tokens"] = *req.MaxTokens
	} else {
		body["max_tokens"] = 4096
	}

	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if len(req.StopSequences) > 0 {
		body["stop_sequences"] = req.StopSequences
	}

	// Tools
	if len(req.Tools) > 0 {
		body["tools"] = translateTools(req.Tools)
	}

	// Tool choice
	if req.ToolChoice != nil {
		tc := translateToolChoice(*req.ToolChoice, len(req.Tools) > 0)
		if tc != nil {
			body["tool_choice"] = tc
		}
	}

	// Provider options
	if opts, ok := req.ProviderOptions["anthropic"]; ok {
		if optsMap, ok := opts.(map[string]any); ok {
			for k, v := range optsMap {
				if k == "beta_headers" || k == "beta_features" {
					continue // handled at HTTP level
				}
				body[k] = v
			}
		}
	}

	return json.Marshal(body)
}

func translateUserMessage(msg llm.Message) map[string]any {
	content := translateContentParts(msg.Content)
	return map[string]any{
		"role":    "user",
		"content": content,
	}
}

func translateAssistantMessage(msg llm.Message) map[string]any {
	content := translateContentParts(msg.Content)
	return map[string]any{
		"role":    "assistant",
		"content": content,
	}
}

func translateToolResultMessage(msg llm.Message) map[string]any {
	var content []map[string]any
	for _, part := range msg.Content {
		if part.Kind == llm.KindToolResult && part.ToolResult != nil {
			tr := map[string]any{
				"type":        "tool_result",
				"tool_use_id": part.ToolResult.ToolCallID,
				"content":     part.ToolResult.Content,
			}
			if part.ToolResult.IsError {
				tr["is_error"] = true
			}
			content = append(content, tr)
		}
	}
	return map[string]any{
		"role":    "user",
		"content": content,
	}
}

func translateContentParts(parts []llm.ContentPart) []map[string]any {
	var result []map[string]any
	for _, part := range parts {
		switch part.Kind {
		case llm.KindText:
			result = append(result, map[string]any{
				"type": "text",
				"text": part.Text,
			})
		case llm.KindImage:
			if part.Image != nil {
				result = append(result, translateImage(part.Image))
			}
		case llm.KindToolCall:
			if part.ToolCall != nil {
				var input any
				if err := json.Unmarshal(part.ToolCall.Arguments, &input); err != nil {
					input = map[string]any{}
				}
				result = append(result, map[string]any{
					"type":  "tool_use",
					"id":    part.ToolCall.ID,
					"name":  part.ToolCall.Name,
					"input": input,
				})
			}
		case llm.KindThinking:
			if part.Thinking != nil {
				if part.Thinking.Redacted {
					result = append(result, map[string]any{
						"type": "redacted_thinking",
						"data": part.Thinking.Text,
					})
				} else {
					entry := map[string]any{
						"type":     "thinking",
						"thinking": part.Thinking.Text,
					}
					if part.Thinking.Signature != "" {
						entry["signature"] = part.Thinking.Signature
					}
					result = append(result, entry)
				}
			}
		}
	}
	return result
}

func translateImage(img *llm.ImageData) map[string]any {
	if img.URL != "" {
		return map[string]any{
			"type": "image",
			"source": map[string]any{
				"type": "url",
				"url":  img.URL,
			},
		}
	}
	mediaType := img.MediaType
	if mediaType == "" {
		mediaType = "image/png"
	}
	return map[string]any{
		"type": "image",
		"source": map[string]any{
			"type":       "base64",
			"media_type": mediaType,
			"data":       img.Data,
		},
	}
}

func translateTools(tools []llm.ToolDefinition) []map[string]any {
	var result []map[string]any
	for _, tool := range tools {
		var schema any
		if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
			schema = map[string]any{}
		}
		result = append(result, map[string]any{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": schema,
		})
	}
	return result
}

func translateToolChoice(tc llm.ToolChoice, hasTools bool) any {
	switch tc.Mode {
	case "auto":
		return map[string]any{"type": "auto"}
	case "none":
		// Anthropic doesn't support none with tools present; caller should omit tools
		return nil
	case "required":
		return map[string]any{"type": "any"}
	case "named":
		return map[string]any{"type": "tool", "name": tc.ToolName}
	default:
		return map[string]any{"type": "auto"}
	}
}

// mergeConsecutiveRoles merges consecutive messages with the same role.
func mergeConsecutiveRoles(messages []map[string]any) []map[string]any {
	if len(messages) == 0 {
		return messages
	}
	var merged []map[string]any
	current := messages[0]
	for i := 1; i < len(messages); i++ {
		if messages[i]["role"] == current["role"] {
			// Merge content arrays
			currentContent, _ := current["content"].([]map[string]any)
			nextContent, _ := messages[i]["content"].([]map[string]any)
			current["content"] = append(currentContent, nextContent...)
		} else {
			merged = append(merged, current)
			current = messages[i]
		}
	}
	merged = append(merged, current)
	return merged
}

// translateResponse converts Anthropic Messages API JSON to a unified Response.
func translateResponse(raw []byte) (*llm.Response, error) {
	var body struct {
		ID         string `json:"id"`
		Model      string `json:"model"`
		Type       string `json:"type"`
		Role       string `json:"role"`
		Content    []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text,omitempty"`
			ID        string          `json:"id,omitempty"`
			Name      string          `json:"name,omitempty"`
			Input     json.RawMessage `json:"input,omitempty"`
			Thinking  string          `json:"thinking,omitempty"`
			Signature string          `json:"signature,omitempty"`
			Data      string          `json:"data,omitempty"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens            int `json:"input_tokens"`
			OutputTokens           int `json:"output_tokens"`
			CacheReadInputTokens   int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("failed to parse Anthropic response: %w", err)
	}

	var content []llm.ContentPart
	for _, block := range body.Content {
		switch block.Type {
		case "text":
			content = append(content, llm.ContentPart{Kind: llm.KindText, Text: block.Text})
		case "tool_use":
			content = append(content, llm.ContentPart{
				Kind: llm.KindToolCall,
				ToolCall: &llm.ToolCallData{
					ID:        block.ID,
					Name:      block.Name,
					Arguments: block.Input,
				},
			})
		case "thinking":
			content = append(content, llm.ContentPart{
				Kind: llm.KindThinking,
				Thinking: &llm.ThinkingData{
					Text:      block.Thinking,
					Signature: block.Signature,
				},
			})
		case "redacted_thinking":
			content = append(content, llm.ContentPart{
				Kind: llm.KindThinking,
				Thinking: &llm.ThinkingData{
					Text:     block.Data,
					Redacted: true,
				},
			})
		}
	}

	finishReason := translateFinishReason(body.StopReason)

	usage := llm.Usage{
		InputTokens:  body.Usage.InputTokens,
		OutputTokens: body.Usage.OutputTokens,
		TotalTokens:  body.Usage.InputTokens + body.Usage.OutputTokens,
	}
	if body.Usage.CacheReadInputTokens > 0 {
		v := body.Usage.CacheReadInputTokens
		usage.CacheReadTokens = &v
	}
	if body.Usage.CacheCreationInputTokens > 0 {
		v := body.Usage.CacheCreationInputTokens
		usage.CacheWriteTokens = &v
	}

	return &llm.Response{
		ID:       body.ID,
		Model:    body.Model,
		Provider: "anthropic",
		Message: llm.Message{
			Role:    llm.RoleAssistant,
			Content: content,
		},
		FinishReason: finishReason,
		Usage:        usage,
		Raw:          raw,
	}, nil
}

func translateFinishReason(raw string) llm.FinishReason {
	switch raw {
	case "end_turn", "stop_sequence":
		return llm.FinishReason{Reason: "stop", Raw: raw}
	case "max_tokens":
		return llm.FinishReason{Reason: "length", Raw: raw}
	case "tool_use":
		return llm.FinishReason{Reason: "tool_calls", Raw: raw}
	default:
		return llm.FinishReason{Reason: "other", Raw: raw}
	}
}
```

Create `llm/anthropic/adapter.go`:

```go
// ABOUTME: Anthropic Messages API adapter for the unified LLM client.
// ABOUTME: Speaks native Anthropic protocol with SSE streaming and cache_control support.
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/2389-research/mammoth-lite/llm"
)

const (
	defaultBaseURL = "https://api.anthropic.com"
	apiVersion     = "2023-06-01"
)

// Adapter implements llm.ProviderAdapter for Anthropic.
type Adapter struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// Option configures the Anthropic adapter.
type Option func(*Adapter)

// WithBaseURL sets a custom base URL.
func WithBaseURL(url string) Option {
	return func(a *Adapter) { a.baseURL = url }
}

// New creates a new Anthropic adapter.
func New(apiKey string, opts ...Option) *Adapter {
	a := &Adapter{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func (a *Adapter) Name() string { return "anthropic" }

func (a *Adapter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	body, err := translateRequest(req)
	if err != nil {
		return nil, fmt.Errorf("translate request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, &llm.NetworkError{SDKError: llm.SDKError{Msg: "create request", Cause: err}}
	}

	a.setHeaders(httpReq, req)

	httpResp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, &llm.NetworkError{SDKError: llm.SDKError{Msg: "request failed", Cause: err}}
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, &llm.NetworkError{SDKError: llm.SDKError{Msg: "read response", Cause: err}}
	}

	if httpResp.StatusCode != 200 {
		return nil, llm.ErrorFromStatusCode(httpResp.StatusCode, string(respBody), "anthropic")
	}

	return translateResponse(respBody)
}

func (a *Adapter) Stream(ctx context.Context, req *llm.Request) <-chan llm.StreamEvent {
	ch := make(chan llm.StreamEvent, 64)

	go func() {
		defer close(ch)

		body, err := translateRequest(req)
		if err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: err}
			return
		}

		// Add stream flag
		var bodyMap map[string]any
		if err := json.Unmarshal(body, &bodyMap); err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: err}
			return
		}
		bodyMap["stream"] = true
		body, _ = json.Marshal(bodyMap)

		httpReq, err := http.NewRequestWithContext(ctx, "POST", a.baseURL+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: err}
			return
		}
		a.setHeaders(httpReq, req)

		httpResp, err := a.httpClient.Do(httpReq)
		if err != nil {
			ch <- llm.StreamEvent{Type: llm.EventError, Err: err}
			return
		}
		defer httpResp.Body.Close()

		if httpResp.StatusCode != 200 {
			respBody, _ := io.ReadAll(httpResp.Body)
			ch <- llm.StreamEvent{
				Type: llm.EventError,
				Err:  llm.ErrorFromStatusCode(httpResp.StatusCode, string(respBody), "anthropic"),
			}
			return
		}

		a.parseSSE(httpResp.Body, ch)
	}()

	return ch
}

func (a *Adapter) Close() error { return nil }

func (a *Adapter) setHeaders(httpReq *http.Request, req *llm.Request) {
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", apiVersion)

	// Beta headers from provider options
	if opts, ok := req.ProviderOptions["anthropic"]; ok {
		if optsMap, ok := opts.(map[string]any); ok {
			if headers, ok := optsMap["beta_headers"]; ok {
				if headerList, ok := headers.([]any); ok {
					var strs []string
					for _, h := range headerList {
						if s, ok := h.(string); ok {
							strs = append(strs, s)
						}
					}
					if len(strs) > 0 {
						httpReq.Header.Set("anthropic-beta", strings.Join(strs, ","))
					}
				}
			}
		}
	}
}

func (a *Adapter) parseSSE(body io.Reader, ch chan<- llm.StreamEvent) {
	scanner := bufio.NewScanner(body)
	// Increase buffer for large SSE events
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	var eventType string
	var dataLines []string
	acc := llm.NewStreamAccumulator()

	ch <- llm.StreamEvent{Type: llm.EventStreamStart}

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
			continue
		}

		// Blank line = end of event
		if line == "" && len(dataLines) > 0 {
			data := strings.Join(dataLines, "\n")
			dataLines = nil

			events := a.translateSSEEvent(eventType, []byte(data), acc)
			for _, e := range events {
				ch <- e
			}
			eventType = ""
		}
	}
}

func (a *Adapter) translateSSEEvent(eventType string, data []byte, acc *llm.StreamAccumulator) []llm.StreamEvent {
	var events []llm.StreamEvent

	switch eventType {
	case "content_block_start":
		var block struct {
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		if json.Unmarshal(data, &block) == nil {
			switch block.ContentBlock.Type {
			case "text":
				e := llm.StreamEvent{Type: llm.EventTextStart, TextID: "text"}
				acc.Process(e)
				events = append(events, e)
			case "tool_use":
				e := llm.StreamEvent{Type: llm.EventToolCallStart, ToolCall: &llm.ToolCallData{
					ID:   block.ContentBlock.ID,
					Name: block.ContentBlock.Name,
				}}
				events = append(events, e)
			case "thinking":
				e := llm.StreamEvent{Type: llm.EventReasoningStart}
				events = append(events, e)
			}
		}

	case "content_block_delta":
		var delta struct {
			Delta struct {
				Type            string `json:"type"`
				Text            string `json:"text"`
				PartialJSON     string `json:"partial_json"`
				Thinking        string `json:"thinking"`
			} `json:"delta"`
		}
		if json.Unmarshal(data, &delta) == nil {
			switch delta.Delta.Type {
			case "text_delta":
				e := llm.StreamEvent{Type: llm.EventTextDelta, Delta: delta.Delta.Text, TextID: "text"}
				acc.Process(e)
				events = append(events, e)
			case "input_json_delta":
				e := llm.StreamEvent{Type: llm.EventToolCallDelta, Delta: delta.Delta.PartialJSON}
				events = append(events, e)
			case "thinking_delta":
				e := llm.StreamEvent{Type: llm.EventReasoningDelta, ReasoningDelta: delta.Delta.Thinking}
				acc.Process(e)
				events = append(events, e)
			}
		}

	case "content_block_stop":
		// Could be text, tool, or thinking end — we emit generic end events
		// The accumulator handles figuring out what it was

	case "message_delta":
		var delta struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(data, &delta) == nil {
			fr := translateFinishReason(delta.Delta.StopReason)
			e := llm.StreamEvent{
				Type:         llm.EventFinish,
				FinishReason: &fr,
				Usage:        &llm.Usage{OutputTokens: delta.Usage.OutputTokens},
			}
			acc.Process(e)
			events = append(events, e)
		}

	case "message_start":
		var msg struct {
			Message struct {
				Usage struct {
					InputTokens int `json:"input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(data, &msg) == nil {
			// Input tokens come in message_start
			_ = msg.Message.Usage.InputTokens
		}

	case "message_stop":
		// Stream complete — no additional event needed

	case "ping":
		// Keepalive, ignore
	}

	return events
}
```

**Step 4: Run tests**

Run: `go test ./llm/... -v`
Expected: ALL PASS

**Step 5: Commit**

```bash
git add llm/anthropic/
git commit -m "feat: add Anthropic Messages API adapter"
```

---

### Task 9: OpenAI Adapter

**Files:**
- Create: `llm/openai/adapter.go`
- Create: `llm/openai/translate.go`
- Create: `llm/openai/adapter_test.go`

The OpenAI adapter speaks the Responses API (`/v1/responses`). This is a parallel implementation to Anthropic — same adapter interface, different wire protocol.

**This task follows the same TDD pattern as Task 8.** The key differences:
- Uses `/v1/responses` endpoint with `input` array (not `messages`)
- System messages go to `instructions` parameter
- Tool calls are top-level input items, not nested
- Streaming uses `response.output_text.delta` events
- `reasoning.effort` parameter for reasoning models
- Bearer token auth (not x-api-key)

Due to the size of the code (similar to Anthropic adapter), this task will be implemented following the same test → implement → verify → commit cycle.

**Commit message:** `feat: add OpenAI Responses API adapter`

---

### Task 10: Google Gemini Adapter

**Files:**
- Create: `llm/google/adapter.go`
- Create: `llm/google/translate.go`
- Create: `llm/google/adapter_test.go`

The Gemini adapter speaks the native Gemini API (`/v1beta/models/*/generateContent`). Key differences:
- System messages go to `systemInstruction`
- Uses `key` query parameter for auth
- Tool calls have no IDs (adapter generates synthetic UUIDs)
- `functionResponse` uses function name (not call ID)
- Streaming via `?alt=sse` query param
- `model` role instead of `assistant`

**Commit message:** `feat: add Google Gemini API adapter`

---

### Task 11: Integration Tests (Behind Build Tag)

**Files:**
- Create: `llm/integration_test.go`

**Step 1: Write integration tests**

Create `llm/integration_test.go` (behind `//go:build integration` tag) that runs real API calls:

1. Simple text generation across all three providers
2. Streaming text generation
3. Tool calling
4. Error handling (invalid API key)
5. Usage token counts

**Step 2: Run against real APIs**

Run: `ANTHROPIC_API_KEY=... OPENAI_API_KEY=... GEMINI_API_KEY=... go test ./llm/ -tags=integration -v`

**Commit message:** `test: add integration tests for all providers`

---

### Task 12: Pretty-Print Output (Stringer)

**Files:**
- Create: `llm/format.go`
- Create: `llm/format_test.go`

Implement `fmt.Stringer` on `Response`, `Usage`, and `SessionResult` (when agent layer exists):

```
[anthropic/claude-opus-4-6] 245 tokens in, 89 out (cache: 200 read)
Cost: $0.003 | Latency: 1.2s | Finish: stop
```

**Commit message:** `feat: add pretty-print formatting for Response and Usage`

---

## Summary

| Task | Component | Est. Complexity |
|------|-----------|----------------|
| 1 | Core types | Small |
| 2 | Stream events | Small |
| 3 | Error types | Small |
| 4 | Provider interface | Small |
| 5 | Core client | Medium |
| 6 | Middleware | Small |
| 7 | Model catalog | Small |
| 8 | Anthropic adapter | Large |
| 9 | OpenAI adapter | Large |
| 10 | Gemini adapter | Large |
| 11 | Integration tests | Medium |
| 12 | Pretty-print output | Small |

Tasks 1-7 are foundations. Tasks 8-10 are the provider adapters (bulk of the work). Tasks 11-12 are validation and polish.

After Layer 1 is complete, we proceed to Layer 2 (Coding Agent Loop) and Layer 3 (Attractor Pipeline Engine) as separate implementation plans.
