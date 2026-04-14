// ABOUTME: Request/response translation between the unified llm types and OpenAI Responses API format.
// ABOUTME: Handles instructions extraction, flat input array mapping, tool definitions, and finish reasons.
package openai

import (
	"encoding/json"
	"strings"

	"github.com/2389-research/tracker/llm"
)

// defaultMaxOutputTokens is the default max_output_tokens value when not specified.
const defaultMaxOutputTokens = 16384

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

	// Extract system/developer messages into instructions; keep the rest.
	or.Instructions, or.Input = extractInstructionsAndInput(req.Messages)

	// Translate tool definitions.
	or.Tools = translateToolDefs(req.Tools)

	// Translate tool choice.
	if req.ToolChoice != nil {
		or.ToolChoice = translateToolChoice(req.ToolChoice)
	}

	// Translate response format.
	or.Text = translateResponseFormat(req.ResponseFormat)

	// Reasoning effort.
	or.Reasoning = translateReasoningEffort(req)

	body, err := json.Marshal(or)
	if err != nil {
		return nil, err
	}

	return mergeProviderOptions(body, req.ProviderOptions, "openai", []string{"reasoning_effort"})
}

// extractInstructionsAndInput separates system/developer messages into an
// instructions string and converts remaining messages to flat input items.
func extractInstructionsAndInput(messages []llm.Message) (string, []openaiInput) {
	var instructions []string
	var input []openaiInput
	for _, m := range messages {
		if m.Role == llm.RoleSystem || m.Role == llm.RoleDeveloper {
			instructions = append(instructions, extractTextParts(m)...)
		} else {
			input = append(input, translateMessageToInput(m)...)
		}
	}
	return strings.Join(instructions, "\n"), input
}

// extractTextParts returns all KindText content strings from a message.
func extractTextParts(m llm.Message) []string {
	var parts []string
	for _, part := range m.Content {
		if part.Kind == llm.KindText {
			parts = append(parts, part.Text)
		}
	}
	return parts
}

// translateToolDefs converts unified tool definitions to OpenAI format.
func translateToolDefs(tools []llm.ToolDefinition) []openaiTool {
	var out []openaiTool
	for _, t := range tools {
		out = append(out, openaiTool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		})
	}
	return out
}

// translateResponseFormat converts unified ResponseFormat to OpenAI text format.
func translateResponseFormat(rf *llm.ResponseFormat) *openaiText {
	if rf == nil {
		return nil
	}
	switch rf.Type {
	case "json_object":
		return &openaiText{Format: &openaiTextFormat{Type: "json_object"}}
	case "json_schema":
		f := &openaiTextFormat{
			Type:   "json_schema",
			Name:   "response",
			Schema: rf.JSONSchema,
		}
		if rf.Strict {
			strict := true
			f.Strict = &strict
		}
		return &openaiText{Format: f}
	default:
		return nil
	}
}

// translateReasoningEffort extracts reasoning effort from request or provider options.
func translateReasoningEffort(req *llm.Request) *openaiReason {
	effort := req.ReasoningEffort
	if effort == "" {
		effort = reasoningEffortFromProviderOptions(req.ProviderOptions)
	}
	if effort == "" {
		return nil
	}
	return &openaiReason{Effort: effort}
}

// reasoningEffortFromProviderOptions extracts the reasoning_effort string from
// the openai provider options map, returning empty string if absent.
func reasoningEffortFromProviderOptions(providerOpts map[string]any) string {
	opts, ok := providerOpts["openai"]
	if !ok {
		return ""
	}
	optsMap, ok := opts.(map[string]any)
	if !ok {
		return ""
	}
	e, _ := optsMap["reasoning_effort"].(string)
	return e
}

// mergeProviderOptions merges provider-specific options into the JSON body,
// skipping any keys in the reserved list.
func mergeProviderOptions(body []byte, providerOpts map[string]any, providerKey string, reserved []string) ([]byte, error) {
	opts, ok := providerOpts[providerKey]
	if !ok {
		return body, nil
	}
	optsMap, ok := opts.(map[string]any)
	if !ok {
		return body, nil
	}
	skipSet := make(map[string]bool, len(reserved))
	for _, k := range reserved {
		skipSet[k] = true
	}
	var bodyMap map[string]any
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		return nil, err
	}
	for k, v := range optsMap {
		if skipSet[k] {
			continue
		}
		bodyMap[k] = v
	}
	return json.Marshal(bodyMap)
}

// translateMessageToInput converts a single llm.Message to one or more flat input items.
func translateMessageToInput(m llm.Message) []openaiInput {
	switch m.Role {
	case llm.RoleUser:
		return translateUserInput(m.Content)
	case llm.RoleAssistant:
		return translateAssistantInput(m.Content)
	case llm.RoleTool:
		return translateToolInput(m.Content)
	}
	return nil
}

// translateUserInput collects user text parts into a single input item.
func translateUserInput(content []llm.ContentPart) []openaiInput {
	var textParts []string
	for _, part := range content {
		if part.Kind == llm.KindText {
			textParts = append(textParts, part.Text)
		}
	}
	if len(textParts) == 0 {
		return nil
	}
	return []openaiInput{{Role: "user", Content: strings.Join(textParts, "")}}
}

// translateAssistantInput emits text and tool call items for assistant messages.
func translateAssistantInput(content []llm.ContentPart) []openaiInput {
	var items []openaiInput
	var textParts []string
	for _, part := range content {
		switch part.Kind {
		case llm.KindText:
			textParts = append(textParts, part.Text)
		case llm.KindToolCall:
			if part.ToolCall != nil {
				items = append(items, openaiInput{
					Type:      "function_call",
					CallID:    part.ToolCall.ID,
					Name:      part.ToolCall.Name,
					Arguments: string(part.ToolCall.Arguments),
				})
			}
		}
	}
	if len(textParts) > 0 {
		textItem := openaiInput{Role: "assistant", Content: strings.Join(textParts, "")}
		items = append([]openaiInput{textItem}, items...)
	}
	return items
}

// translateToolInput converts tool result parts into function_call_output items.
func translateToolInput(content []llm.ContentPart) []openaiInput {
	var items []openaiInput
	for _, part := range content {
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
	CallID    string `json:"call_id,omitempty"`
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

	content, hasFunctionCalls := translateOutputItems(or.Output)
	usage := translateUsage(or.Usage)
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

// translateOutputItems converts OpenAI output items to unified content parts.
func translateOutputItems(items []openaiOutputItem) ([]llm.ContentPart, bool) {
	var content []llm.ContentPart
	hasFunctionCalls := false

	for _, item := range items {
		switch item.Type {
		case "message":
			content = append(content, translateMessageBlocks(item.Content)...)
		case "function_call":
			hasFunctionCalls = true
			content = append(content, translateFunctionCallItem(item))
		case "reasoning":
			if part, ok := translateReasoningItem(item.Summary); ok {
				content = append(content, part)
			}
		}
	}
	return content, hasFunctionCalls
}

func translateMessageBlocks(blocks []openaiContentBlock) []llm.ContentPart {
	var parts []llm.ContentPart
	for _, block := range blocks {
		if block.Type == "output_text" {
			parts = append(parts, llm.ContentPart{Kind: llm.KindText, Text: block.Text})
		}
	}
	return parts
}

func translateFunctionCallItem(item openaiOutputItem) llm.ContentPart {
	callID := item.CallID
	if callID == "" {
		callID = item.ID
	}
	return llm.ContentPart{
		Kind: llm.KindToolCall,
		ToolCall: &llm.ToolCallData{
			ID:        callID,
			Name:      item.Name,
			Arguments: json.RawMessage(item.Arguments),
		},
	}
}

func translateReasoningItem(summary []openaiSummaryBlock) (llm.ContentPart, bool) {
	var texts []string
	for _, s := range summary {
		if s.Type == "summary_text" {
			texts = append(texts, s.Text)
		}
	}
	if len(texts) == 0 {
		return llm.ContentPart{}, false
	}
	return llm.ContentPart{
		Kind:     llm.KindThinking,
		Thinking: &llm.ThinkingData{Text: strings.Join(texts, "")},
	}, true
}

// translateUsage converts OpenAI usage to unified format.
func translateUsage(u openaiUsage) llm.Usage {
	usage := llm.Usage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		TotalTokens:  u.TotalTokens,
	}
	if u.OutputDetail != nil && u.OutputDetail.ReasoningTokens > 0 {
		v := u.OutputDetail.ReasoningTokens
		usage.ReasoningTokens = &v
	}
	return usage
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
