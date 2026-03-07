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
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Arguments      json.RawMessage `json:"arguments"`
	ThoughtSigData string          `json:"thought_signature,omitempty"`
}

// ToolResultData represents the result of executing a tool call.
type ToolResultData struct {
	ToolCallID     string `json:"tool_call_id"`
	Name           string `json:"name,omitempty"`
	Content        string `json:"content"`
	IsError        bool   `json:"is_error"`
	ImageData      []byte `json:"image_data,omitempty"`
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
	Kind       ContentKind     `json:"kind"`
	Text       string          `json:"text,omitempty"`
	Image      *ImageData      `json:"image,omitempty"`
	Audio      *AudioData      `json:"audio,omitempty"`
	Document   *DocumentData   `json:"document,omitempty"`
	ToolCall   *ToolCallData   `json:"tool_call,omitempty"`
	ToolResult *ToolResultData `json:"tool_result,omitempty"`
	Thinking   *ThinkingData   `json:"thinking,omitempty"`
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

// SystemMessage creates a message with the system role.
func SystemMessage(text string) Message {
	return Message{Role: RoleSystem, Content: []ContentPart{{Kind: KindText, Text: text}}}
}

// UserMessage creates a message with the user role.
func UserMessage(text string) Message {
	return Message{Role: RoleUser, Content: []ContentPart{{Kind: KindText, Text: text}}}
}

// AssistantMessage creates a message with the assistant role.
func AssistantMessage(text string) Message {
	return Message{Role: RoleAssistant, Content: []ContentPart{{Kind: KindText, Text: text}}}
}

// ToolResultMessage creates a tool result message.
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

// ToolChoiceAuto returns a ToolChoice that lets the model decide whether to call tools.
func ToolChoiceAuto() ToolChoice { return ToolChoice{Mode: "auto"} }

// ToolChoiceNone returns a ToolChoice that prevents the model from calling tools.
func ToolChoiceNone() ToolChoice { return ToolChoice{Mode: "none"} }

// ToolChoiceRequired returns a ToolChoice that forces the model to call a tool.
func ToolChoiceRequired() ToolChoice { return ToolChoice{Mode: "required"} }

// ToolChoiceNamed creates a ToolChoice that forces the model to call a specific tool.
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
	TraceObservers  []TraceObserver   `json:"-"`
}

// FinishReason indicates why generation stopped.
type FinishReason struct {
	Reason string `json:"reason"`
	Raw    string `json:"raw,omitempty"`
}

// Usage tracks token consumption.
type Usage struct {
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	ReasoningTokens  *int    `json:"reasoning_tokens,omitempty"`
	CacheReadTokens  *int    `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int    `json:"cache_write_tokens,omitempty"`
	EstimatedCost    float64 `json:"estimated_cost,omitempty"`
	Raw              any     `json:"raw,omitempty"`
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

// addOptionalInt adds two optional int pointers, returning nil if both are nil.
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
