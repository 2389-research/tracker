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
	basePrompt := "File tool arguments (read, write, edit, glob, grep_search) MUST use paths relative to the working directory. " +
		"For example, use \"src/main.go\" instead of \"/home/user/project/src/main.go\". " +
		"Bash commands may use absolute paths when needed."
	if s.config.SystemPrompt != "" {
		s.messages = append(s.messages, llm.SystemMessage(basePrompt+"\n\n"+s.config.SystemPrompt))
	} else {
		s.messages = append(s.messages, llm.SystemMessage(basePrompt))
	}

	finalUserInput := userInput
	if s.config.Localize {
		if block := localize(s.config.WorkingDir, userInput).Message; block != "" {
			finalUserInput = block + "\n" + userInput
		}
	}
	s.messages = append(s.messages, llm.UserMessage(finalUserInput))
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
// It returns true if any tool call resulted in an error.
func (s *Session) executeToolCalls(ctx context.Context, toolCalls []llm.ToolCallData, result *SessionResult) bool {
	var toolResults []llm.ContentPart
	anyError := false
	for _, call := range toolCalls {
		toolResult, toolDuration := s.executeSingleTool(ctx, call)
		result.ToolCalls[call.Name]++
		if toolResult.IsError {
			anyError = true
		}

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
	return anyError
}

// executeSingleTool runs a single tool call, handling caching.
func (s *Session) executeSingleTool(ctx context.Context, call llm.ToolCallData) (llm.ToolResultData, time.Duration) {
	s.emit(Event{
		Type:      EventToolCallStart,
		SessionID: s.id,
		ToolName:  call.Name,
		ToolInput: string(call.Arguments),
	})

	policy := s.toolCachePolicy(call.Name)

	if result, hit := s.tryToolCache(call, policy); hit {
		return result, 0
	}

	return s.executeAndCacheTool(ctx, call, policy)
}

// toolCachePolicy returns the cache policy for the named tool, or CachePolicyNone if unknown.
func (s *Session) toolCachePolicy(name string) tools.CachePolicy {
	tool := s.registry.Get(name)
	if tool == nil {
		return tools.CachePolicyNone
	}
	return tools.GetCachePolicy(tool)
}

// tryToolCache checks the cache for a previous result. Returns the cached result and true on hit.
func (s *Session) tryToolCache(call llm.ToolCallData, policy tools.CachePolicy) (llm.ToolResultData, bool) {
	if s.cache == nil || policy != tools.CachePolicyCacheable {
		return llm.ToolResultData{}, false
	}
	cached, hit := s.cache.get(call.Name, string(call.Arguments))
	if !hit {
		return llm.ToolResultData{}, false
	}
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
	}, true
}

// executeAndCacheTool runs the tool, updates cache and timings, and returns the result.
func (s *Session) executeAndCacheTool(ctx context.Context, call llm.ToolCallData, policy tools.CachePolicy) (llm.ToolResultData, time.Duration) {
	toolStart := time.Now()
	toolResult := s.registry.Execute(ctx, call)
	toolDuration := time.Since(toolStart)
	s.toolTimings[call.Name] += toolDuration

	if s.cache != nil {
		s.updateToolCache(call, policy, toolResult)
	}

	return toolResult, toolDuration
}

// updateToolCache invalidates or stores tool results based on cache policy.
func (s *Session) updateToolCache(call llm.ToolCallData, policy tools.CachePolicy, result llm.ToolResultData) {
	if policy != tools.CachePolicyCacheable {
		// Invalidate on mutating or unknown tools (safe default: unclassified tools may have side effects).
		s.cache.invalidateAll()
		return
	}
	if !result.IsError {
		s.cache.store(call.Name, string(call.Arguments), result.Content)
	}
}
