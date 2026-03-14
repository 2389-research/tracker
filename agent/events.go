// ABOUTME: Event types emitted by the agent session for UI rendering and logging.
// ABOUTME: Defines EventType constants, Event struct, EventHandler interface, and multi-handler fan-out.
package agent

import (
	"time"

	"github.com/2389-research/tracker/llm"
)

// EventType identifies the kind of event emitted during an agent session.
type EventType string

const (
	EventSessionStart         EventType = "session_start"
	EventSessionEnd           EventType = "session_end"
	EventTurnStart            EventType = "turn_start"
	EventTurnEnd              EventType = "turn_end"
	EventToolCallStart        EventType = "tool_call_start"
	EventToolCallEnd          EventType = "tool_call_end"
	EventTextDelta            EventType = "text_delta"
	EventError                EventType = "error"
	EventContextWindowWarning EventType = "context_window_warning"
	EventSteeringInjected     EventType = "steering_injected"
	EventLLMRequestStart      EventType = "llm_request_start"
	EventLLMReasoning         EventType = "llm_reasoning"
	EventLLMText              EventType = "llm_text"
	EventLLMToolPrepare       EventType = "llm_tool_prepare"
	EventLLMFinish            EventType = "llm_finish"
	EventLLMProviderRaw       EventType = "llm_provider_raw"
	EventToolCacheHit         EventType = "tool_cache_hit"
	EventContextCompaction    EventType = "context_compaction"
	EventTurnMetrics          EventType = "turn_metrics"
)

// TurnMetrics captures per-turn token and performance data.
type TurnMetrics struct {
	InputTokens        int
	OutputTokens       int
	CacheReadTokens    int
	CacheWriteTokens   int
	ContextUtilization float64
	ToolCacheHits      int
	ToolCacheMisses    int
	TurnDuration       time.Duration
	EstimatedCost      float64
}

// Event carries data about something that happened during an agent session.
type Event struct {
	Type               EventType
	Timestamp          time.Time
	SessionID          string
	Turn               int
	ToolName           string
	ToolInput          string
	ToolOutput         string
	ToolError          string
	Text               string
	Err                error
	ContextUtilization float64
	Provider           string
	Model              string
	Preview            string
	ProviderEvent      string
	FinishReason       string
	Usage              llm.Usage
	Metrics            *TurnMetrics
	ToolDuration       time.Duration
}

// EventHandler receives events emitted by the agent session.
type EventHandler interface {
	HandleEvent(evt Event)
}

// EventHandlerFunc is an adapter to allow the use of ordinary functions as EventHandlers.
type EventHandlerFunc func(evt Event)

func (f EventHandlerFunc) HandleEvent(evt Event) {
	f(evt)
}

type noopHandler struct{}

func (noopHandler) HandleEvent(Event) {}

// NoopHandler silently discards all events.
var NoopHandler EventHandler = noopHandler{}

// MultiHandler returns an EventHandler that fans out each event to all provided handlers.
func MultiHandler(handlers ...EventHandler) EventHandler {
	return multiHandler(handlers)
}

type multiHandler []EventHandler

func (m multiHandler) HandleEvent(evt Event) {
	for _, h := range m {
		if h != nil {
			h.HandleEvent(evt)
		}
	}
}
