// ABOUTME: Optional GateAware side-interface — the callback side of #509 gate
// ABOUTME: identity, letting an out-of-process transport correlate an Ask* call.
package handlers

import (
	"context"

	"github.com/2389-research/tracker/pipeline"
)

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
	// Default and Options are the structured options the gate presents (#631)
	// — the same values carried on the gate_opened event, so an out-of-process
	// transport can render buttons without parsing the prompt.
	Default string
	Options []pipeline.GateOption
	// Prompt is the full rendered prompt (label, body, upstream context),
	// untruncated.
	Prompt string
	// ScopePath locates the gate: enclosing subgraph node IDs, then the
	// parallel branch target (unless the gate is that target), outermost
	// first. Empty at the top level.
	ScopePath []string
	// BranchIndex is the gate's parallel branch index (0 outside a branch).
	BranchIndex int
}

type gateInfoKey struct{}

// WithGateInfo returns ctx carrying the gate's identity. The human handler
// passes it on every Ask*Context call, so an interviewer serving concurrent
// gates (parallel branches) reads the identity of the gate it is answering
// from the call itself rather than from a shared BeginGate.
func WithGateInfo(ctx context.Context, info GateInfo) context.Context {
	return context.WithValue(ctx, gateInfoKey{}, info)
}

// GateInfoFrom returns the GateInfo the human handler put on ctx.
func GateInfoFrom(ctx context.Context) (GateInfo, bool) {
	info, ok := ctx.Value(gateInfoKey{}).(GateInfo)
	return info, ok
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
func (h *HumanHandler) beginGate(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext, prompt string) (*pipeline.GateDetail, GateInfo) {
	gateID := newGateID()
	mode := gateMode(node)
	info := h.gateInfo(ctx, node, pctx, gateID, mode, prompt)
	if ga, ok := h.interviewer.(GateAware); ok {
		ga.BeginGate(info)
	}
	return h.emitGateOpened(node, pctx, prompt, gateID, mode), info
}

// gateInfo builds the identity of the gate about to be presented, handed to a
// GateAware interviewer and carried on the gate call's context.
func (h *HumanHandler) gateInfo(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext, gateID, mode, prompt string) GateInfo {
	var runID string
	if pctx != nil {
		runID, _ = pctx.GetInternal(pipeline.InternalKeyRunID)
	}
	def, opts := h.gateOptions(node, mode)
	scope := pipeline.SubgraphScope(ctx)
	branchID, branchIndex, inBranch := pipeline.BranchScope(pctx)
	// A gate that is itself the branch target is already unique by node ID;
	// its branch adds nothing to the scope.
	if inBranch && branchID != node.ID {
		scope = append(scope, branchID)
	}
	if len(scope) == 0 {
		scope = nil
	}
	return GateInfo{
		RunID:       runID,
		NodeID:      node.ID,
		GateID:      gateID,
		Mode:        mode,
		Label:       node.Label,
		Default:     def,
		Options:     opts,
		Prompt:      prompt,
		ScopePath:   scope,
		BranchIndex: branchIndex,
	}
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
