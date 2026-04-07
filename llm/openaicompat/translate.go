// ABOUTME: Request/response translation between the unified llm types and OpenAI Chat Completions API format.
// ABOUTME: Handles messages array (system stays inline), nested tool_calls, and function-wrapped tool definitions.
package openaicompat

import (
	"encoding/json"
	"strings"

	"github.com/2389-research/tracker/llm"
)

// defaultMaxTokens is the default max_tokens value when not specified.
const defaultMaxTokens = 16384

// --- Wire format types for the OpenAI Chat Completions API ---

// chatRequest is the wire format for POST /v1/chat/completions.
type chatRequest struct {
	Model          string              `json:"model"`
	Messages       []chatMessage       `json:"messages"`
	Tools          []chatTool          `json:"tools,omitempty"`
	ToolChoice     any                 `json:"tool_choice,omitempty"`
	MaxTokens      int                 `json:"max_tokens,omitempty"`
	Temperature    *float64            `json:"temperature,omitempty"`
	TopP           *float64            `json:"top_p,omitempty"`
	Stop           []string            `json:"stop,omitempty"`
	ResponseFormat *chatResponseFormat `json:"response_format,omitempty"`
	Stream         bool                `json:"stream,omitempty"`
	StreamOptions  *chatStreamOptions  `json:"stream_options,omitempty"`
}

// chatStreamOptions configures streaming behavior.
type chatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// chatMessage represents a single message in the Chat Completions format.
type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// chatToolCall represents a tool invocation nested in an assistant message.
type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatFunctionCall `json:"function"`
}

// chatFunctionCall holds the function name and arguments for a tool call.
type chatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// chatTool wraps a function definition in the Chat Completions tool format.
type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

// chatFunction defines a callable function for the model.
type chatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// chatResponseFormat specifies output format constraints.
type chatResponseFormat struct {
	Type string `json:"type"`
}

// chatResponse is the wire format for a Chat Completions API response.
type chatResponse struct {
	ID      string       `json:"id"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

// chatChoice represents a single completion choice.
type chatChoice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// chatUsage tracks token consumption in Chat Completions format.
type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// --- Request translation ---

// translateRequest converts a unified llm.Request to Chat Completions API JSON.
// When stream is true, sets stream:true and requests usage in the final chunk.
func translateRequest(req *llm.Request, stream bool) ([]byte, error) {
	cr := chatRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.StopSequences,
	}

	// max_tokens
	if req.MaxTokens != nil {
		cr.MaxTokens = *req.MaxTokens
	} else {
		cr.MaxTokens = defaultMaxTokens
	}

	// Streaming: enable stream and request usage in the final chunk.
	if stream {
		cr.Stream = true
		cr.StreamOptions = &chatStreamOptions{IncludeUsage: true}
	}

	// Messages — system messages stay in the array (Chat Completions format)
	cr.Messages = translateMessages(req.Messages)

	// Tool definitions with function wrapping
	cr.Tools = translateToolDefs(req.Tools)

	// Tool choice
	if req.ToolChoice != nil {
		cr.ToolChoice = translateToolChoice(req.ToolChoice)
	}

	// Response format
	cr.ResponseFormat = translateResponseFormat(req.ResponseFormat)

	return json.Marshal(cr)
}

// translateMessages converts all messages including system to Chat Completions format.
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

// translateSystemMessage converts a system/developer message.
func translateSystemMessage(m llm.Message) chatMessage {
	var parts []string
	for _, c := range m.Content {
		if c.Kind == llm.KindText {
			parts = append(parts, c.Text)
		}
	}
	return chatMessage{Role: "system", Content: strings.Join(parts, "")}
}

// translateUserMessage converts a user message.
func translateUserMessage(m llm.Message) chatMessage {
	var parts []string
	for _, c := range m.Content {
		if c.Kind == llm.KindText {
			parts = append(parts, c.Text)
		}
	}
	return chatMessage{Role: "user", Content: strings.Join(parts, "")}
}

// translateAssistantMessage converts an assistant message with optional nested tool_calls.
func translateAssistantMessage(m llm.Message) chatMessage {
	var textParts []string
	var toolCalls []chatToolCall

	for _, c := range m.Content {
		switch c.Kind {
		case llm.KindText:
			textParts = append(textParts, c.Text)
		case llm.KindToolCall:
			if c.ToolCall != nil {
				toolCalls = append(toolCalls, chatToolCall{
					ID:   c.ToolCall.ID,
					Type: "function",
					Function: chatFunctionCall{
						Name:      c.ToolCall.Name,
						Arguments: string(c.ToolCall.Arguments),
					},
				})
			}
		}
	}

	msg := chatMessage{Role: "assistant"}
	if len(textParts) > 0 {
		msg.Content = strings.Join(textParts, "")
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}
	return msg
}

// translateToolResultMessages converts a tool result message to one or more tool messages.
// Each tool result content part becomes a separate message with role "tool".
func translateToolResultMessages(m llm.Message) []chatMessage {
	var out []chatMessage
	for _, c := range m.Content {
		if c.Kind == llm.KindToolResult && c.ToolResult != nil {
			callID := c.ToolResult.ToolCallID
			if callID == "" {
				callID = m.ToolCallID
			}
			if callID == "" {
				callID = "call_unknown"
			}
			out = append(out, chatMessage{
				Role:       "tool",
				Content:    c.ToolResult.Content,
				ToolCallID: callID,
			})
		}
	}
	return out
}

// translateToolDefs converts unified tool definitions to Chat Completions format.
// Chat Completions wraps in {type:"function", function:{...}} (extra nesting vs Responses API).
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

// translateToolChoice converts llm.ToolChoice to Chat Completions format.
func translateToolChoice(tc *llm.ToolChoice) any {
	switch tc.Mode {
	case "auto":
		return "auto"
	case "none":
		return "none"
	case "required":
		return "required"
	case "named":
		return map[string]any{
			"type":     "function",
			"function": map[string]string{"name": tc.ToolName},
		}
	default:
		return tc.Mode
	}
}

// translateResponseFormat converts unified ResponseFormat to Chat Completions format.
// json_object passes through; json_schema is silently dropped (not supported by compat endpoints).
func translateResponseFormat(rf *llm.ResponseFormat) *chatResponseFormat {
	if rf == nil {
		return nil
	}
	switch rf.Type {
	case "json_object":
		return &chatResponseFormat{Type: "json_object"}
	default:
		// json_schema and other types are silently dropped
		return nil
	}
}

// --- Response translation ---

// translateResponse parses Chat Completions API JSON into a unified llm.Response.
func translateResponse(raw []byte) (*llm.Response, error) {
	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return nil, err
	}

	resp := &llm.Response{
		ID:    cr.ID,
		Model: cr.Model,
		Message: llm.Message{
			Role: llm.RoleAssistant,
		},
		Usage: translateUsage(cr.Usage),
		Raw:   raw,
	}

	if len(cr.Choices) > 0 {
		choice := cr.Choices[0]
		resp.Message.Content = translateChoiceMessage(choice.Message)
		resp.FinishReason = translateFinishReason(choice.FinishReason, len(choice.Message.ToolCalls) > 0)
	} else {
		// Empty choices: stop with empty content
		resp.FinishReason = llm.FinishReason{Reason: "stop", Raw: ""}
	}

	return resp, nil
}

// translateChoiceMessage converts a Chat Completions message to unified content parts.
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

// translateUsage maps Chat Completions usage to unified format.
func translateUsage(u chatUsage) llm.Usage {
	return llm.Usage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
	}
}

// translateFinishReason maps Chat Completions finish_reason to unified format.
func translateFinishReason(reason string, hasToolCalls bool) llm.FinishReason {
	if hasToolCalls {
		return llm.FinishReason{Reason: "tool_calls", Raw: reason}
	}

	var mapped string
	switch reason {
	case "stop":
		mapped = "stop"
	case "tool_calls":
		mapped = "tool_calls"
	case "length":
		mapped = "length"
	case "content_filter":
		mapped = "content_filter"
	default:
		mapped = reason
	}

	return llm.FinishReason{Reason: mapped, Raw: reason}
}
