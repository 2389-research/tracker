// ABOUTME: Event adapter — converts raw engine events into typed TUI messages.
// ABOUTME: The ONLY file in the TUI package that imports pipeline, agent, and llm engine types.
package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// AdaptPipelineEvent maps a pipeline lifecycle event to a typed TUI message.
// Returns nil for event types that have no TUI representation.
//
// Terminal events are consumed authoritatively: any event carrying a non-empty
// PipelineEvent.TerminalStatus becomes a single MsgPipelineTerminated (see
// adaptTerminal). This is the stateless free-function form, so the terminal
// message carries a nil Override — the function doesn't observe override events
// across the run. Callers that need the terminal message to carry the headline
// Override should use the stateful PipelineAdapter (NewPipelineAdapter) instead;
// it accumulates EventValidationOverridden across the run. Tests retain the
// free-function form for one-shot conversions.
func AdaptPipelineEvent(evt pipeline.PipelineEvent) tea.Msg {
	switch evt.Type {
	case pipeline.EventStageStarted:
		return MsgNodeStarted{NodeID: evt.NodeID}
	case pipeline.EventStageCompleted:
		return MsgNodeCompleted{NodeID: evt.NodeID, Outcome: "success"}
	case pipeline.EventStageFailed, pipeline.EventWorkPreserveFailed:
		// EventWorkPreserveFailed (#423) is the terminal never-lose-work HARD
		// signal. It surfaces in the TUI identically to a stage failure — a hard
		// failure line — but is a distinct event type upstream only so that
		// `tracker diagnose` does NOT count it as another per-node execution
		// attempt toward RetryCount / IdenticalRetries (#428 review).
		return MsgNodeFailed{NodeID: evt.NodeID, Error: pipelineEventMsg(evt)}
	case pipeline.EventStageRetrying:
		return MsgNodeRetrying{NodeID: evt.NodeID, Message: evt.Message}
	case pipeline.EventValidationOverridden:
		return adaptValidationOverridden(evt)
	}
	// Every terminal exit (completed / failed / budget_exceeded / billing
	// paused) carries an authoritative TerminalStatus; a non-terminal event has
	// an empty one and yields nil here.
	return adaptTerminal(evt, nil)
}

// adaptTerminal converts an authoritative terminal event into the single
// MsgPipelineTerminated for the run, or returns nil when evt is not the
// run-level finish. The engine stamps PipelineEvent.TerminalStatus on exactly
// one terminal event per run, so consuming it directly means the TUI never
// re-derives how the run ended.
//
// Root-scope rule: only an UNSCOPED NodeID (no "/") is the run-level finish. A
// subgraph or manager_loop child that trips the shared budget guard forwards
// its own scoped terminal event; the parent tracks that separately and emits
// its own unscoped terminal event, so a scoped one must not drive the TUI's
// completion row (see PipelineEvent.TerminalStatus docs). A malformed event
// with an empty TerminalStatus also yields nil — no phantom terminal
// transition. override, when non-nil, rides along as the headline display
// detail (latest per D5a) and is attached only to validation_overridden runs.
func adaptTerminal(evt pipeline.PipelineEvent, override *pipeline.OverrideDetail) tea.Msg {
	status := pipeline.TerminalStatus(evt.TerminalStatus)
	if status == "" || IsSubgraphNode(evt.NodeID) {
		return nil
	}
	msg := MsgPipelineTerminated{Status: status}
	if status == pipeline.OutcomeValidationOverridden {
		msg.Override = override
	}
	if !status.IsSuccess() {
		msg.Error = pipelineEventMsg(evt)
	}
	return msg
}

// adaptValidationOverridden builds MsgValidationOverridden from a pipeline
// event. The engine guarantees evt.Override is non-nil for this event type;
// the nil check is defensive against malformed events from custom emitters.
func adaptValidationOverridden(evt pipeline.PipelineEvent) tea.Msg {
	if evt.Override == nil {
		return nil
	}
	return MsgValidationOverridden{NodeID: evt.NodeID, Detail: *evt.Override}
}

// PipelineAdapter is a stateful event-to-message adapter that accumulates
// override events across a run so the MsgPipelineTerminated it emits carries the
// headline OverrideDetail per Gap 5.2 spec D5a (latest entry wins). The terminal
// Status itself comes straight off the authoritative PipelineEvent.TerminalStatus
// — the adapter no longer infers it from override presence. Use this when the
// TUI needs the headline override on the terminal event; use the stateless
// AdaptPipelineEvent for one-shot conversions.
//
// Lifetime is scoped to a single pipeline run. Construct one per run via
// NewPipelineAdapter — sharing one across runs would mix override state.
type PipelineAdapter struct {
	overrides []pipeline.OverrideDetail
}

// NewPipelineAdapter returns a freshly-initialized PipelineAdapter ready to
// adapt one pipeline run's events.
func NewPipelineAdapter() *PipelineAdapter {
	return &PipelineAdapter{}
}

// Adapt is the stateful equivalent of AdaptPipelineEvent: it tracks override
// events as they arrive and, on the run's authoritative terminal event, returns
// a MsgPipelineTerminated whose Status comes from PipelineEvent.TerminalStatus
// and whose headline Override is the latest accumulated entry. Other event types
// route through the same mapping as the free function.
func (a *PipelineAdapter) Adapt(evt pipeline.PipelineEvent) tea.Msg {
	if evt.Type == pipeline.EventValidationOverridden {
		if evt.Override != nil {
			a.overrides = append(a.overrides, *evt.Override)
		}
		return adaptValidationOverridden(evt)
	}
	if evt.TerminalStatus != "" {
		return adaptTerminal(evt, a.headlineOverride())
	}
	return AdaptPipelineEvent(evt)
}

// headlineOverride returns the LATEST accumulated override per spec D5a
// (operators reading "validation override at <gate>" should see the most recent,
// closest-to-exit gate by default), or nil when no override fired.
func (a *PipelineAdapter) headlineOverride() *pipeline.OverrideDetail {
	if n := len(a.overrides); n > 0 {
		head := a.overrides[n-1]
		return &head
	}
	return nil
}

// pipelineEventMsg returns the error message from a pipeline event, preferring Err over Message.
func pipelineEventMsg(evt pipeline.PipelineEvent) string {
	if evt.Err != nil {
		return evt.Err.Error()
	}
	return evt.Message
}

// AdaptAgentEvent maps an agent session event to a typed TUI message.
// Returns nil for event types that have no TUI representation.
func AdaptAgentEvent(evt agent.Event, nodeID string) tea.Msg {
	switch evt.Type {
	case agent.EventLLMRequestPreparing:
		return MsgLLMRequestPreparing{NodeID: nodeID, Provider: evt.Provider, Model: evt.Model}
	case agent.EventLLMRequestStart:
		return MsgThinkingStarted{NodeID: nodeID}
	case agent.EventLLMFinish:
		return MsgThinkingStopped{NodeID: nodeID}
	case agent.EventTextDelta:
		return MsgTextChunk{NodeID: nodeID, Text: evt.Text}
	default:
		return adaptAgentToolEvent(evt, nodeID)
	}
}

// adaptAgentToolEvent handles the tool-call and status agent events. Split out
// of AdaptAgentEvent so each switch stays under the complexity gate.
func adaptAgentToolEvent(evt agent.Event, nodeID string) tea.Msg {
	switch evt.Type {
	case agent.EventToolCallStart:
		return MsgToolCallStart{NodeID: nodeID, ToolName: evt.ToolName, ToolInput: evt.ToolInput}
	case agent.EventToolCallEnd:
		return adaptToolCallEnd(evt, nodeID)
	case agent.EventError:
		return adaptAgentError(evt, nodeID)
	case agent.EventVerify:
		return MsgVerifyStatus{NodeID: nodeID, Text: evt.Text}
	case agent.EventCheckpoint:
		return MsgVerifyStatus{NodeID: nodeID, Text: "checkpoint: " + evt.Text}
	default:
		return nil
	}
}

// adaptToolCallEnd builds a MsgToolCallEnd from an agent tool call end event.
func adaptToolCallEnd(evt agent.Event, nodeID string) tea.Msg {
	return MsgToolCallEnd{
		NodeID:   nodeID,
		ToolName: evt.ToolName,
		Output:   evt.ToolOutput,
		Error:    evt.ToolError,
	}
}

// adaptAgentError builds a MsgAgentError from an agent error event.
func adaptAgentError(evt agent.Event, nodeID string) tea.Msg {
	errMsg := ""
	if evt.Err != nil {
		errMsg = evt.Err.Error()
	}
	return MsgAgentError{NodeID: nodeID, Error: errMsg}
}

// AdaptLLMTraceEvent maps an LLM trace event to one or more typed TUI messages.
// Some trace events produce multiple messages (e.g. TraceRequestStart emits both
// MsgLLMRequestStart and MsgThinkingStarted). Returns nil for filtered events.
func AdaptLLMTraceEvent(evt llm.TraceEvent, nodeID string, verbose bool) []tea.Msg {
	switch evt.Kind {
	case llm.TraceRequestStart:
		// Thinking start/stop is handled by AdaptAgentEvent (which has the node ID).
		// LLM trace only emits provider-level messages.
		return []tea.Msg{
			MsgLLMRequestStart{NodeID: nodeID, Provider: evt.Provider, Model: evt.Model},
		}
	case llm.TraceText:
		return []tea.Msg{
			MsgTextChunk{NodeID: nodeID, Text: evt.Preview},
		}
	case llm.TraceReasoning:
		return []tea.Msg{
			MsgReasoningChunk{NodeID: nodeID, Text: evt.Preview},
		}
	case llm.TraceFinish:
		return []tea.Msg{
			MsgLLMFinish{NodeID: nodeID},
		}
	case llm.TraceToolPrepare:
		return nil // MsgToolCallStart arrives from AdaptAgentEvent shortly after
	case llm.TraceProviderRaw:
		if !verbose {
			return nil
		}
		return []tea.Msg{
			MsgLLMProviderRaw{NodeID: nodeID, Data: evt.RawPreview},
		}
	default:
		return nil
	}
}
