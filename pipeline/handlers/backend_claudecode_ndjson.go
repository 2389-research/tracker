// ABOUTME: NDJSON types and parser for the Claude Code backend.
// ABOUTME: Converts Claude CLI stream-json output into agent.Event objects.
package handlers

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
)

// ndjsonMessage represents a single NDJSON line from claude CLI output.
// The schema varies by type:
//   - "assistant": content is in Message.Content
//   - "result": turns in NumTurns, cost in TotalCostUSD, usage at top level
//   - "system": informational, parsed for subtype
type ndjsonMessage struct {
	Type         string          `json:"type"`
	Subtype      string          `json:"subtype,omitempty"`
	Message      *ndjsonInner    `json:"message,omitempty"`
	Content      []ndjsonContent `json:"content,omitempty"`
	NumTurns     int             `json:"num_turns,omitempty"`
	Turns        int             `json:"turns,omitempty"`
	TotalCostUSD float64         `json:"total_cost_usd,omitempty"`
	Result       string          `json:"result,omitempty"`
	Usage        *ndjsonUsage    `json:"usage,omitempty"`
	IsError      bool            `json:"is_error,omitempty"`
}

// ndjsonInner wraps the nested message object in "assistant" type messages.
type ndjsonInner struct {
	Content []ndjsonContent `json:"content,omitempty"`
	Usage   *ndjsonUsage    `json:"usage,omitempty"`
}

// ndjsonContent represents a content block within an NDJSON message.
type ndjsonContent struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ID        string          `json:"id,omitempty"`          // tool_use ID (assistant messages)
	ToolUseID string          `json:"tool_use_id,omitempty"` // tool_use ID (user/tool_result messages)
	Content   json.RawMessage `json:"content,omitempty"`     // string or array (subagent results)
	IsError   bool            `json:"is_error,omitempty"`
}

// contentString extracts the text from a content field that may be
// a JSON string or an array of content blocks.
func (c *ndjsonContent) contentString() string {
	if len(c.Content) == 0 {
		return ""
	}
	// Try string first (most common)
	var s string
	if json.Unmarshal(c.Content, &s) == nil {
		return s
	}
	// Try array of content blocks
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(c.Content, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(c.Content)
}

// ndjsonUsage represents token usage from a result message. The cache
// fields are Anthropic-native — the Claude CLI reports them directly
// in the NDJSON result envelope, so we read real counts (not estimates)
// and forward them to llm.EstimateCost which prices cache reads at
// 10% and cache writes at 25% of the input rate. Pre-fix, these
// fields were silently dropped and heavy-cache workloads (Sonnet +
// CLAUDE.md injection on every turn) were charged ~3× the real input
// rate because all input tokens were treated as fresh.
type ndjsonUsage struct {
	InputTokens              int     `json:"input_tokens,omitempty"`
	OutputTokens             int     `json:"output_tokens,omitempty"`
	CacheReadInputTokens     int     `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int     `json:"cache_creation_input_tokens,omitempty"`
	CostUSD                  float64 `json:"cost_usd,omitempty"`
}

// parseMessage converts a raw NDJSON message into zero or more agent.Event objects.
func parseMessage(raw json.RawMessage, state *runState) []agent.Event {
	var msg ndjsonMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		state.decodeErrors++
		log.Printf("[claude-code] warning: failed to unmarshal NDJSON message: %v", err)
		return nil
	}
	now := time.Now()
	return dispatchNDJSONMessage(msg, now, state)
}

// dispatchNDJSONMessage routes a parsed NDJSON message to the appropriate handler.
func dispatchNDJSONMessage(msg ndjsonMessage, now time.Time, state *runState) []agent.Event {
	switch msg.Type {
	case "system":
		return parseSystemMessage(msg, now)
	case "assistant":
		return parseAssistantMessage(msg, now, state)
	case "user":
		return parseUserContent(msg.Content, now, state)
	case "result":
		storeResult(msg, state)
		return nil
	case "rate_limit_event":
		return nil
	default:
		log.Printf("[claude-code] warning: unknown NDJSON message type: %q", msg.Type)
		return nil
	}
}

// parseSystemMessage handles "system" type NDJSON messages.
func parseSystemMessage(msg ndjsonMessage, now time.Time) []agent.Event {
	if msg.Subtype != "init" {
		return nil
	}
	return []agent.Event{
		{Type: agent.EventLLMRequestPreparing, Timestamp: now, Provider: "claude-code"},
		{Type: agent.EventTurnStart, Timestamp: now},
	}
}

// parseAssistantMessage handles "assistant" type NDJSON messages.
func parseAssistantMessage(msg ndjsonMessage, now time.Time, state *runState) []agent.Event {
	var content []ndjsonContent
	if msg.Message != nil {
		content = msg.Message.Content
	}
	return parseAssistantContent(content, now, state)
}

// storeResult populates the session result from a "result" NDJSON message.
func storeResult(msg ndjsonMessage, state *runState) {
	turns := msg.NumTurns
	if turns == 0 {
		turns = msg.Turns
	}
	result := &agent.SessionResult{
		Turns:    turns,
		Provider: "claude-code",
	}

	result.ToolCalls = make(map[string]int, len(state.toolUseIDs))
	for _, name := range state.toolUseIDs {
		result.ToolCalls[name]++
	}

	if msg.Usage != nil {
		// TotalTokens stays fresh-input + output to match the convention
		// in llm/anthropic/translate_response.go:54 — cache tokens are
		// tracked separately via the *int fields and priced independently
		// by llm.EstimateCost. Diverging here would make BudgetGuard's
		// --max-tokens semantics differ by backend, which is exactly
		// what we're trying to avoid.
		result.Usage = llm.Usage{
			InputTokens:   msg.Usage.InputTokens,
			OutputTokens:  msg.Usage.OutputTokens,
			TotalTokens:   msg.Usage.InputTokens + msg.Usage.OutputTokens,
			EstimatedCost: msg.TotalCostUSD,
		}
		if msg.Usage.CacheReadInputTokens > 0 {
			v := msg.Usage.CacheReadInputTokens
			result.Usage.CacheReadTokens = &v
		}
		if msg.Usage.CacheCreationInputTokens > 0 {
			v := msg.Usage.CacheCreationInputTokens
			result.Usage.CacheWriteTokens = &v
		}
	}

	state.lastResult = result
}

// parseAssistantContent processes content blocks from an assistant message.
func parseAssistantContent(content []ndjsonContent, now time.Time, state *runState) []agent.Event {
	var events []agent.Event
	for _, c := range content {
		switch c.Type {
		case "text":
			events = append(events, agent.Event{
				Type:      agent.EventTextDelta,
				Timestamp: now,
				Text:      c.Text,
			})
		case "thinking":
			events = append(events, agent.Event{
				Type:      agent.EventLLMReasoning,
				Timestamp: now,
				Text:      c.Text,
			})
		case "tool_use":
			// Claude Code uses "id" for tool_use blocks in assistant messages.
			toolID := c.ID
			if toolID == "" {
				toolID = c.ToolUseID // fallback
			}
			state.toolUseIDs[toolID] = c.Name
			events = append(events, agent.Event{
				Type:      agent.EventToolCallStart,
				Timestamp: now,
				ToolName:  c.Name,
				ToolInput: string(c.Input),
			})
		default:
			log.Printf("[claude-code] warning: unknown assistant content type: %q", c.Type)
		}
	}
	return events
}

// parseUserContent processes content blocks from a user message (tool results).
func parseUserContent(content []ndjsonContent, now time.Time, state *runState) []agent.Event {
	var events []agent.Event
	for _, c := range content {
		if c.Type != "tool_result" {
			continue
		}
		toolName := state.toolUseIDs[c.ToolUseID]
		evt := agent.Event{
			Type:      agent.EventToolCallEnd,
			Timestamp: now,
			ToolName:  toolName,
		}
		if c.IsError {
			evt.ToolError = c.contentString()
		} else {
			evt.ToolOutput = c.contentString()
		}
		events = append(events, evt)
	}
	return events
}
