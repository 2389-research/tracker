// ABOUTME: Request/response translation between the unified llm types and Anthropic Messages API format.
// ABOUTME: Handles system extraction, message alternation merging, content block mapping, and finish reasons.
package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/2389-research/tracker/llm"
)

// promptCachingBeta is the beta header value for prompt caching support.
const promptCachingBeta = "prompt-caching-2024-07-31"

// defaultMaxTokens is the default max_tokens value when not specified.
// Anthropic requires max_tokens in every request.
const defaultMaxTokens = 16384

// anthropicRequest is the wire format for the Anthropic Messages API.
type anthropicRequest struct {
	Model       string             `json:"model"`
	Messages    []anthropicMessage `json:"messages"`
	MaxTokens   int                `json:"max_tokens"`
	System      []anthropicContent `json:"system,omitempty"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
	ToolChoice  any                `json:"tool_choice,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
	Temperature *float64           `json:"temperature,omitempty"`
	TopP        *float64           `json:"top_p,omitempty"`
	StopSeqs    []string           `json:"stop_sequences,omitempty"`
}

type anthropicMessage struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
}

// cacheControl is the Anthropic cache_control annotation.
type cacheControl struct {
	Type string `json:"type"`
}

type anthropicContent struct {
	Type string `json:"type"`

	// text block fields
	Text string `json:"text,omitempty"`

	// image block fields
	Source *anthropicSource `json:"source,omitempty"`

	// tool_use block fields
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result block fields
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`

	// thinking block fields
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`

	// redacted_thinking block fields
	Data string `json:"data,omitempty"`

	// cache control annotation
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type anthropicSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type anthropicTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
}

// translateRequest converts a unified llm.Request to Anthropic Messages API JSON.
func translateRequest(req *llm.Request) ([]byte, error) {
	ar := anthropicRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		StopSeqs:    req.StopSequences,
	}

	// Default max_tokens
	if req.MaxTokens != nil {
		ar.MaxTokens = *req.MaxTokens
	} else {
		ar.MaxTokens = defaultMaxTokens
	}

	// Extract system/developer messages to top-level system field.
	ar.System, ar.Messages = extractSystemAndMessages(req.Messages)

	// Append JSON response format instruction to system content.
	ar.System = appendResponseFormatInstruction(ar.System, req.ResponseFormat)

	// Translate tools. Skip when tool choice mode is "none" since Anthropic
	// rejects requests that include a tools array with tool_choice "none".
	ar.Tools = translateAnthropicTools(req.Tools, req.ToolChoice)

	// Translate tool choice.
	if req.ToolChoice != nil {
		ar.ToolChoice = translateToolChoice(req.ToolChoice)
	}

	// Inject cache_control annotations when auto_cache is enabled (default true).
	if autoCacheEnabled(req) {
		injectCacheControl(&ar)
	}

	body, err := json.Marshal(ar)
	if err != nil {
		return nil, err
	}

	return mergeAnthropicProviderOptions(body, req.ProviderOptions)
}

// extractSystemAndMessages separates system/developer messages into anthropic
// system content blocks and converts the rest to merged anthropic messages.
func extractSystemAndMessages(messages []llm.Message) ([]anthropicContent, []anthropicMessage) {
	var system []anthropicContent
	var converted []anthropicMessage
	for _, m := range messages {
		if m.Role == llm.RoleSystem || m.Role == llm.RoleDeveloper {
			for _, part := range m.Content {
				if part.Kind == llm.KindText {
					system = append(system, anthropicContent{Type: "text", Text: part.Text})
				}
			}
		} else {
			converted = append(converted, translateMessage(m))
		}
	}
	return system, mergeConsecutiveMessages(converted)
}

// appendResponseFormatInstruction appends JSON format instructions to system content.
func appendResponseFormatInstruction(system []anthropicContent, rf *llm.ResponseFormat) []anthropicContent {
	if rf == nil {
		return system
	}
	switch rf.Type {
	case "json_object":
		return append(system, anthropicContent{Type: "text", Text: "Respond with valid JSON."})
	case "json_schema":
		instruction := "Respond with valid JSON conforming to this schema: " + string(rf.JSONSchema)
		return append(system, anthropicContent{Type: "text", Text: instruction})
	default:
		return system
	}
}

// translateAnthropicTools converts unified tool definitions to Anthropic format.
// Returns nil when tool choice mode is "none".
func translateAnthropicTools(tools []llm.ToolDefinition, tc *llm.ToolChoice) []anthropicTool {
	if tc != nil && tc.Mode == "none" {
		return nil
	}
	var out []anthropicTool
	for _, t := range tools {
		out = append(out, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}
	return out
}

// mergeAnthropicProviderOptions merges anthropic-specific provider options into the JSON body.
func mergeAnthropicProviderOptions(body []byte, providerOpts map[string]any) ([]byte, error) {
	opts, ok := providerOpts["anthropic"]
	if !ok {
		return body, nil
	}
	optsMap, ok := opts.(map[string]any)
	if !ok {
		return body, nil
	}
	var bodyMap map[string]any
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		return nil, err
	}
	for k, v := range optsMap {
		if k == "beta_headers" || k == "auto_cache" {
			continue
		}
		bodyMap[k] = v
	}
	return json.Marshal(bodyMap)
}

// autoCacheEnabled returns true unless the request explicitly opts out of
// automatic cache control injection via provider_options["anthropic"]["auto_cache"] = false.
func autoCacheEnabled(req *llm.Request) bool {
	if req.ProviderOptions == nil {
		return true
	}
	opts, ok := req.ProviderOptions["anthropic"]
	if !ok {
		return true
	}
	optsMap, ok := opts.(map[string]any)
	if !ok {
		return true
	}
	if v, ok := optsMap["auto_cache"]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return true
}

// injectCacheControl adds ephemeral cache_control annotations to:
//   - the last system content block
//   - the last tool definition
//   - the last content block of the last user message
func injectCacheControl(ar *anthropicRequest) {
	ephemeral := &cacheControl{Type: "ephemeral"}

	// Last system content block.
	if len(ar.System) > 0 {
		ar.System[len(ar.System)-1].CacheControl = ephemeral
	}

	// Last tool definition.
	if len(ar.Tools) > 0 {
		ar.Tools[len(ar.Tools)-1].CacheControl = ephemeral
	}

	// Last content block of the last user message.
	for i := len(ar.Messages) - 1; i >= 0; i-- {
		if ar.Messages[i].Role == "user" && len(ar.Messages[i].Content) > 0 {
			last := len(ar.Messages[i].Content) - 1
			ar.Messages[i].Content[last].CacheControl = ephemeral
			break
		}
	}
}

// collectBetaHeaders gathers all beta header values that should be sent,
// combining user-specified values with auto-injected ones (like prompt caching).
func collectBetaHeaders(req *llm.Request) string {
	var headers []string

	// Auto-inject prompt caching beta when auto_cache is enabled.
	if autoCacheEnabled(req) {
		headers = append(headers, promptCachingBeta)
	}

	// Add user-specified beta headers from provider options.
	headers = append(headers, extractUserBetaHeaders(req)...)

	return strings.Join(headers, ",")
}

// extractUserBetaHeaders extracts beta header values from provider options.
func extractUserBetaHeaders(req *llm.Request) []string {
	if req.ProviderOptions == nil {
		return nil
	}
	opts, ok := req.ProviderOptions["anthropic"]
	if !ok {
		return nil
	}
	optsMap, ok := opts.(map[string]any)
	if !ok {
		return nil
	}
	beta, ok := optsMap["beta_headers"]
	if !ok {
		return nil
	}
	return parseBetaHeaderValue(beta)
}

// parseBetaHeaderValue converts a beta_headers value (string, []string, or []any)
// to a string slice.
func parseBetaHeaderValue(beta any) []string {
	var out []string
	switch v := beta.(type) {
	case string:
		if v != "" {
			out = append(out, v)
		}
	case []string:
		for _, s := range v {
			if s != "" {
				out = append(out, s)
			}
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// translateMessage converts a single llm.Message to Anthropic format.
func translateMessage(m llm.Message) anthropicMessage {
	role := string(m.Role)
	if m.Role == llm.RoleTool {
		role = "user"
	}

	// Initialize as empty slice (not nil) so JSON serializes to [] instead of null.
	// The Anthropic API rejects null content with "Input should be a valid list".
	content := make([]anthropicContent, 0, len(m.Content))
	for _, part := range m.Content {
		if c, ok := translateContentPart(part); ok {
			content = append(content, c)
		}
	}

	return anthropicMessage{Role: role, Content: content}
}

// translateContentPart converts a single llm.ContentPart to Anthropic format.
// Returns (content, true) if conversion succeeded, (zero, false) if the part should be skipped.
func translateContentPart(part llm.ContentPart) (anthropicContent, bool) {
	switch part.Kind {
	case llm.KindText:
		return anthropicContent{Type: "text", Text: part.Text}, true
	case llm.KindImage:
		return translateImagePart(part)
	case llm.KindToolCall:
		if part.ToolCall != nil {
			return anthropicContent{Type: "tool_use", ID: part.ToolCall.ID, Name: part.ToolCall.Name, Input: part.ToolCall.Arguments}, true
		}
	case llm.KindToolResult:
		if part.ToolResult != nil {
			return anthropicContent{Type: "tool_result", ToolUseID: part.ToolResult.ToolCallID, Content: part.ToolResult.Content, IsError: part.ToolResult.IsError}, true
		}
	case llm.KindThinking:
		if part.Thinking != nil {
			return anthropicContent{Type: "thinking", Thinking: part.Thinking.Text, Signature: part.Thinking.Signature}, true
		}
	case llm.KindRedactedThinking:
		if part.Thinking != nil {
			return anthropicContent{Type: "redacted_thinking", Data: part.Thinking.Signature}, true
		}
	}
	return anthropicContent{}, false
}

// translateImagePart converts an image content part to Anthropic format.
func translateImagePart(part llm.ContentPart) (anthropicContent, bool) {
	if part.Image == nil {
		return anthropicContent{}, false
	}
	if len(part.Image.Data) > 0 {
		return anthropicContent{
			Type:   "image",
			Source: &anthropicSource{Type: "base64", MediaType: part.Image.MediaType, Data: base64.StdEncoding.EncodeToString(part.Image.Data)},
		}, true
	}
	if part.Image.URL != "" {
		return anthropicContent{
			Type:   "image",
			Source: &anthropicSource{Type: "url", MediaType: part.Image.MediaType, URL: part.Image.URL},
		}, true
	}
	return anthropicContent{}, false
}

// mergeConsecutiveMessages merges adjacent messages with the same role.
func mergeConsecutiveMessages(msgs []anthropicMessage) []anthropicMessage {
	if len(msgs) == 0 {
		return msgs
	}

	var merged []anthropicMessage
	current := msgs[0]

	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role == current.Role {
			current.Content = append(current.Content, msgs[i].Content...)
		} else {
			merged = append(merged, current)
			current = msgs[i]
		}
	}
	merged = append(merged, current)
	return merged
}

// translateToolChoice converts llm.ToolChoice to Anthropic format.
func translateToolChoice(tc *llm.ToolChoice) any {
	switch tc.Mode {
	case "auto":
		return map[string]string{"type": "auto"}
	case "none":
		return nil
	case "required":
		return map[string]string{"type": "any"}
	case "named":
		return map[string]string{"type": "tool", "name": tc.ToolName}
	default:
		return map[string]string{"type": tc.Mode}
	}
}
