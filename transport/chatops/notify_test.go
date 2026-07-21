// ABOUTME: Tests for the notifier plumbing (cost throttle + event filtering).
package chatops

import (
	"testing"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

func costEvent(usd float64) pipeline.PipelineEvent {
	return pipeline.PipelineEvent{
		Type: pipeline.EventCostUpdated,
		Cost: &pipeline.CostSnapshot{TotalCostUSD: usd, TotalTokens: 100},
	}
}

func TestNotifier_CostThrottle(t *testing.T) {
	ui := newFakeUI()
	n := newNotifier(ui)
	base := time.Unix(1000, 0)
	cur := base
	n.now = func() time.Time { return cur }

	n.HandlePipelineEvent(costEvent(0.1)) // first — posts
	n.HandlePipelineEvent(costEvent(0.2)) // same window — throttled
	cur = base.Add(costThrottle + time.Second)
	n.HandlePipelineEvent(costEvent(0.3)) // window elapsed — posts

	if got := len(ui.posts); got != 2 {
		t.Fatalf("cost posts = %d, want 2 (%v)", got, ui.posts)
	}
}

func TestNotifier_FiltersToNotable(t *testing.T) {
	ui := newFakeUI()
	n := newNotifier(ui)

	n.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventStageCompleted, NodeID: "Build"})
	n.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventStageStarted, NodeID: "Build"}) // not notable
	n.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventDecisionEdge, NodeID: "Build"}) // not notable
	n.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventPipelineCompleted, TerminalStatus: "success"})

	if got := len(ui.posts); got != 2 {
		t.Fatalf("posts = %d, want 2 (stage_completed + terminal): %v", got, ui.posts)
	}
}
