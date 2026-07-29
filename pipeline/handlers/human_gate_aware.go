// ABOUTME: Optional GateAware side-interface — the callback side of #509 gate
// ABOUTME: identity, letting an out-of-process transport correlate an Ask* call.
package handlers

import "github.com/2389-research/tracker/pipeline"

// GateInfo carries the identity of a gate the handler is about to present,
// mirroring the #509 gate_opened event fields. A GateAware interviewer receives
// it immediately before the matching Ask* call, so an out-of-process transport
// (Slack, a web gate) can key a pending-gate record on GateID and match the
// eventual answer back to the gate_opened event carrying the same GateID.
type GateInfo struct {
	// RunID is the active run (pipeline.InternalKeyRunID); "" when unset.
	RunID string
	// NodeID is the gate node — the same NodeID on the gate_opened event.
	NodeID string
	// GateID is the SAME id carried on the gate_opened event for this gate.
	GateID string
	// Mode is the gate's HumanConfig mode ("choice", "freeform", "yes_no",
	// "interview"), defaulting to "choice" — matching gate_opened's Mode.
	Mode string
	// Label is the gate node's short title (node.Label).
	Label string
}

// GateAware is an optional interviewer side-interface. When an interviewer
// implements it, HumanHandler calls BeginGate immediately before invoking any
// Ask* method, handing over the identity of the gate it is about to answer. The
// GateID equals the one on the gate_opened event for the same gate, so a
// transport can correlate the two. Interviewers that do not implement GateAware
// behave exactly as before — the handler gates the call behind a type assertion,
// mirroring the existing Actor()/Cancel()/ContextSetter optional interfaces.
type GateAware interface {
	BeginGate(info GateInfo)
}

// beginGate is called once per gate presentation, immediately before any Ask*
// method. It mints the single gate id shared by both the gate_opened event and
// the optional GateAware.BeginGate callback, notifies a GateAware interviewer,
// then emits EventGateOpened (a no-op when no emitter is wired). It returns the
// GateDetail used to correlate the later resolution, or nil when no emitter is
// configured. The GateAware callback fires whether or not an emitter is attached.
func (h *HumanHandler) beginGate(node *pipeline.Node, pctx *pipeline.PipelineContext, prompt string) *pipeline.GateDetail {
	gateID := newGateID()
	mode := gateMode(node)
	h.notifyGateAware(node, pctx, gateID, mode)
	return h.emitGateOpened(node, pctx, prompt, gateID, mode)
}

// notifyGateAware hands the gate identity to the interviewer when it implements
// GateAware. A no-op otherwise, so plain interviewers are unaffected.
func (h *HumanHandler) notifyGateAware(node *pipeline.Node, pctx *pipeline.PipelineContext, gateID, mode string) {
	ga, ok := h.interviewer.(GateAware)
	if !ok {
		return
	}
	var runID string
	if pctx != nil {
		runID, _ = pctx.GetInternal(pipeline.InternalKeyRunID)
	}
	ga.BeginGate(GateInfo{
		RunID:  runID,
		NodeID: node.ID,
		GateID: gateID,
		Mode:   mode,
		Label:  node.Label,
	})
}

// gateMode returns the gate's HumanConfig mode, defaulting to "choice". Shared
// so the GateAware callback and the gate_opened event report the same mode.
func gateMode(node *pipeline.Node) string {
	mode := node.HumanConfig().Mode
	if mode == "" {
		mode = "choice"
	}
	return mode
}
