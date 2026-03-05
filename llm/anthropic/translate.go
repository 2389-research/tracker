// ABOUTME: Request/response translation between the unified llm types and Anthropic Messages API format.
// ABOUTME: Handles system extraction, message alternation merging, content block mapping, and finish reasons.
package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/2389-research/mammoth-lite/llm"
)

// promptCachingBeta is the beta header value for prompt caching support.
const promptCachingBeta = "prompt-caching-2024-07-31"

// defaultMaxTokens is the default max_tokens value when not specified.
// Anthropic requires max_tokens in every request.
const defaultMaxTokens = 4096

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
	var msgs []llm.Message
	for _, m := range req.Messages {
		if m.Role == llm.RoleSystem || m.Role == llm.RoleDeveloper {
			for _, part := range m.Content {
				if part.Kind == llm.KindText {
					ar.System = append(ar.System, anthropicContent{
						Type: "text",
						Text: part.Text,
					})
				}
			}
		} else {
			msgs = append(msgs, m)
		}
	}

	// Convert messages, mapping tool role to user role with tool_result blocks.
	var converted []anthropicMessage
	for _, m := range msgs {
		am := translateMessage(m)
		converted = append(converted, am)
	}

	// Merge consecutive same-role messages for strict alternation.
	ar.Messages = mergeConsecutiveMessages(converted)

	// Translate tools. Skip when tool choice mode is "none" since Anthropic
	// rejects requests that include a tools array with tool_choice "none".
	skipTools := req.ToolChoice != nil && req.ToolChoice.Mode == "none"
	if !skipTools {
		for _, t := range req.Tools {
			ar.Tools = append(ar.Tools, anthropicTool{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.Parameters,
			})
		}
	}

	// Translate tool choice.
	if req.ToolChoice != nil {
		ar.ToolChoice = translateToolChoice(req.ToolChoice)
	}

	// Inject cache_control annotations when auto_cache is enabled (default true).
	if autoCacheEnabled(req) {
		injectCacheControl(&ar)
	}

	// Apply provider options (pass through anthropic-specific fields).
	body, err := json.Marshal(ar)
	if err != nil {
		return nil, err
	}

	// Merge provider_options["anthropic"] into the body (except reserved keys).
	if opts, ok := req.ProviderOptions["anthropic"]; ok {
		if optsMap, ok := opts.(map[string]any); ok {
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
			body, err = json.Marshal(bodyMap)
			if err != nil {
				return nil, err
			}
		}
	}

	return body, nil
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
	if req.ProviderOptions != nil {
		if opts, ok := req.ProviderOptions["anthropic"]; ok {
			if optsMap, ok := opts.(map[string]any); ok {
				if beta, ok := optsMap["beta_headers"]; ok {
					switch v := beta.(type) {
					case string:
						if v != "" {
							headers = append(headers, v)
						}
					case []string:
						for _, s := range v {
							if s != "" {
								headers = append(headers, s)
							}
						}
					case []any:
						for _, item := range v {
							if s, ok := item.(string); ok && s != "" {
								headers = append(headers, s)
							}
						}
					}
				}
			}
		}
	}

	return strings.Join(headers, ",")
}

// translateMessage converts a single llm.Message to Anthropic format.
func translateMessage(m llm.Message) anthropicMessage {
	role := string(m.Role)
	if m.Role == llm.RoleTool {
		role = "user"
	}

	var content []anthropicContent
	for _, part := range m.Content {
		switch part.Kind {
		case llm.KindText:
			content = append(content, anthropicContent{
				Type: "text",
				Text: part.Text,
			})

		case llm.KindImage:
			if part.Image != nil {
				if len(part.Image.Data) > 0 {
					content = append(content, anthropicContent{
						Type: "image",
						Source: &anthropicSource{
							Type:      "base64",
							MediaType: part.Image.MediaType,
							Data:      base64.StdEncoding.EncodeToString(part.Image.Data),
						},
					})
				} else if part.Image.URL != "" {
					content = append(content, anthropicContent{
						Type: "image",
						Source: &anthropicSource{
							Type:      "url",
							MediaType: part.Image.MediaType,
							URL:       part.Image.URL,
						},
					})
				}
			}

		case llm.KindToolCall:
			if part.ToolCall != nil {
				content = append(content, anthropicContent{
					Type:  "tool_use",
					ID:    part.ToolCall.ID,
					Name:  part.ToolCall.Name,
					Input: part.ToolCall.Arguments,
				})
			}

		case llm.KindToolResult:
			if part.ToolResult != nil {
				content = append(content, anthropicContent{
					Type:      "tool_result",
					ToolUseID: part.ToolResult.ToolCallID,
					Content:   part.ToolResult.Content,
					IsError:   part.ToolResult.IsError,
				})
			}

		case llm.KindThinking:
			if part.Thinking != nil {
				content = append(content, anthropicContent{
					Type:      "thinking",
					Thinking:  part.Thinking.Text,
					Signature: part.Thinking.Signature,
				})
			}

		case llm.KindRedactedThinking:
			if part.Thinking != nil {
				content = append(content, anthropicContent{
					Type: "redacted_thinking",
					Data: part.Thinking.Signature,
				})
			}
		}
	}

	return anthropicMessage{
		Role:    role,
		Content: content,
	}
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

// --- Response translation ---

// anthropicResponse is the wire format for an Anthropic Messages API response.
type anthropicResponse struct {
	ID         string                    `json:"id"`
	Type       string                    `json:"type"`
	Model      string                    `json:"model"`
	Role       string                    `json:"role"`
	Content    []anthropicContentBlock   `json:"content"`
	StopReason string                    `json:"stop_reason"`
	Usage      anthropicUsage            `json:"usage"`
}

type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Data      string          `json:"data,omitempty"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

// translateResponse converts Anthropic Messages API JSON to a unified llm.Response.
func translateResponse(raw []byte) (*llm.Response, error) {
	var ar anthropicResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, err
	}

	var content []llm.ContentPart
	for _, block := range ar.Content {
		switch block.Type {
		case "text":
			content = append(content, llm.ContentPart{
				Kind: llm.KindText,
				Text: block.Text,
			})

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
				Kind: llm.KindRedactedThinking,
				Thinking: &llm.ThinkingData{
					Redacted:  true,
					Signature: block.Data,
				},
			})
		}
	}

	usage := llm.Usage{
		InputTokens:  ar.Usage.InputTokens,
		OutputTokens: ar.Usage.OutputTokens,
		TotalTokens:  ar.Usage.InputTokens + ar.Usage.OutputTokens,
	}

	if ar.Usage.CacheReadInputTokens > 0 {
		v := ar.Usage.CacheReadInputTokens
		usage.CacheReadTokens = &v
	}
	if ar.Usage.CacheCreationInputTokens > 0 {
		v := ar.Usage.CacheCreationInputTokens
		usage.CacheWriteTokens = &v
	}

	return &llm.Response{
		ID:    ar.ID,
		Model: ar.Model,
		Message: llm.Message{
			Role:    llm.RoleAssistant,
			Content: content,
		},
		FinishReason: translateFinishReason(ar.StopReason),
		Usage:        usage,
		Raw:          raw,
	}, nil
}

// translateFinishReason maps Anthropic stop reasons to the unified finish reason format.
func translateFinishReason(raw string) llm.FinishReason {
	var reason string
	switch raw {
	case "end_turn", "stop_sequence":
		reason = "stop"
	case "max_tokens":
		reason = "length"
	case "tool_use":
		reason = "tool_calls"
	default:
		reason = raw
	}
	return llm.FinishReason{
		Reason: reason,
		Raw:    raw,
	}
}
