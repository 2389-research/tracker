// ABOUTME: Gate lifecycle payload types — GateDetail on gate_opened/gate_resolved (#509),
// ABOUTME: plus the structured GateOption / GateQuestion a front-end renders from (#631).
package pipeline

// GateMaxPromptBytes caps the resolved gate prompt carried on GateDetail.
// A gate prompt can embed an entire upstream agent response (the human handler
// appends ctx.last_response), and this payload lands on every activity.jsonl
// line for the gate — so the tail beyond the cap is dropped. Consumers that
// need the full text read it from the gate node's prompt plus context.
const GateMaxPromptBytes = 4096

// GateDetail is the payload for EventGateOpened and EventGateResolved (#509).
//
// GateID correlates the pair: generated when the gate opens, repeated on the
// resolution, scoped to one open/resolve pair (a loop re-entry gets a fresh ID).
// Open-time fields (Mode, Label, Prompt, Choices) describe the question;
// resolve-time fields (Response, Outcome, Actor, TimedOut, Error) describe the
// answer. The two sets are disjoint in practice, but Mode is repeated on the
// resolution so a consumer can interpret Response without joining.
type GateDetail struct {
	GateID string `json:"gate_id"`
	// Mode is the gate's HumanConfig mode: "choice" (the default), "freeform",
	// "yes_no", or "interview".
	Mode string `json:"mode,omitempty"`
	// Label is the gate node's short title (node.Label) — the question, without
	// the appended prompt body or upstream context.
	Label string `json:"label,omitempty"`
	// Prompt is the fully resolved prompt shown to the responder: label, plus
	// the authored prompt body, plus upstream context, after variable
	// expansion. Truncated to GateMaxPromptBytes.
	Prompt string `json:"prompt,omitempty"`
	// Choices are the selectable options derived from outgoing edge labels.
	// Empty for an unlabeled freeform gate; ["Yes","No"] for yes_no mode.
	// Kept for consumers that predate Options; the two are always in lockstep.
	Choices []string `json:"choices,omitempty"`
	// Default is the option an unattended run (--auto-approve / timeout_action
	// default) picks: the node's declared `default:`, or "Yes" in yes_no mode.
	// Empty when the gate declares none.
	Default string `json:"default,omitempty"`
	// Options is the structured form of Choices (#631): one entry per outgoing
	// labeled edge (or the fixed pair in yes_no mode), carrying the routing
	// target, the default flag, and the edge's override/restart facts, so a
	// consumer renders buttons from structure instead of parsing Prompt.
	// Nil for an unlabeled freeform gate and for interview mode.
	Options []GateOption `json:"options,omitempty"`
	// Question is the parsed question set for an interview-mode gate — what the
	// responder is actually shown, which in that mode is NOT Prompt (the
	// interview handler passes parsed questions to AskInterview and never shows
	// the node prompt). Nil for every other mode, and for an interview gate
	// that parsed zero questions and fell back to freeform against Prompt.
	Question []GateQuestion `json:"questions,omitempty"`
	// Response is what came back: the selected choice/label in choice and
	// yes_no modes, the entered text in freeform mode, or the markdown answer
	// summary in interview mode. Empty when the gate failed to resolve.
	Response string `json:"response,omitempty"`
	// Outcome is the gate's resulting Outcome.Status ("success" / "fail").
	Outcome string `json:"outcome,omitempty"`
	// Actor is the interviewer classification that answered (human, autopilot,
	// webhook, unknown) — the same value carried on OverrideDetail.Actor.
	Actor Actor `json:"actor,omitempty"`
	// TimedOut is true when the gate's timeout fired and the timeout_action
	// decided the outcome rather than a responder.
	TimedOut bool `json:"timed_out,omitempty"`
	// Error is non-empty when the gate failed to collect an answer at all
	// (e.g. the bound interviewer does not support the node's mode).
	Error string `json:"error,omitempty"`
}

// Gate option meanings (#631). Derived by the engine so every front-end
// renders the same semantics the engine routes on: GateMeaningReject is the
// same classification that makes a gate → exit selection terminate the run
// `fail` (#633); GateMeaningApprove covers an audited `override: true` edge
// and the small affirmative label set. An empty Meaning is a neutral option
// (e.g. "adjust", "retry").
const (
	GateMeaningApprove = "approve"
	GateMeaningReject  = "reject"
)

// GateOption is one selectable option of a human gate, derived from the
// node's outgoing edges and its declared default (#631). Label is what the
// responder answers with (the edge label — the same string Choices carries).
type GateOption struct {
	Label string `json:"label"`
	// Choice is the edge's stable DIP150 routing key when it declares one; the
	// engine routes on it instead of Label. A responder may answer with either.
	Choice string `json:"choice,omitempty"`
	// Target is the node this option routes to (edge.To). Empty for the fixed
	// yes_no pair, which routes on ctx.outcome rather than a labeled edge.
	Target string `json:"target,omitempty"`
	// Default is true for the option an unattended run picks.
	Default bool `json:"default,omitempty"`
	// Override is true when the edge is an audited validation override.
	Override bool `json:"override,omitempty"`
	// Restart is true when the edge loops back (`restart: true`) — an
	// "adjust and try again" option rather than a forward one.
	Restart bool `json:"restart,omitempty"`
	// Meaning is GateMeaningApprove, GateMeaningReject, or "" (neutral).
	Meaning string `json:"meaning,omitempty"`
}

// GateQuestion is one question of an interview-mode gate, as parsed from the
// upstream agent's output and presented to the responder. ID matches the answer
// ID in the resolved interview result ("q<index>"), so a consumer can map an
// answer back to the question that produced it.
type GateQuestion struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Options []string `json:"options,omitempty"`
	IsYesNo bool     `json:"is_yes_no,omitempty"`
}
