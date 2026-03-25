// ABOUTME: Response translation from Anthropic Messages API format to unified llm types.
// ABOUTME: Handles content block mapping (text, tool_use, thinking, redacted_thinking) and finish reasons.
package anthropic

import (
	"encoding/json"

	"github.com/2389-research/tracker/llm"
)

// --- Response translation ---

// anthropicResponse is the wire format for an Anthropic Messages API response.
type anthropicResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Model      string                  `json:"model"`
	Role       string                  `json:"role"`
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      anthropicUsage          `json:"usage"`
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
