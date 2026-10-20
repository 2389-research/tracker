// ABOUTME: Human-gate handling specific to pause mode: a paused gate's
// ABOUTME: gate_resolved event, and failing closed where pausing is unsupported.
package handlers

import (
	"context"
	"errors"

	"github.com/2389-research/tracker/pipeline"
)

// subgraphPauseRefusal is the failure reason for a gate inside a subgraph or
// manager_loop child in pause mode. Re-running the subgraph on resume would
// regenerate the question on every answer, so v1 refuses instead of pausing.
const subgraphPauseRefusal = "a gate inside a subgraph can't pause yet — answer it interactively, or use --auto-approve / --autopilot"

// refuseSubgraphPause fails a pause-mode gate that runs inside a subgraph or
// manager_loop child: a routable, non-retryable OutcomeFail. ok is false when
// the gate may proceed.
func (h *HumanHandler) refuseSubgraphPause(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext, gate *pipeline.GateDetail, actor pipeline.Actor) (pipeline.Outcome, bool) {
	gp, pauses := h.interviewer.(GatePauser)
	if !pauses || !gp.PausesGates() || len(pipeline.SubgraphScope(ctx)) == 0 {
		return pipeline.Outcome{}, false
	}
	out := pipeline.Outcome{Status: pipeline.OutcomeFail, FailureReason: subgraphPauseRefusal, OverrideActor: actor}
	h.emitGateResolved(node, pctx, gate, out, actor, false, errors.New(subgraphPauseRefusal))
	return out, true
}

// emitGatePaused emits gate_resolved for a gate that paused the run instead of
// being answered (Resolution "paused", no Error), keeping gate_opened /
// gate_resolved paired. It reports whether err was such a pause.
func (h *HumanHandler) emitGatePaused(node *pipeline.Node, pctx *pipeline.PipelineContext, gate *pipeline.GateDetail, actor pipeline.Actor, err error) bool {
	var pe *pipeline.PauseError
	if !errors.As(err, &pe) || pe.Status != pipeline.OutcomePausedGate {
		return false
	}
	if h.emitter == nil || gate == nil {
		return true
	}
	resolved := &pipeline.GateDetail{
		GateID: gate.GateID, Mode: gate.Mode, Label: gate.Label, Actor: actor,
		Resolution: pipeline.GateResolutionPaused,
	}
	if len(pe.Gates) > 0 {
		resolved.Fingerprint = pe.Gates[0].Fingerprint
	}
	h.emit(node, pctx, pipeline.EventGateResolved, resolved, nil)
	return true
}
