// ABOUTME: Run-loop helpers for Session.Run — extracted to reduce session.go file size.
// ABOUTME: Contains conversation init, steering, LLM calls, usage tracking, tool execution, and loop detection.
package agent

import (
	"context"
	"strings"
	"time"

	"github.com/2389-research/tracker/agent/tools"
	"github.com/2389-research/tracker/llm"
)

// initConversation sets up the initial system and user messages.
func (s *Session) initConversation(userInput string) {
	basePrompt := "All file paths in tool calls MUST be relative to the working directory. " +
		"NEVER use absolute paths starting with '/'. " +
		"For example, use \"src/main.go\" instead of \"/home/user/project/src/main.go\"."
	if s.config.SystemPrompt != "" {
		s.messages = append(s.messages, llm.SystemMessage(basePrompt+"\n\n"+s.config.SystemPrompt))
	} else {
		s.messages = append(s.messages, llm.SystemMessage(basePrompt))
	}
	s.messages = append(s.messages, llm.UserMessage(userInput))
}

// drainSteering consumes all pending steering messages and injects them into the conversation.
func (s *Session) drainSteering() {
	if s.steering == nil {
		return
	}
	for {
		select {
		case msg := <-s.steering:
			s.messages = append(s.messages, llm.UserMessage("[STEERING] "+msg))
			s.emit(Event{Type: EventSteeringInjected, SessionID: s.id, Text: msg})
		default:
			return
		}
	}
}

// doLLMCall prepares and sends a single LLM request for the given turn.
func (s *Session) doLLMCall(ctx context.Context, turn int) (*llm.Response, error) {
	req := &llm.Request{
		Model:           s.config.Model,
		Provider:        s.config.Provider,
		Messages:        s.messages,
		Tools:           s.registry.Definitions(),
		ReasoningEffort: s.config.ReasoningEffort,
		ResponseFormat:  s.buildResponseFormat(),
		TraceObservers: []llm.TraceObserver{
			llm.TraceObserverFunc(func(traceEvt llm.TraceEvent) {
				s.emitLLMTraceEvent(turn, traceEvt)
			}),
		},
	}

	s.emit(Event{
		Type:      EventLLMRequestPreparing,
		SessionID: s.id,
		Turn:      turn,
		Provider:  s.config.Provider,
		Model:     s.config.Model,
	})

	return s.client.Complete(ctx, req)
}

// buildResponseFormat creates an llm.ResponseFormat from session config.
func (s *Session) buildResponseFormat() *llm.ResponseFormat {
	if s.config.ResponseFormat == "" {
		return nil
	}
	rf := &llm.ResponseFormat{Type: s.config.ResponseFormat}
	if s.config.ResponseFormat == "json_schema" && s.config.ResponseSchema != "" {
		rf.JSONSchema = []byte(s.config.ResponseSchema)
		rf.Strict = true
	}
	return rf
}

// updateUsage updates result usage, context window tracking, and compaction.
func (s *Session) updateUsage(result *SessionResult, resp *llm.Response, turn int, tracker *ContextWindowTracker) {
	result.Usage = result.Usage.Add(resp.Usage)
	if resp.Usage.EstimatedCost == 0 {
		result.Usage.EstimatedCost += llm.EstimateCost(s.config.Model, resp.Usage)
	}
	result.Turns = turn

	tracker.Update(resp.Usage)
	if tracker.ShouldWarn() {
		s.emit(Event{
			Type:               EventContextWindowWarning,
			SessionID:          s.id,
			Turn:               turn,
			ContextUtilization: tracker.Utilization(),
		})
		tracker.MarkWarned()
	}

	// Check if context compaction is needed after updating utilization.
	// Skip if we already compacted at this turn (no new tool results to compact).
	if s.config.ContextCompaction == CompactionAuto && turn > s.lastCompactTurn {
		prevLen := totalToolResultBytes(s.messages)
		s.compactIfNeeded(tracker, turn)
		newLen := totalToolResultBytes(s.messages)
		if newLen < prevLen {
			s.lastCompactTurn = turn
			result.CompactionsApplied++
			s.emit(Event{
				Type:               EventContextCompaction,
				SessionID:          s.id,
				Turn:               turn,
				ContextUtilization: tracker.Utilization(),
			})
		}
	}
}

// snapshotCacheStats returns the current cache hit/miss counts for computing per-turn deltas.
func (s *Session) snapshotCacheStats() (hits, misses int) {
	if s.cache != nil {
		return s.cache.hits, s.cache.misses
	}
	return 0, 0
}

// handleNoToolCalls handles a response with no tool calls.
// Returns true if the session should stop (natural end), false if it should continue (truncation).
func (s *Session) handleNoToolCalls(resp *llm.Response, turn int, turnStart time.Time, tracker *ContextWindowTracker, prevCacheHits, prevCacheMisses int, result *SessionResult) bool {
	// If the response was truncated (hit max_tokens), inject a
	// continuation prompt so the agent keeps working instead of
	// stopping mid-thought.
	if resp.FinishReason.Reason == "length" || resp.FinishReason.Reason == "max_tokens" {
		text := resp.Text()
		if text != "" {
			s.emit(Event{Type: EventTextDelta, SessionID: s.id, Text: text})
		}
		s.messages = append(s.messages, llm.UserMessage(
			"Your previous response was truncated due to length. Continue where you left off. "+
				"Use tool calls to make progress — do not output large blocks of text directly.",
		))
		s.emitTurnMetrics(turn, turnStart, resp, tracker, prevCacheHits, prevCacheMisses, result)
		s.emit(Event{Type: EventTurnEnd, SessionID: s.id, Turn: turn})
		return false
	}

	text := resp.Text()

	if text != "" {
		s.emit(Event{Type: EventTextDelta, SessionID: s.id, Text: text})
	}
	s.emitTurnMetrics(turn, turnStart, resp, tracker, prevCacheHits, prevCacheMisses, result)
	s.emit(Event{Type: EventTurnEnd, SessionID: s.id, Turn: turn})
	return true
}

// computeToolSignature builds a string signature for loop detection.
func (s *Session) computeToolSignature(toolCalls []llm.ToolCallData) string {
	toolSigs := make([]string, len(toolCalls))
	for i, call := range toolCalls {
		toolSigs[i] = call.Name + ":" + compactJSON(string(call.Arguments))
	}
	return strings.Join(toolSigs, ",")
}

// executeToolCalls runs each tool call sequentially and appends results to messages.
func (s *Session) executeToolCalls(ctx context.Context, toolCalls []llm.ToolCallData, result *SessionResult) {
	var toolResults []llm.ContentPart
	for _, call := range toolCalls {
		toolResult, toolDuration := s.executeSingleTool(ctx, call)
		result.ToolCalls[call.Name]++

		s.emit(Event{
			Type:         EventToolCallEnd,
			SessionID:    s.id,
			ToolName:     call.Name,
			ToolOutput:   toolResult.Content,
			ToolError:    boolToErrStr(toolResult.IsError),
			ToolDuration: toolDuration,
		})

		toolResults = append(toolResults, llm.ContentPart{
			Kind:       llm.KindToolResult,
			ToolResult: &toolResult,
		})
	}

	s.messages = append(s.messages, llm.Message{
		Role:    llm.RoleTool,
		Content: toolResults,
	})
}

// executeSingleTool runs a single tool call, handling caching.
func (s *Session) executeSingleTool(ctx context.Context, call llm.ToolCallData) (llm.ToolResultData, time.Duration) {
	s.emit(Event{
		Type:      EventToolCallStart,
		SessionID: s.id,
		ToolName:  call.Name,
		ToolInput: string(call.Arguments),
	})

	tool := s.registry.Get(call.Name)
	policy := tools.CachePolicyNone
	if tool != nil {
		policy = tools.GetCachePolicy(tool)
	}

	// Try cache first.
	if s.cache != nil && policy == tools.CachePolicyCacheable {
		if cached, hit := s.cache.get(call.Name, string(call.Arguments)); hit {
			s.emit(Event{
				Type:      EventToolCacheHit,
				SessionID: s.id,
				ToolName:  call.Name,
				ToolInput: string(call.Arguments),
			})
			return llm.ToolResultData{
				ToolCallID: call.ID,
				Name:       call.Name,
				Content:    cached,
				IsError:    false,
			}, 0
		}
	}

	// Execute the tool.
	toolStart := time.Now()
	toolResult := s.registry.Execute(ctx, call)
	toolDuration := time.Since(toolStart)
	s.toolTimings[call.Name] += toolDuration

	// Invalidate cache on mutating tools or unknown tools (safe
	// default: an unclassified tool may have side effects).
	if s.cache != nil && policy != tools.CachePolicyCacheable {
		s.cache.invalidateAll()
	}

	if s.cache != nil && policy == tools.CachePolicyCacheable && !toolResult.IsError {
		s.cache.store(call.Name, string(call.Arguments), toolResult.Content)
	}

	return toolResult, toolDuration
}
