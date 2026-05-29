// ABOUTME: All typed Bubbletea message constants for the TUI.
// ABOUTME: Components communicate exclusively through these messages — no string comparisons.
package tui

import (
	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
)

// Pipeline lifecycle messages.
type MsgNodeStarted struct{ NodeID string }
type MsgNodeCompleted struct {
	NodeID  string
	Outcome string
}
type MsgNodeFailed struct {
	NodeID string
	Error  string
}
type MsgNodeRetrying struct {
	NodeID  string
	Message string
}

// MsgPipelineCompleted is emitted when the pipeline reaches the success exit.
// Status carries the terminal status so the TUI's completion row can render
// validation_overridden in amber (per Gap 5.2 spec D17 + D18) instead of a
// generic green checkmark. Override is non-nil when overrides fired during the
// run, carrying the headline (latest) entry per spec D5a.
//
// When the message comes from the free-function AdaptPipelineEvent (the
// stateless adapter used in tests / one-shot conversions), Status defaults to
// the zero value and Override is nil — the StateStore reconstructs Status
// from accumulated MsgValidationOverridden messages it has seen during the
// run. The stateful Adapter populates these fields at construction time so
// downstream consumers don't have to re-derive them.
type MsgPipelineCompleted struct {
	Status   pipeline.TerminalStatus
	Override *pipeline.OverrideDetail // headline entry (latest) per D5a; non-nil for override runs
}
type MsgPipelineFailed struct{ Error string }

// MsgValidationOverridden carries a single override-edge traversal so the
// StateStore can accumulate overrides for the completion row's gate/label/actor
// display and so live UI surfaces (e.g. the activity log) can flag the moment.
// Mapped from pipeline.EventValidationOverridden by the adapter.
type MsgValidationOverridden struct {
	NodeID string                  // the gate node that produced the override
	Detail pipeline.OverrideDetail // gate/label/actor/subgraph_path
}

// Agent activity messages.
type MsgThinkingStarted struct{ NodeID string }
type MsgThinkingStopped struct{ NodeID string }
type MsgTextChunk struct {
	NodeID string
	Text   string
}
type MsgReasoningChunk struct {
	NodeID string
	Text   string
}
type MsgToolCallStart struct {
	NodeID    string
	ToolName  string
	ToolInput string
}
type MsgToolCallEnd struct {
	NodeID   string
	ToolName string
	Output   string
	Error    string
}
type MsgAgentError struct {
	NodeID string
	Error  string
}
type MsgVerifyStatus struct {
	NodeID string
	Text   string
}

// LLM provider messages.
type MsgLLMRequestPreparing struct {
	NodeID   string
	Provider string
	Model    string
}
type MsgLLMRequestStart struct {
	NodeID   string
	Provider string
	Model    string
}
type MsgLLMFinish struct{ NodeID string }
type MsgLLMProviderRaw struct {
	NodeID string
	Data   string
}

// Gate (human-in-the-loop) messages.
type MsgGateChoice struct {
	NodeID  string
	Prompt  string
	Options []string
	ReplyCh chan<- string
}
type MsgGateFreeform struct {
	NodeID  string
	Prompt  string
	Labels  []string // outgoing edge labels (e.g., "approve", "adjust", "reject")
	Default string   // default label (pre-selected)
	ReplyCh chan<- string
}
type MsgGateInterview struct {
	NodeID    string
	Questions []handlers.Question
	Previous  *handlers.InterviewResult
	ReplyCh   chan<- string // JSON string
}

// UI tick and interaction messages.
type MsgThinkingTick struct{}
type MsgHeaderTick struct{}
type MsgToggleExpand struct{}
type MsgModalDismiss struct{}
type MsgPipelineDone struct{ Err error }

// Verbosity cycling for the agent log filter.
type MsgCycleVerbosity struct{}

// Search activation and control.
type MsgSearchActivate struct{}
type MsgSearchDeactivate struct{}
type MsgSearchUpdate struct{ Term string }

// Node drill-down focus.
type MsgFocusNode struct{ NodeID string }
type MsgClearFocus struct{}

// Status bar flash messages (e.g., "Copied!").
type MsgStatusFlash struct{ Text string }
type MsgStatusFlashClear struct{}
