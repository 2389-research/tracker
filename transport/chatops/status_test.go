// ABOUTME: Tests the live-card statusTracker folds the event stream correctly.
package chatops

import (
	"testing"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

type captureRenderer struct {
	last StatusCard
	n    int
}

func (c *captureRenderer) UpsertStatus(card StatusCard) error {
	c.last, c.n = card, c.n+1
	return nil
}

func startEvt(nodes ...pipeline.SnapshotNode) pipeline.PipelineEvent {
	return pipeline.PipelineEvent{Type: pipeline.EventPipelineStarted, Snapshot: &pipeline.RunSnapshot{Nodes: nodes}}
}

func TestStatusTracker_FoldsEvents(t *testing.T) {
	cr := &captureRenderer{}
	st := newStatusTracker(cr, "build_product", 5.00)
	st.minPush = 0 // push on every event

	st.HandlePipelineEvent(startEvt(
		pipeline.SnapshotNode{ID: "a", Label: "Setup"},
		pipeline.SnapshotNode{ID: "b", Label: "Build"},
		pipeline.SnapshotNode{ID: "c", Label: "Ship"},
	))
	if cr.last.TotalCount != 3 || cr.last.BudgetUSD != 5.00 {
		t.Fatalf("after start: %+v", cr.last)
	}

	st.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventStageStarted, NodeID: "a"})
	if cr.last.CurrentNode != "Setup" {
		t.Fatalf("current = %q, want Setup", cr.last.CurrentNode)
	}

	st.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventStageCompleted, NodeID: "a"})
	st.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventStageStarted, NodeID: "b"})
	if cr.last.DoneCount != 1 || cr.last.CurrentNode != "Build" {
		t.Fatalf("mid-run: done=%d current=%q", cr.last.DoneCount, cr.last.CurrentNode)
	}

	st.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventCostUpdated, Cost: &pipeline.CostSnapshot{TotalCostUSD: 1.87, TotalTokens: 4200}})
	if cr.last.CostUSD != 1.87 || cr.last.Tokens != 4200 {
		t.Fatalf("cost: %+v", cr.last)
	}

	st.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventPipelineCompleted, TerminalStatus: "success"})
	if cr.last.State != "success" || cr.last.CurrentNode != "" {
		t.Fatalf("terminal: state=%q current=%q", cr.last.State, cr.last.CurrentNode)
	}
}

func TestStatusTracker_FailedNodeAndResume(t *testing.T) {
	cr := &captureRenderer{}
	st := newStatusTracker(cr, "wf", 0)
	st.minPush = 0

	// A resume seeds already-completed nodes as done.
	st.HandlePipelineEvent(pipeline.PipelineEvent{
		Type: pipeline.EventPipelineStarted,
		Snapshot: &pipeline.RunSnapshot{
			Nodes:          []pipeline.SnapshotNode{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}},
			CompletedNodes: []string{"a"},
		},
	})
	if cr.last.DoneCount != 1 || cr.last.Nodes[0].State != "done" {
		t.Fatalf("resume seed: done=%d node0=%q", cr.last.DoneCount, cr.last.Nodes[0].State)
	}

	st.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventStageFailed, NodeID: "b"})
	if cr.last.Nodes[1].State != "failed" {
		t.Fatalf("node b state = %q, want failed", cr.last.Nodes[1].State)
	}
}

func TestStatusTracker_ThrottlesProgressPushes(t *testing.T) {
	cr := &captureRenderer{}
	st := newStatusTracker(cr, "wf", 0)
	cur := time.Unix(0, 0)
	st.now = func() time.Time { return cur }

	st.HandlePipelineEvent(startEvt(pipeline.SnapshotNode{ID: "a", Label: "A"}, pipeline.SnapshotNode{ID: "b", Label: "B"}))
	if cr.n != 1 {
		t.Fatalf("start pushed %d times, want 1", cr.n)
	}

	// Inside the throttle window a progress event folds in but does not push.
	cur = cur.Add(st.minPush / 2)
	st.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventStageStarted, NodeID: "a"})
	if cr.n != 1 {
		t.Fatalf("throttled event pushed: n=%d", cr.n)
	}

	// Once the window passes, the next event pushes a card carrying both changes.
	cur = cur.Add(st.minPush)
	st.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventCostUpdated, Cost: &pipeline.CostSnapshot{TotalCostUSD: 0.5, TotalTokens: 10}})
	if cr.n != 2 || cr.last.CurrentNode != "A" || cr.last.CostUSD != 0.5 || cr.last.Elapsed != st.minPush*3/2 {
		t.Fatalf("after window: n=%d card=%+v", cr.n, cr.last)
	}
	pushed := cr.last

	// A terminal event pushes at once, and the card pushed earlier keeps its own nodes.
	st.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventStageCompleted, NodeID: "a"})
	st.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventPipelineCompleted, TerminalStatus: "success"})
	if cr.n != 3 || cr.last.State != "success" || cr.last.DoneCount != 1 {
		t.Fatalf("terminal: n=%d card=%+v", cr.n, cr.last)
	}
	if pushed.Nodes[0].State != "active" {
		t.Fatalf("earlier card changed after a later event: node a = %q", pushed.Nodes[0].State)
	}
}
