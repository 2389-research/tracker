// ABOUTME: Gate lifecycle event emission for the human gate handler (#509).
// ABOUTME: Emits gate_opened/gate_resolved pairs correlated by GateDetail.GateID.
package handlers

import (
	"time"
	"unicode/utf8"

	"github.com/2389-research/tracker/pipeline"
)

// HumanOption configures a HumanHandler.
type HumanOption func(*HumanHandler)

// WithHumanPipelineEmitter makes the handler emit gate lifecycle events
// (EventGateOpened / EventGateResolved) around every interviewer call (#509).
// Without it the handler emits nothing, which is what every non-engine caller
// (tests, embedders constructing the handler directly) gets by default.
func WithHumanPipelineEmitter(e pipeline.PipelineEventHandler) HumanOption {
	return func(h *HumanHandler) { h.emitter = e }
}

// emitGateOpened emits EventGateOpened and returns the GateDetail whose GateID
// correlates the matching resolution (#509). Returns nil when no emitter is
// configured, which makes every emit* call below a no-op.
func (h *HumanHandler) emitGateOpened(node *pipeline.Node, prompt string) *pipeline.GateDetail {
	if h.emitter == nil {
		return nil
	}
	mode := node.HumanConfig().Mode
	if mode == "" {
		mode = "choice"
	}
	gate := &pipeline.GateDetail{
		GateID:  newGateID(),
		Mode:    mode,
		Label:   node.Label,
		Prompt:  truncateGatePrompt(prompt),
		Choices: h.gateChoices(node, mode),
	}
	h.emitter.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventGateOpened,
		NodeID:    node.ID,
		Timestamp: time.Now(),
		Gate:      gate,
	})
	return gate
}

// emitGateResolved emits EventGateResolved carrying the same GateID as the
// matching gate_opened. Called on every exit path — success, fail, timeout, and
// interviewer error — so a stream consumer never sees a gate stay open.
func (h *HumanHandler) emitGateResolved(node *pipeline.Node, gate *pipeline.GateDetail, outcome pipeline.Outcome, actor pipeline.Actor, timedOut bool, err error) {
	if h.emitter == nil || gate == nil {
		return
	}
	resolved := &pipeline.GateDetail{
		GateID:   gate.GateID,
		Mode:     gate.Mode,
		Label:    gate.Label,
		Response: gateResponseOf(node, outcome),
		Outcome:  string(outcome.Status),
		Actor:    actor,
		TimedOut: timedOut,
	}
	if err != nil {
		resolved.Error = err.Error()
	}
	h.emitter.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventGateResolved,
		NodeID:    node.ID,
		Timestamp: time.Now(),
		Gate:      resolved,
		Err:       err,
	})
}

// gateChoices returns the options presented to the responder for this mode:
// the fixed pair for yes_no, otherwise the outgoing edge labels (empty for an
// unlabeled freeform gate).
func (h *HumanHandler) gateChoices(node *pipeline.Node, mode string) []string {
	if mode == "yes_no" {
		return []string{"Yes", "No"}
	}
	return collectEdgeLabels(h.graph, node.ID)
}

// gateResponseOf extracts what the responder actually returned from the gate's
// outcome: the per-node human response when the mode recorded one (freeform,
// interview, timeout-default), else the routing label selected in choice and
// yes_no modes.
func gateResponseOf(node *pipeline.Node, outcome pipeline.Outcome) string {
	if v, ok := outcome.ContextUpdates[pipeline.ContextKeyResponsePrefix+node.ID]; ok && v != "" {
		return v
	}
	return outcome.PreferredLabel
}

// truncateGatePrompt caps the prompt carried on GateDetail at
// pipeline.GateMaxPromptBytes. See GateMaxPromptBytes for why.
func truncateGatePrompt(prompt string) string {
	if len(prompt) <= pipeline.GateMaxPromptBytes {
		return prompt
	}
	// Back off to a rune boundary so the payload stays valid UTF-8 (at most
	// three iterations).
	cut := prompt[:pipeline.GateMaxPromptBytes]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
