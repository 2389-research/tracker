// ABOUTME: Turns a run's pipeline event stream into concise Slack thread updates.
// ABOUTME: describeEvent is the "what's worth posting" policy (decision D2).
package chatops

import (
	"fmt"
	"sync"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// costThrottle bounds how often a spend update is posted — EventCostUpdated
// fires after every completed node, so it must be debounced.
const costThrottle = 30 * time.Second

// notifier is a run's pipeline.PipelineEventHandler: it filters the event stream
// down to notable updates and posts them to the run's thread.
type notifier struct {
	ui ThreadUI

	mu       sync.Mutex
	lastCost time.Time
	now      func() time.Time // injectable for tests
}

func newNotifier(ui ThreadUI) *notifier {
	return &notifier{ui: ui, now: time.Now}
}

// HandlePipelineEvent implements pipeline.PipelineEventHandler.
func (n *notifier) HandlePipelineEvent(evt pipeline.PipelineEvent) {
	if evt.Type == pipeline.EventCostUpdated {
		n.maybePostCost(evt)
		return
	}
	if msg := describeEvent(evt); msg != "" {
		_ = n.ui.Post(msg)
	}
}

// maybePostCost posts a spend update at most once per costThrottle window.
func (n *notifier) maybePostCost(evt pipeline.PipelineEvent) {
	if evt.Cost == nil {
		return
	}
	n.mu.Lock()
	if !n.lastCost.IsZero() && n.now().Sub(n.lastCost) < costThrottle {
		n.mu.Unlock()
		return
	}
	n.lastCost = n.now()
	n.mu.Unlock()
	_ = n.ui.Post(fmt.Sprintf("💸 spend so far: $%.2f (%d tokens)", evt.Cost.TotalCostUSD, evt.Cost.TotalTokens))
}

// describeEvent returns a short thread message for a notable pipeline event, or
// "" to stay quiet. This is the policy for what a Slack watcher sees during a
// run.
//
// DECISION POINT (D2) — this starting point is deliberately minimal; refine it
// to taste (which event types surface, how they read). EventCostUpdated is
// handled separately (throttled), so return "" for it here.
//
// Notable event types to consider: EventStageCompleted (milestone),
// EventStageFailed / EventStageRetrying, EventBudgetExceeded,
// EventNodeCostLimitExceeded, EventNodeNoProgressDetected,
// EventValidationOverridden (a gate was overridden), EventWorkPreserveFailed,
// and the terminal event (evt.TerminalStatus is set on it).
func describeEvent(evt pipeline.PipelineEvent) string {
	switch evt.Type {
	case pipeline.EventStageCompleted:
		return "✅ " + evt.NodeID + " done"
	case pipeline.EventStageFailed:
		return "⚠️ " + evt.NodeID + " failed"
	case pipeline.EventStageRetrying:
		return "🔁 retrying " + evt.NodeID
	case pipeline.EventBudgetExceeded:
		return "🛑 budget exceeded — run halted"
	case pipeline.EventValidationOverridden:
		return "🔓 a gate was overridden"
	}
	if evt.TerminalStatus != "" {
		return "🏁 run finished: " + evt.TerminalStatus
	}
	return ""
}
