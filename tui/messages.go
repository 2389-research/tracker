// ABOUTME: Message types for mode-2 gate requests sent from BubbleteaInterviewer to
// ABOUTME: the running TUI program. These document the protocol between the interviewer
// ABOUTME: and the dashboard app without creating an import cycle.
package tui

// GateChoiceRequestMsg is a documentation/test-facing alias for the gate choice
// request message. In mode 2, BubbleteaInterviewer sends dashboard.GateChoiceMsg
// (which is equivalent) via tea.Program.Send().
//
// Fields mirror dashboard.GateChoiceMsg exactly so that either type can be used
// interchangeably when writing tests that exercise the tui package directly.
type GateChoiceRequestMsg struct {
	Prompt        string
	Choices       []string
	DefaultChoice string
	ReplyCh       chan string
}

// GateFreeformRequestMsg is a documentation/test-facing alias for the gate freeform
// request message. In mode 2, BubbleteaInterviewer sends dashboard.GateFreeformMsg
// (which is equivalent) via tea.Program.Send().
type GateFreeformRequestMsg struct {
	Prompt  string
	ReplyCh chan string
}
