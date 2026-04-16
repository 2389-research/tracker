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
func AdaptPipelineEvent(evt pipeline.PipelineEvent) tea.Msg {
	switch evt.Type {
	case pipeline.EventStageStarted:
		return MsgNodeStarted{NodeID: evt.NodeID}
	case pipeline.EventStageCompleted:
		return MsgNodeCompleted{NodeID: evt.NodeID, Outcome: "success"}
	case pipeline.EventStageFailed:
		return MsgNodeFailed{NodeID: evt.NodeID, Error: pipelineEventMsg(evt)}
	case pipeline.EventStageRetrying:
		return MsgNodeRetrying{NodeID: evt.NodeID, Message: evt.Message}
	case pipeline.EventPipelineCompleted:
		return MsgPipelineCompleted{}
	case pipeline.EventPipelineFailed:
		return MsgPipelineFailed{Error: pipelineEventMsg(evt)}
	default:
		return nil
	}
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
	case agent.EventToolCallStart:
		return MsgToolCallStart{NodeID: nodeID, ToolName: evt.ToolName, ToolInput: evt.ToolInput}
	case agent.EventToolCallEnd:
		return adaptToolCallEnd(evt, nodeID)
	case agent.EventError:
		return adaptAgentError(evt, nodeID)
	case agent.EventVerify:
		return MsgVerifyStatus{NodeID: nodeID, Text: evt.Text}
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
