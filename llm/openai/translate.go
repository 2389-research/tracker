// ABOUTME: Request/response translation between the unified llm types and OpenAI Responses API format.
// ABOUTME: Handles instructions extraction, flat input array mapping, tool definitions, and finish reasons.
package openai

import (
	"encoding/json"
	"strings"

	"github.com/2389-research/tracker/llm"
)

// defaultMaxOutputTokens is the default max_output_tokens value when not specified.
const defaultMaxOutputTokens = 4096

// --- Wire format types for the OpenAI Responses API ---

// openaiRequest is the wire format for POST /v1/responses.
type openaiRequest struct {
	Model           string        `json:"model"`
	Instructions    string        `json:"instructions,omitempty"`
	Input           []openaiInput `json:"input"`
	Tools           []openaiTool  `json:"tools,omitempty"`
	ToolChoice      any           `json:"tool_choice,omitempty"`
	Text            *openaiText   `json:"text,omitempty"`
	Temperature     *float64      `json:"temperature,omitempty"`
	TopP            *float64      `json:"top_p,omitempty"`
	MaxOutputTokens *int          `json:"max_output_tokens,omitempty"`
	Stop            []string      `json:"stop,omitempty"`
	Reasoning       *openaiReason `json:"reasoning,omitempty"`
	Stream          bool          `json:"stream,omitempty"`
}

// openaiText holds the text output configuration including response format.
type openaiText struct {
	Format *openaiTextFormat `json:"format,omitempty"`
}

// openaiTextFormat specifies the response format constraint for text output.
type openaiTextFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name,omitempty"`
	Schema json.RawMessage `json:"schema,omitempty"`
	Strict *bool           `json:"strict,omitempty"`
}

// openaiInput represents a single item in the flat input array.
type openaiInput struct {
	// Common fields
	Role string `json:"role,omitempty"`
	// For user/assistant messages
	Content string `json:"content,omitempty"`
	// For function_call items (tool invocations from the model)
	Type      string `json:"type,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	// For function_call_output items (tool results)
	CallID string `json:"call_id,omitempty"`
	Output string `json:"output,omitempty"`
}

type openaiTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openaiReason struct {
	Effort string `json:"effort"`
}

// translateRequest converts a unified llm.Request to OpenAI Responses API JSON.
func translateRequest(req *llm.Request) ([]byte, error) {
	or := openaiRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.StopSequences,
	}

	// max_output_tokens
	if req.MaxTokens != nil {
		or.MaxOutputTokens = req.MaxTokens
	} else {
		v := defaultMaxOutputTokens
		or.MaxOutputTokens = &v
	}

	// Extract system/developer messages into the instructions string.
	var instructions []string
	var msgs []llm.Message
	for _, m := range req.Messages {
		if m.Role == llm.RoleSystem || m.Role == llm.RoleDeveloper {
			for _, part := range m.Content {
				if part.Kind == llm.KindText {
					instructions = append(instructions, part.Text)
				}
			}
		} else {
			msgs = append(msgs, m)
		}
	}
	if len(instructions) > 0 {
		or.Instructions = strings.Join(instructions, "\n")
	}

	// Build flat input array from remaining messages.
	for _, m := range msgs {
		items := translateMessageToInput(m)
		or.Input = append(or.Input, items...)
	}

	// Translate tool definitions.
	for _, t := range req.Tools {
		or.Tools = append(or.Tools, openaiTool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		})
	}

	// Translate tool choice.
	if req.ToolChoice != nil {
		or.ToolChoice = translateToolChoice(req.ToolChoice)
	}

	// Translate response format to text.format configuration.
	if req.ResponseFormat != nil {
		switch req.ResponseFormat.Type {
		case "json_object":
			or.Text = &openaiText{
				Format: &openaiTextFormat{Type: "json_object"},
			}
		case "json_schema":
			f := &openaiTextFormat{
				Type:   "json_schema",
				Name:   "response",
				Schema: req.ResponseFormat.JSONSchema,
			}
			if req.ResponseFormat.Strict {
				strict := true
				f.Strict = &strict
			}
			or.Text = &openaiText{Format: f}
		}
	}

	// Reasoning effort from request field or provider options.
	effort := req.ReasoningEffort
	if effort == "" {
		if opts, ok := req.ProviderOptions["openai"]; ok {
			if optsMap, ok := opts.(map[string]any); ok {
				if e, ok := optsMap["reasoning_effort"].(string); ok {
					effort = e
				}
			}
		}
	}
	if effort != "" {
		or.Reasoning = &openaiReason{Effort: effort}
	}

	body, err := json.Marshal(or)
	if err != nil {
		return nil, err
	}

	// Merge provider_options["openai"] into the body (except reserved keys).
	if opts, ok := req.ProviderOptions["openai"]; ok {
		if optsMap, ok := opts.(map[string]any); ok {
			var bodyMap map[string]any
			if err := json.Unmarshal(body, &bodyMap); err != nil {
				return nil, err
			}
			for k, v := range optsMap {
				if k == "reasoning_effort" {
					continue
				}
				bodyMap[k] = v
			}
			body, err = json.Marshal(bodyMap)
			if err != nil {
				return nil, err
			}
		}
	}

	return body, nil
}

// translateMessageToInput converts a single llm.Message to one or more flat input items.
func translateMessageToInput(m llm.Message) []openaiInput {
	var items []openaiInput

	switch m.Role {
	case llm.RoleUser, llm.RoleAssistant:
		// Collect text parts into a single content string per message.
		// Tool calls from assistant messages become separate function_call items.
		var textParts []string
		for _, part := range m.Content {
			switch part.Kind {
			case llm.KindText:
				textParts = append(textParts, part.Text)
			case llm.KindToolCall:
				if part.ToolCall != nil {
					items = append(items, openaiInput{
						Type:      "function_call",
						ID:        part.ToolCall.ID,
						Name:      part.ToolCall.Name,
						Arguments: string(part.ToolCall.Arguments),
					})
				}
			}
		}
		if len(textParts) > 0 {
			// Text items come before function_call items in the input.
			textItem := openaiInput{
				Role:    string(m.Role),
				Content: strings.Join(textParts, ""),
			}
			items = append([]openaiInput{textItem}, items...)
		}

	case llm.RoleTool:
		// Tool result messages become function_call_output items.
		for _, part := range m.Content {
			if part.Kind == llm.KindToolResult && part.ToolResult != nil {
				callID := part.ToolResult.ToolCallID
				if callID == "" {
					callID = "call_unknown"
				}
				items = append(items, openaiInput{
					Type:   "function_call_output",
					CallID: callID,
					Output: part.ToolResult.Content,
				})
			}
		}
	}

	return items
}

// translateToolChoice converts llm.ToolChoice to OpenAI Responses API format.
func translateToolChoice(tc *llm.ToolChoice) any {
	switch tc.Mode {
	case "auto":
		return "auto"
	case "none":
		return "none"
	case "required":
		return "required"
	case "named":
		return map[string]string{"type": "function", "name": tc.ToolName}
	default:
		return tc.Mode
	}
}

// --- Response translation ---

// openaiResponse is the wire format for an OpenAI Responses API response.
type openaiResponse struct {
	ID                string             `json:"id"`
	Object            string             `json:"object"`
	Model             string             `json:"model"`
	Output            []openaiOutputItem `json:"output"`
	Usage             openaiUsage        `json:"usage"`
	Status            string             `json:"status"`
	IncompleteDetails *incompleteDetails `json:"incomplete_details,omitempty"`
}

type openaiOutputItem struct {
	Type    string               `json:"type"`
	Role    string               `json:"role,omitempty"`
	Content []openaiContentBlock `json:"content,omitempty"`
	// function_call fields
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	// reasoning fields
	Summary []openaiSummaryBlock `json:"summary,omitempty"`
}

type openaiContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type openaiSummaryBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type openaiUsage struct {
	InputTokens  int              `json:"input_tokens"`
	OutputTokens int              `json:"output_tokens"`
	TotalTokens  int              `json:"total_tokens"`
	OutputDetail *openaiOutDetail `json:"output_tokens_details,omitempty"`
}

type openaiOutDetail struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

type incompleteDetails struct {
	Reason string `json:"reason"`
}

// translateResponse converts OpenAI Responses API JSON to a unified llm.Response.
func translateResponse(raw []byte) (*llm.Response, error) {
	var or openaiResponse
	if err := json.Unmarshal(raw, &or); err != nil {
		return nil, err
	}

	var content []llm.ContentPart
	hasFunctionCalls := false

	for _, item := range or.Output {
		switch item.Type {
		case "message":
			for _, block := range item.Content {
				if block.Type == "output_text" {
					content = append(content, llm.ContentPart{
						Kind: llm.KindText,
						Text: block.Text,
					})
				}
			}
		case "function_call":
			hasFunctionCalls = true
			content = append(content, llm.ContentPart{
				Kind: llm.KindToolCall,
				ToolCall: &llm.ToolCallData{
					ID:        item.ID,
					Name:      item.Name,
					Arguments: json.RawMessage(item.Arguments),
				},
			})
		case "reasoning":
			var reasoningText []string
			for _, s := range item.Summary {
				if s.Type == "summary_text" {
					reasoningText = append(reasoningText, s.Text)
				}
			}
			if len(reasoningText) > 0 {
				content = append(content, llm.ContentPart{
					Kind: llm.KindThinking,
					Thinking: &llm.ThinkingData{
						Text: strings.Join(reasoningText, ""),
					},
				})
			}
		}
	}

	usage := llm.Usage{
		InputTokens:  or.Usage.InputTokens,
		OutputTokens: or.Usage.OutputTokens,
		TotalTokens:  or.Usage.TotalTokens,
	}
	if or.Usage.OutputDetail != nil && or.Usage.OutputDetail.ReasoningTokens > 0 {
		v := or.Usage.OutputDetail.ReasoningTokens
		usage.ReasoningTokens = &v
	}

	fr := translateFinishReason(or.Status, hasFunctionCalls, or.IncompleteDetails)

	return &llm.Response{
		ID:    or.ID,
		Model: or.Model,
		Message: llm.Message{
			Role:    llm.RoleAssistant,
			Content: content,
		},
		FinishReason: fr,
		Usage:        usage,
		Raw:          raw,
	}, nil
}

// translateFinishReason maps OpenAI Responses API status to the unified finish reason format.
func translateFinishReason(status string, hasFunctionCalls bool, incomplete *incompleteDetails) llm.FinishReason {
	if hasFunctionCalls {
		return llm.FinishReason{Reason: "tool_calls", Raw: status}
	}

	var reason string
	switch status {
	case "completed":
		reason = "stop"
	case "incomplete":
		reason = "length"
		if incomplete != nil && incomplete.Reason != "max_output_tokens" {
			reason = incomplete.Reason
		}
	case "failed":
		reason = "error"
	default:
		reason = status
	}

	return llm.FinishReason{Reason: reason, Raw: status}
}
