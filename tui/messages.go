// ABOUTME: All typed Bubbletea message constants for the TUI.
// ABOUTME: Components communicate exclusively through these messages — no string comparisons.
package tui

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
type MsgPipelineCompleted struct{}
type MsgPipelineFailed struct{ Error string }

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

// LLM provider messages.
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
	ReplyCh chan<- string
}

// UI tick and interaction messages.
type MsgThinkingTick struct{}
type MsgHeaderTick struct{}
type MsgToggleExpand struct{}
type MsgModalDismiss struct{}
type MsgPipelineDone struct{ Err error }
