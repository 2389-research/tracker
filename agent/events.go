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
	EventLLMRequestPreparing  EventType = "llm_request_preparing"
	// EventVerify is emitted for verify-after-edit status updates (pass/fail/retry).
	// Use EventError only for infrastructure failures (binary not found, etc.).
	EventVerify EventType = "verify"
	// EventCheckpoint is emitted when a turn-budget checkpoint fires and its
	// message is injected into the conversation as a user message.
	EventCheckpoint EventType = "checkpoint"
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
	NodeID             string // Pipeline node that owns this session (empty for standalone sessions).
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

// NodeScopedHandler wraps an EventHandler and stamps every event with a
// pipeline NodeID. This lets parallel branches identify their events without
// the agent layer needing to know about pipeline concepts.
func NodeScopedHandler(nodeID string, inner EventHandler) EventHandler {
	if inner == nil {
		return NoopHandler
	}
	return &nodeScopedHandler{nodeID: nodeID, inner: inner}
}

type nodeScopedHandler struct {
	nodeID string
	inner  EventHandler
}

func (h *nodeScopedHandler) HandleEvent(evt Event) {
	evt.NodeID = h.nodeID
	h.inner.HandleEvent(evt)
}
