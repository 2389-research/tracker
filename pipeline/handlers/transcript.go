// ABOUTME: Session transcript collector for codergen handler.
// ABOUTME: Captures tool calls, text output, and errors as an ordered plain-text log.
package handlers

import (
	"fmt"
	"strings"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/pipeline"
)

// transcriptCollector preserves an ordered plain-text transcript of a session
// while also keeping the concatenated assistant text for status parsing.
type transcriptCollector struct {
	lines     []string
	textParts []string
}

func (c *transcriptCollector) HandleEvent(evt agent.Event) {
	switch evt.Type {
	case agent.EventTurnStart:
		c.lines = append(c.lines, fmt.Sprintf("TURN %d", evt.Turn))
	case agent.EventToolCallStart:
		c.appendToolCallStart(evt)
	case agent.EventToolCallEnd:
		c.appendToolCallEnd(evt)
	case agent.EventTextDelta:
		c.appendTextDelta(evt)
	case agent.EventError:
		if evt.Err != nil {
			c.lines = append(c.lines, "ERROR:")
			c.lines = append(c.lines, evt.Err.Error())
		}
	}
}

func (c *transcriptCollector) appendToolCallStart(evt agent.Event) {
	c.lines = append(c.lines, fmt.Sprintf("TOOL CALL: %s", evt.ToolName))
	if evt.ToolInput != "" {
		c.lines = append(c.lines, "INPUT:")
		c.lines = append(c.lines, evt.ToolInput)
	}
}

func (c *transcriptCollector) appendToolCallEnd(evt agent.Event) {
	c.lines = append(c.lines, fmt.Sprintf("TOOL RESULT: %s", evt.ToolName))
	if evt.ToolOutput != "" {
		c.lines = append(c.lines, "OUTPUT:")
		c.lines = append(c.lines, evt.ToolOutput)
	}
	if evt.ToolError != "" {
		c.lines = append(c.lines, "ERROR:")
		c.lines = append(c.lines, evt.ToolError)
	}
}

func (c *transcriptCollector) appendTextDelta(evt agent.Event) {
	if evt.Text != "" {
		c.textParts = append(c.textParts, evt.Text)
		c.lines = append(c.lines, "TEXT:")
		c.lines = append(c.lines, evt.Text)
	}
}

func (c *transcriptCollector) text() string {
	return strings.Join(c.textParts, "")
}

func (c *transcriptCollector) transcript() string {
	return strings.Join(c.lines, "\n")
}

// derefInt safely dereferences an optional int pointer, returning 0 for nil.
func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// buildSessionStats converts an agent.SessionResult into a pipeline.SessionStats
// for inclusion in the trace entry.
func buildSessionStats(r agent.SessionResult) *pipeline.SessionStats {
	toolCalls := make(map[string]int, len(r.ToolCalls))
	for k, v := range r.ToolCalls {
		toolCalls[k] = v
	}
	return &pipeline.SessionStats{
		Turns:            r.Turns,
		ToolCalls:        toolCalls,
		TotalToolCalls:   r.TotalToolCalls(),
		FilesModified:    append([]string(nil), r.FilesModified...),
		FilesCreated:     append([]string(nil), r.FilesCreated...),
		Compactions:      r.CompactionsApplied,
		LongestTurn:      r.LongestTurn,
		CacheHits:        r.ToolCacheHits,
		CacheMisses:      r.ToolCacheMisses,
		InputTokens:      r.Usage.InputTokens,
		OutputTokens:     r.Usage.OutputTokens,
		TotalTokens:      r.Usage.TotalTokens,
		CostUSD:          r.Usage.EstimatedCost,
		ReasoningTokens:  derefInt(r.Usage.ReasoningTokens),
		CacheReadTokens:  derefInt(r.Usage.CacheReadTokens),
		CacheWriteTokens: derefInt(r.Usage.CacheWriteTokens),
		Provider:         r.Provider,
	}
}
