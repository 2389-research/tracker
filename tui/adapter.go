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
		errMsg := evt.Message
		if evt.Err != nil {
			errMsg = evt.Err.Error()
		}
		return MsgNodeFailed{NodeID: evt.NodeID, Error: errMsg}
	case pipeline.EventPipelineCompleted:
		return MsgPipelineCompleted{}
	case pipeline.EventPipelineFailed:
		errMsg := evt.Message
		if evt.Err != nil {
			errMsg = evt.Err.Error()
		}
		return MsgPipelineFailed{Error: errMsg}
	default:
		return nil
	}
}

// AdaptAgentEvent maps an agent session event to a typed TUI message.
// Returns nil for event types that have no TUI representation.
func AdaptAgentEvent(evt agent.Event, nodeID string) tea.Msg {
	switch evt.Type {
	case agent.EventTextDelta:
		return MsgTextChunk{NodeID: nodeID, Text: evt.Text}
	case agent.EventToolCallStart:
		return MsgToolCallStart{NodeID: nodeID, ToolName: evt.ToolName}
	case agent.EventToolCallEnd:
		return MsgToolCallEnd{
			NodeID:   nodeID,
			ToolName: evt.ToolName,
			Output:   evt.ToolOutput,
			Error:    evt.ToolError,
		}
	case agent.EventError:
		errMsg := ""
		if evt.Err != nil {
			errMsg = evt.Err.Error()
		}
		return MsgAgentError{NodeID: nodeID, Error: errMsg}
	default:
		return nil
	}
}

// AdaptLLMTraceEvent maps an LLM trace event to one or more typed TUI messages.
// Some trace events produce multiple messages (e.g. TraceRequestStart emits both
// MsgLLMRequestStart and MsgThinkingStarted). Returns nil for filtered events.
func AdaptLLMTraceEvent(evt llm.TraceEvent, nodeID string, verbose bool) []tea.Msg {
	switch evt.Kind {
	case llm.TraceRequestStart:
		return []tea.Msg{
			MsgLLMRequestStart{NodeID: nodeID, Provider: evt.Provider, Model: evt.Model},
			MsgThinkingStarted{NodeID: nodeID},
		}
	case llm.TraceText:
		return []tea.Msg{
			MsgTextChunk{NodeID: nodeID, Text: evt.Preview},
			MsgThinkingStopped{NodeID: nodeID},
		}
	case llm.TraceReasoning:
		return []tea.Msg{
			MsgReasoningChunk{NodeID: nodeID, Text: evt.Preview},
			MsgThinkingStopped{NodeID: nodeID},
		}
	case llm.TraceFinish:
		return []tea.Msg{
			MsgLLMFinish{NodeID: nodeID},
			MsgThinkingStopped{NodeID: nodeID},
		}
	case llm.TraceToolPrepare:
		return []tea.Msg{
			MsgToolCallStart{NodeID: nodeID, ToolName: evt.ToolName},
			MsgThinkingStopped{NodeID: nodeID},
		}
	case llm.TraceProviderRaw:
		if !verbose {
			return nil
		}
		return []tea.Msg{
			MsgLLMProviderRaw{NodeID: nodeID, Data: evt.Preview},
		}
	default:
		return nil
	}
}
