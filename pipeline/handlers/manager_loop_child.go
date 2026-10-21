// ABOUTME: manager_loop child-result helpers for a canceled or paused child run —
// ABOUTME: split from manager_loop.go to keep that file under the size ratchet.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// handleChildCancelled reports the manager_loop's own ctx cancellation as the
// authoritative cause of a child-result race (see handleChildResult's
// cancellation-cascade comment for why ctx.Err() — not msg.err — is
// canonical here).
func (h *ManagerLoopHandler) handleChildCancelled(nodeID string, msg engineResultMsg, ctxErr error, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	pctx.Set("stack.child.status", "cancelled")
	// Use ctx.Err() (typically `context.Canceled` / `context.DeadlineExceeded`)
	// as the canonical cause for the audit message. The other cancellation
	// path (the <-ctx.Done() arm in Execute) emits the same bare cause; the
	// `msg.err` available here is the engine-wrapped form ("handler error
	// at node ...: context canceled"), so using it would make the two paths
	// produce different audit lines for the same observable event.
	h.pipelineEvents.HandlePipelineEvent(stampRunID(pipeline.PipelineEvent{
		Type:      pipeline.EventStageFailed,
		Timestamp: time.Now(),
		NodeID:    nodeID,
		Message:   fmt.Sprintf("manager_loop: cancelled: %v", ctxErr),
	}, pctx))
	out := pipeline.Outcome{Status: pipeline.OutcomeFail}
	if msg.result != nil {
		out.ChildUsage = msg.result.Usage
	}
	return out, fmt.Errorf("manager_loop: cancelled: %w", ctxErr)
}

// childPause returns the child's PauseError when the child run ended paused.
func childPause(msg engineResultMsg) (*pipeline.PauseError, bool) {
	if msg.result == nil || !msg.result.Status.IsPaused() {
		return nil, false
	}
	var pe *pipeline.PauseError
	if !errors.As(msg.err, &pe) {
		return nil, false
	}
	return pe, true
}

// joinChild cancels the child and waits (bounded) for it to exit. A child that
// paused just before it was canceled still pauses the parent: paused is true
// and the caller returns (out, pe) instead of its abort or stop result.
func joinChild(cancelChild context.CancelFunc, resultCh <-chan engineResultMsg, pctx *pipeline.PipelineContext) (pipeline.Outcome, *pipeline.PauseError, bool) {
	cancelChild()
	msg, exited := waitForChild(resultCh)
	if !exited {
		return pipeline.Outcome{}, nil, false
	}
	pe, paused := childPause(msg)
	if !paused {
		return pipeline.Outcome{}, nil, false
	}
	pctx.Set("stack.child.status", "paused")
	return pipeline.Outcome{Status: pipeline.OutcomeFail, ChildUsage: msg.result.Usage}, pe, true
}

// childJoinGrace is the maximum time the manager will wait for a child
// goroutine to finish after cancellation. If the child is stuck in a
// non-context-aware handler, the manager returns after this grace period
// rather than blocking indefinitely (the child goroutine becomes orphaned).
const childJoinGrace = 30 * time.Second

// waitForChild waits for the child goroutine to send on resultCh, with a
// bounded grace period. Returns the child's message and true if it exited,
// false if the grace period expired.
func waitForChild(resultCh <-chan engineResultMsg) (engineResultMsg, bool) {
	select {
	case msg := <-resultCh:
		return msg, true
	case <-time.After(childJoinGrace):
		return engineResultMsg{}, false
	}
}
