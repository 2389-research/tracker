// ABOUTME: Resume entry-point policy (#651): rewind a halted run to the node whose failure routed it
// ABOUTME: into a dead end, or to an explicit --from node, instead of re-entering the terminal.
package pipeline

import (
	"fmt"
	"sort"
	"time"
)

// EventResumeRewound fires once at resume when the run re-enters somewhere
// other than the checkpoint's CurrentNode (#651): an automatic rewind past a
// fail-closed terminal to the node that failed, or an explicit --from. NodeID
// is the entry node; Decision carries EdgeFrom (the halted node), EdgeTo,
// ClearedNodes, RewindReason, and OutcomeStatus (the origin's recorded
// outcome). Declared here rather than in events.go to keep that file under
// the size gate; it is a PipelineEventType like every other event.
const EventResumeRewound PipelineEventType = "resume_rewound"

// ResumePolicy configures how a checkpoint resume picks its entry node (#651).
// The zero value is the default: rewind automatically when the run halted at a
// fail-routed target (see Engine.resumeEntryNode); otherwise resume exactly at
// the checkpoint's CurrentNode as before.
type ResumePolicy struct {
	// From names a node to restart from. It must exist in the graph and be
	// either completed or the checkpoint's CurrentNode; the node and everything
	// reachable from it are un-completed (fresh retry budgets, fallback latches
	// re-armed) and the run re-enters there. Refused with a clear error
	// otherwise. Takes precedence over the automatic rewind.
	From string
	// NoRewind disables the automatic rewind: the run re-enters exactly at
	// CurrentNode (today's behavior — a run halted at a fail-closed terminal
	// re-runs the terminal and fails again).
	NoRewind bool
}

// WithResumePolicy sets the resume entry-point policy (#651). Ignored for a
// fresh (non-resume) run.
func WithResumePolicy(p ResumePolicy) EngineOption {
	return func(e *Engine) {
		e.resumePolicy = p
	}
}

// rewindSkipHandlers are origin handlers the automatic rewind refuses to
// re-run (#651): a human gate's failure is a decision (e.g. yes_no "No" /
// abandon), not a transient fault to retry behind the operator's back; and a
// parallel / subgraph / manager loop may have partially completed child work
// the parent checkpoint cannot see, so re-running it is not obviously safe.
// `--from` still reaches these — that is an explicit operator instruction.
var rewindSkipHandlers = map[string]string{
	"wait.human":         "a human gate — its failure was a decision, not a transient fault",
	"parallel":           "a parallel node whose branches may have partially completed",
	"subgraph":           "a subgraph whose child run may have partially completed",
	"stack.manager_loop": "a manager loop whose child runs may have partially completed",
}

// validateResumePolicy checks an explicit From against the loaded checkpoint
// before any node runs, so a typo fails closed with a clear error rather than
// silently resuming somewhere else. No-op without From or on a fresh run.
func (e *Engine) validateResumePolicy(cp *Checkpoint) error {
	from := e.resumePolicy.From
	if from == "" {
		return nil
	}
	if cp.CurrentNode == "" && len(cp.CompletedNodes) == 0 {
		return fmt.Errorf("resume --from %q: no checkpoint to resume — --from requires a resumed run (-r)", from)
	}
	if _, ok := e.graph.Nodes[from]; !ok {
		return fmt.Errorf("resume --from %q: node does not exist in the graph", from)
	}
	if !cp.IsCompleted(from) && from != cp.CurrentNode && from != cp.HaltedAt {
		return fmt.Errorf("resume --from %q: node was never reached in this run (not completed and not the current node %q)", from, cp.CurrentNode)
	}
	return nil
}

// resumePlan is the resume decision (#651), computed without mutating the
// checkpoint so both context compaction (which pins the ENTRY node's declared
// reads) and the entry itself agree on where the run re-enters.
type resumePlan struct {
	entry   string                // node the run re-enters
	halted  string                // cp.HaltedAt (consumed on entry), "" if none
	origin  *FallbackOriginRecord // provenance driving an automatic rewind
	reason  string                // rewind reason for the event; "" = no rewind
	refusal string                // why an automatic rewind was NOT taken; "" = n/a
}

// planResume decides where a resume re-enters (#651). Precedence:
//
//  1. ResumePolicy.From — explicit operator rewind (validated earlier).
//  2. Automatic rewind, unless NoRewind: the run halted AT a node that was
//     reached by fail-routing (FallbackOrigin) AND is a true dead end
//     (isFailDeadEnd, #654) — e.g. build_product's Setup -> AbortRun — so the
//     terminal would only fail again. Rewind to the origin so the failed step
//     is retried with its cause presumably fixed. Refused (with a warning at
//     entry) for a human-gate / parallel / subgraph origin.
//  3. Otherwise CurrentNode as before — including a fail-routed node that is
//     NOT a dead end (`Test -> Fix when fail`, `Fix -> Test`, Fix died
//     transiently): Fix is re-run in place, not its origin Test (#654).
func (e *Engine) planResume(cp *Checkpoint) resumePlan {
	plan := resumePlan{entry: cp.CurrentNode, halted: cp.HaltedAt}
	if from := e.resumePolicy.From; from != "" {
		plan.entry, plan.reason = from, "explicit --from"
		return plan
	}
	if e.resumePolicy.NoRewind || plan.halted == "" || !e.isFailDeadEnd(plan.halted) {
		return plan
	}
	origin, ok := cp.GetFallbackOrigin(plan.halted)
	if !ok {
		return plan
	}
	if why := e.rewindRefusal(origin); why != "" {
		plan.refusal = why
		return plan
	}
	plan.entry = origin.Node
	plan.origin = &origin
	plan.reason = fmt.Sprintf("halted at %q, reached via %s from failed node %q", plan.halted, origin.Kind, origin.Node)
	return plan
}

// resumeEntryNode applies the resume plan (#651) and returns the node the run
// (re-)enters. For a fresh run it is the start node. For a resume it consumes
// the checkpoint's HaltedAt marker (the halted node is un-completed so it
// re-executes, exactly as before — OutcomeFail marks completion before
// routing, and skipping a completed terminal through its edge would fabricate
// a success), then rewinds if the plan says so. Every rewind is persisted
// immediately so a second kill lands on a consistent checkpoint.
func (e *Engine) resumeEntryNode(s *runState) string {
	cp := s.cp
	if cp.CurrentNode == "" {
		return e.graph.StartNode
	}
	plan := e.planResume(cp)
	cp.HaltedAt = ""
	if plan.halted != "" && plan.halted == cp.CurrentNode {
		cp.ClearCompleted(plan.halted)
	}
	if plan.refusal != "" {
		origin, _ := cp.GetFallbackOrigin(plan.halted)
		e.emit(PipelineEvent{
			Type:      EventWarning,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    plan.halted,
			Message: fmt.Sprintf("resume: run halted at %q (reached from failed node %q) but not rewinding: %s — resuming at %q; use --from to override",
				plan.halted, origin.Node, plan.refusal, plan.halted),
		})
	}
	if plan.reason != "" {
		e.rewindTo(s, plan.halted, plan.entry, plan.reason, plan.origin)
	}
	return plan.entry
}

// isFailDeadEnd reports whether a halted node is a fail-closed terminal the
// automatic rewind may step past (#654): a designated failure sink — the
// graph-level `on_failure` / `fallback_target` / `fallback_retry_target`, or
// any node's `fallback_target` / `fallback_retry_target` — or a node whose
// only continuation is the exit node (no outgoing edges, or every edge leads
// to ExitNode). A fail-routed node with real onward routing (a Fix step that
// loops back to Test) is not a dead end: its failure is retried in place.
func (e *Engine) isFailDeadEnd(nodeID string) bool {
	// A declared fallback sink that has real onward routing (build_product's
	// EscalateMilestone/EscalateReview) is NOT a dead end — re-entering it is
	// meaningful, so resume there rather than rewinding past it (#654 review).
	return e.onlyContinuesToExit(nodeID)
}

// onlyContinuesToExit reports whether every outgoing edge of nodeID leads to
// the exit node (vacuously true with no outgoing edges).
func (e *Engine) onlyContinuesToExit(nodeID string) bool {
	for _, edge := range e.graph.OutgoingEdges(nodeID) {
		if edge.To != e.graph.ExitNode {
			return false
		}
	}
	return true
}

// rewindRefusal returns a non-empty reason when the automatic rewind must not
// re-run origin (see rewindSkipHandlers), or when the origin no longer exists
// in the (possibly edited) graph.
func (e *Engine) rewindRefusal(origin FallbackOriginRecord) string {
	node, ok := e.graph.Nodes[origin.Node]
	if !ok {
		return fmt.Sprintf("origin node %q no longer exists in the graph", origin.Node)
	}
	if why, skip := rewindSkipHandlers[node.Handler]; skip {
		return fmt.Sprintf("origin %q is %s", origin.Node, why)
	}
	return ""
}

// rewindTo re-enters the run at target (#651): target and everything
// reachable from it are un-completed (stale edge selections dropped), their
// retry counters and one-shot fallback latches reset so the retried step gets
// a fresh budget, the halted terminal (if any) is dropped too, and
// CurrentNode moves to target. RestartCounts are deliberately untouched — a
// rewind is operator-initiated recovery, not a loop iteration, and the
// max_restarts ceiling must keep counting the loop's real restarts (#603).
// Emits EventResumeRewound and persists the checkpoint.
func (e *Engine) rewindTo(s *runState, halted, target, reason string, origin *FallbackOriginRecord) {
	cp := s.cp
	cleared := e.rewindClearSet(cp, halted, target)
	for _, id := range cleared {
		cp.ClearCompleted(id)
		delete(cp.RetryCounts, id)
		cp.ClearFallbackTaken(id)
	}
	if halted != "" {
		cp.ClearFallbackOrigin(halted)
	}
	cp.CurrentNode = target
	detail := &DecisionDetail{
		EdgeFrom:     halted,
		EdgeTo:       target,
		ClearedNodes: cleared,
		RewindReason: reason,
	}
	if origin != nil {
		detail.OutcomeStatus = origin.Outcome
	}
	e.emit(PipelineEvent{
		Type:      EventResumeRewound,
		Timestamp: time.Now(),
		RunID:     s.runID,
		NodeID:    target,
		Message:   fmt.Sprintf("resume: rewinding to %q (%s); %d node(s) un-completed", target, reason, len(cleared)),
		Decision:  detail,
	})
	e.saveCheckpoint(cp, s.pctx, s.runID)
}

// rewindClearSet returns, sorted, the node IDs a rewind to target invalidates:
// target plus everything reachable from it, and the halted node plus its
// downstream (a fail-closed terminal is usually NOT reachable from the origin
// through graph edges — `on_failure` is an attribute, not an edge). Besides
// target and halted themselves, only nodes with checkpoint state (completed, a
// retry count, or a latch) are listed so the event names what actually changed.
func (e *Engine) rewindClearSet(cp *Checkpoint, halted, target string) []string {
	set := map[string]bool{}
	e.addWithDownstream(set, target)
	if halted != "" {
		e.addWithDownstream(set, halted)
	}
	var out []string
	for id := range set {
		if id == target || id == halted || cp.IsCompleted(id) || cp.RetryCount(id) > 0 || cp.IsFallbackTaken(id) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// addWithDownstream adds root and every node reachable from it to set.
func (e *Engine) addWithDownstream(set map[string]bool, root string) {
	set[root] = true
	for _, id := range downstreamNodes(e.graph, root) {
		set[id] = true
	}
}

// recordHalt persists the dead-stop location (#651) so the next resume can
// tell "the run ended at this node" from "the run was killed on the way to
// it". Saves the checkpoint: the terminal-halt paths otherwise return without
// one, leaving the on-disk state at the previous routing step.
func (e *Engine) recordHalt(s *runState, nodeID string) {
	s.cp.RecordHalt(nodeID)
	e.saveCheckpoint(s.cp, s.pctx, s.runID)
}

// recordFailRoute remembers fail-routing provenance (#651) at the point the
// engine routes AWAY from a failed node to target. No-op unless the node's
// outcome was fail. Reason is the handler's FailureReason (bounded on write).
func (e *Engine) recordFailRoute(s *runState, origin, target string, kind FallbackOriginKind) {
	outcome, _ := s.pctx.Get(ContextKeyOutcome)
	if outcome != string(OutcomeFail) {
		return
	}
	s.cp.RecordFallbackOrigin(target, origin, OutcomeFail, s.lastOutcome.FailureReason, kind)
}
