// ABOUTME: Shared run_id stamping for handler-originated pipeline events.
// ABOUTME: Keeps every handler emitter in parity with the gate handler (audit finding 3).
package handlers

import "github.com/2389-research/tracker/pipeline"

// stampRunID sets evt.RunID from the active run recorded on pctx
// (pipeline.InternalKeyRunID) when the event does not already carry one, then
// returns it. Handler-originated events bypass Engine.emit (which stamps
// RunID: s.runID itself), so without this they reach the wire with an empty
// run_id and are unattributable when one event handler serves concurrent runs.
// A non-empty RunID is never overwritten, and a nil pctx leaves it empty.
// Mirrors HumanHandler.emit so the two cannot diverge.
func stampRunID(evt pipeline.PipelineEvent, pctx *pipeline.PipelineContext) pipeline.PipelineEvent {
	if evt.RunID == "" && pctx != nil {
		evt.RunID, _ = pctx.GetInternal(pipeline.InternalKeyRunID)
	}
	return evt
}
