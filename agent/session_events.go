// ABOUTME: Session event emission — turn metrics and LLM trace events.
// ABOUTME: Every emitted event carries provider/model/usage attribution (#508).
package agent

import (
	"time"

	"github.com/2389-research/tracker/llm"
)

// emit sends an event with the current timestamp to the session's event handler.
func (s *Session) emit(evt Event) {
	evt.Timestamp = time.Now()
	s.handler.HandleEvent(evt)
}

// emitTurnMetrics emits an EventTurnMetrics event and updates LongestTurn on result.
// It computes per-turn cache deltas from the snapshot taken before tool execution.
func (s *Session) emitTurnMetrics(turn int, turnStart time.Time, resp *llm.Response, tracker *ContextWindowTracker, prevCacheHits, prevCacheMisses int, result *SessionResult) {
	turnDuration := time.Since(turnStart)
	if turnDuration > result.LongestTurn {
		result.LongestTurn = turnDuration
	}

	turnCacheHits, turnCacheMisses := 0, 0
	if s.cache != nil {
		turnCacheHits = s.cache.hits - prevCacheHits
		turnCacheMisses = s.cache.misses - prevCacheMisses
	}

	cacheRead, cacheWrite := 0, 0
	if resp.Usage.CacheReadTokens != nil {
		cacheRead = *resp.Usage.CacheReadTokens
	}
	if resp.Usage.CacheWriteTokens != nil {
		cacheWrite = *resp.Usage.CacheWriteTokens
	}

	estimatedCost := resp.Usage.EstimatedCost
	if estimatedCost == 0 {
		estimatedCost = llm.EstimateCost(s.config.Model, resp.Usage)
	}

	// Carry the same top-level attribution as llm_finish (#508): a consumer
	// building per-turn cost rollups off turn_metrics would otherwise read an
	// empty model and zero usage and conclude the turn was free. Response
	// fields win; the configured model/provider is the fallback for adapters
	// that leave them unset.
	provider, model := resp.Provider, resp.Model
	if provider == "" {
		provider = s.config.Provider
	}
	if model == "" {
		model = s.config.Model
	}

	s.emit(Event{
		Type:      EventTurnMetrics,
		SessionID: s.id,
		Turn:      turn,
		Provider:  provider,
		Model:     model,
		Usage:     resp.Usage,
		Metrics: &TurnMetrics{
			InputTokens:        resp.Usage.InputTokens,
			OutputTokens:       resp.Usage.OutputTokens,
			CacheReadTokens:    cacheRead,
			CacheWriteTokens:   cacheWrite,
			ContextUtilization: tracker.Utilization(),
			ToolCacheHits:      turnCacheHits,
			ToolCacheMisses:    turnCacheMisses,
			TurnDuration:       turnDuration,
			EstimatedCost:      estimatedCost,
		},
	})
}

func (s *Session) emitLLMTraceEvent(turn int, traceEvt llm.TraceEvent) {
	evt := Event{
		SessionID:     s.id,
		Turn:          turn,
		Provider:      traceEvt.Provider,
		Model:         traceEvt.Model,
		Preview:       traceEvt.Preview,
		ToolName:      traceEvt.ToolName,
		ProviderEvent: traceEvt.ProviderEvent,
		FinishReason:  traceEvt.FinishReason,
		Usage:         traceEvt.Usage,
	}

	switch traceEvt.Kind {
	case llm.TraceRequestStart:
		evt.Type = EventLLMRequestStart
	case llm.TraceReasoning:
		evt.Type = EventLLMReasoning
	case llm.TraceText:
		evt.Type = EventLLMText
	case llm.TraceToolPrepare:
		evt.Type = EventLLMToolPrepare
	case llm.TraceFinish:
		evt.Type = EventLLMFinish
	case llm.TraceProviderRaw:
		evt.Type = EventLLMProviderRaw
	default:
		return
	}

	s.emit(evt)
}
