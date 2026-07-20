// ABOUTME: Transport-neutral gate types for the Slack bot's human-gate bridge.
// ABOUTME: ThreadUI is the seam between gate logic and the Slack Block Kit layer.
package main

// GateKind classifies how a human gate is presented in a thread.
type GateKind string

const (
	GateChoice   GateKind = "choice"   // pick one of N labels (buttons)
	GateYesNo    GateKind = "yes_no"   // a fixed Yes/No decision
	GateFreeform GateKind = "freeform" // open-ended text reply
)

// Gate describes a human decision the pipeline is blocked on, for presentation
// in a conversation thread. One SlackInterviewer serves one thread, so a Gate
// carries no thread id — the ThreadUI it is posted through is already bound to
// its thread. ID correlates the eventual answer back to the blocked call.
// Interview gates are decomposed into a sequence of these by the interviewer.
type Gate struct {
	ID      string
	Kind    GateKind
	Prompt  string
	Choices []string // GateChoice / GateYesNo: selectable labels
	Default string   // preferred label, if any
}

// GateAnswer carries a human's response back to the blocked pipeline. Exactly
// one field is meaningful per gate kind; Canceled short-circuits both.
type GateAnswer struct {
	Choice   string // GateChoice / GateYesNo / labeled freeform
	Freeform string // GateFreeform / labeled freeform "other"
	Canceled bool
}

// ThreadUI presents gates and messages in a single conversation thread. It is
// the seam between the transport-neutral gate logic (SlackInterviewer, runner)
// and the Slack Block Kit layer (slack.go) — so the logic is testable without a
// live Slack connection. Implementations are bound to one thread.
type ThreadUI interface {
	// PostGate renders a gate to the thread; gate.ID correlates the answer.
	PostGate(gate Gate) error
	// Post sends a plain notification/message to the thread.
	Post(text string) error
}
