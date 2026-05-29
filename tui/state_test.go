// ABOUTME: Tests for the StateStore central state container.
// ABOUTME: Verifies state updates via Apply and reads via getters.
package tui

import (
	"reflect"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func TestStateStoreInitialState(t *testing.T) {
	s := NewStateStore(nil)
	if len(s.Nodes()) != 0 {
		t.Error("expected empty node list")
	}
	if s.IsThinking("n1") {
		t.Error("expected not thinking initially")
	}
	if s.PipelineDone() {
		t.Error("expected pipeline not done initially")
	}
}

func TestStateStoreNodeLifecycle(t *testing.T) {
	s := NewStateStore(nil)
	s.SetNodes([]NodeEntry{{ID: "n1", Label: "Step 1"}, {ID: "n2", Label: "Step 2"}, {ID: "n3", Label: "Step 3"}})
	if len(s.Nodes()) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(s.Nodes()))
	}
	s.Apply(MsgNodeStarted{NodeID: "n1"})
	if s.NodeStatus("n1") != NodeRunning {
		t.Errorf("expected running, got %v", s.NodeStatus("n1"))
	}
	s.Apply(MsgNodeCompleted{NodeID: "n1", Outcome: "success"})
	if s.NodeStatus("n1") != NodeDone {
		t.Errorf("expected done")
	}
	s.Apply(MsgNodeFailed{NodeID: "n2", Error: "boom"})
	if s.NodeStatus("n2") != NodeFailed {
		t.Errorf("expected failed")
	}
	if s.NodeError("n2") != "boom" {
		t.Errorf("expected error 'boom', got %q", s.NodeError("n2"))
	}
}

func TestStateStorePipelineDone(t *testing.T) {
	s := NewStateStore(nil)
	s.Apply(MsgPipelineCompleted{})
	if !s.PipelineDone() {
		t.Error("expected pipeline done")
	}
	// With no overrides accumulated and no explicit Status on the msg, the
	// StateStore derives success.
	if s.PipelineStatus() != pipeline.OutcomeSuccess {
		t.Errorf("expected status=success, got %q", s.PipelineStatus())
	}
	if s.HeadlineOverride() != nil {
		t.Errorf("expected nil headline override, got %+v", s.HeadlineOverride())
	}
}

func TestStateStorePipelineFailed(t *testing.T) {
	s := NewStateStore(nil)
	s.Apply(MsgPipelineFailed{Error: "fatal"})
	if !s.PipelineDone() {
		t.Error("expected pipeline done on failure")
	}
	if s.PipelineError() != "fatal" {
		t.Errorf("expected 'fatal', got %q", s.PipelineError())
	}
	if s.PipelineStatus() != pipeline.OutcomeFail {
		t.Errorf("expected status=fail, got %q", s.PipelineStatus())
	}
}

// TestStateStore_ValidationOverridesAccumulate verifies that
// MsgValidationOverridden events build up the in-memory override list in
// chronological order, and the completion message uses the latest as headline.
func TestStateStore_ValidationOverridesAccumulate(t *testing.T) {
	s := NewStateStore(nil)
	s.Apply(MsgValidationOverridden{
		NodeID: "G1",
		Detail: pipeline.OverrideDetail{GateNodeID: "G1", Label: "lgtm", Actor: pipeline.ActorHuman},
	})
	s.Apply(MsgValidationOverridden{
		NodeID: "G2",
		Detail: pipeline.OverrideDetail{GateNodeID: "G2", Label: "force", Actor: pipeline.ActorAutopilot},
	})
	got := s.ValidationOverrides()
	if len(got) != 2 {
		t.Fatalf("expected 2 accumulated overrides, got %d", len(got))
	}
	if got[0].GateNodeID != "G1" || got[1].GateNodeID != "G2" {
		t.Errorf("expected chronological order [G1, G2], got [%s, %s]", got[0].GateNodeID, got[1].GateNodeID)
	}
	// Now complete: status should flip to validation_overridden and headline
	// should be the latest (G2) per spec D5a.
	s.Apply(MsgPipelineCompleted{})
	if s.PipelineStatus() != pipeline.OutcomeValidationOverridden {
		t.Errorf("expected status=validation_overridden, got %q", s.PipelineStatus())
	}
	if h := s.HeadlineOverride(); h == nil || h.GateNodeID != "G2" {
		t.Errorf("expected headline=G2 (D5a latest), got %+v", h)
	}
}

// TestStateStore_MsgPipelineCompleted_ExplicitStatusWins verifies that when
// the stateful PipelineAdapter has already computed Status + Override (the
// production path), the StateStore honors the message's fields rather than
// re-deriving from accumulated state. This matters when the adapter saw
// override events the state store didn't (e.g. early-injection scenarios).
func TestStateStore_MsgPipelineCompleted_ExplicitStatusWins(t *testing.T) {
	s := NewStateStore(nil)
	override := &pipeline.OverrideDetail{
		GateNodeID: "AdapterSet",
		Label:      "approve",
		Actor:      pipeline.ActorWebhook,
	}
	s.Apply(MsgPipelineCompleted{
		Status:   pipeline.OutcomeValidationOverridden,
		Override: override,
	})
	if s.PipelineStatus() != pipeline.OutcomeValidationOverridden {
		t.Errorf("expected status=validation_overridden, got %q", s.PipelineStatus())
	}
	if h := s.HeadlineOverride(); h == nil || h.GateNodeID != "AdapterSet" {
		t.Errorf("expected headline=AdapterSet from msg, got %+v", h)
	}
}

func TestStateStoreThinking(t *testing.T) {
	s := NewStateStore(nil)
	s.Apply(MsgThinkingStarted{NodeID: "n1"})
	if !s.IsThinking("n1") {
		t.Error("expected thinking")
	}
	s.Apply(MsgThinkingStopped{NodeID: "n1"})
	if s.IsThinking("n1") {
		t.Error("expected not thinking")
	}
}

func TestStateStoreNodeRetrying(t *testing.T) {
	s := NewStateStore(nil)
	s.SetNodes([]NodeEntry{{ID: "n1", Label: "Step 1"}})
	s.Apply(MsgNodeRetrying{NodeID: "n1", Message: "retrying in 5s"})
	if s.NodeStatus("n1") != NodeRetrying {
		t.Errorf("expected retrying, got %v", s.NodeStatus("n1"))
	}
	if s.NodeRetryMessage("n1") != "retrying in 5s" {
		t.Errorf("expected retry message 'retrying in 5s', got %q", s.NodeRetryMessage("n1"))
	}
}

func TestStateStoreSubgraphNodeInsertion(t *testing.T) {
	s := NewStateStore(nil)
	s.SetNodes([]NodeEntry{
		{ID: "Start", Label: "Start"},
		{ID: "SubA", Label: "SubA"},
		{ID: "Done", Label: "Done"},
	})

	// Simulate subgraph child nodes starting (dynamic insertion).
	s.Apply(MsgNodeStarted{NodeID: "SubA/Child1"})
	s.Apply(MsgNodeStarted{NodeID: "SubA/Child2"})

	nodes := s.Nodes()
	// Expect: Start, SubA, SubA/Child1, SubA/Child2, Done
	if len(nodes) != 5 {
		t.Fatalf("expected 5 nodes, got %d: %v", len(nodes), nodeIDs(nodes))
	}

	expected := []string{"Start", "SubA", "SubA/Child1", "SubA/Child2", "Done"}
	for i, want := range expected {
		if nodes[i].ID != want {
			t.Errorf("nodes[%d] = %q, want %q (full: %v)", i, nodes[i].ID, want, nodeIDs(nodes))
		}
	}

	// Verify the children are running.
	if s.NodeStatus("SubA/Child1") != NodeRunning {
		t.Errorf("expected SubA/Child1 running, got %v", s.NodeStatus("SubA/Child1"))
	}
	if s.NodeStatus("SubA/Child2") != NodeRunning {
		t.Errorf("expected SubA/Child2 running, got %v", s.NodeStatus("SubA/Child2"))
	}

	// Verify visit path includes subgraph nodes.
	path := s.VisitPath()
	if len(path) != 2 || path[0] != "SubA/Child1" || path[1] != "SubA/Child2" {
		t.Errorf("expected visit path [SubA/Child1, SubA/Child2], got %v", path)
	}
}

func TestStateStoreSubgraphHelpers(t *testing.T) {
	if !IsSubgraphNode("Parent/Child") {
		t.Error("Parent/Child should be a subgraph node")
	}
	if IsSubgraphNode("TopLevel") {
		t.Error("TopLevel should not be a subgraph node")
	}
	if SubgraphDepth("A/B/C") != 2 {
		t.Errorf("expected depth 2, got %d", SubgraphDepth("A/B/C"))
	}
	if SubgraphChildLabel("Parent/Child") != "Child" {
		t.Errorf("expected 'Child', got %q", SubgraphChildLabel("Parent/Child"))
	}
}

func nodeIDs(entries []NodeEntry) []string {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}
	return ids
}

func TestStateStoreCompletedCount(t *testing.T) {
	s := NewStateStore(nil)
	s.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}, {ID: "n3"}})
	s.Apply(MsgNodeCompleted{NodeID: "n1"})
	s.Apply(MsgNodeCompleted{NodeID: "n2"})
	done, total := s.Progress()
	if done != 2 || total != 3 {
		t.Errorf("expected 2/3, got %d/%d", done, total)
	}
}

func TestStateStoreLazyInsertsSubgraphChildAfterParent(t *testing.T) {
	s := NewStateStore(nil)
	s.SetNodes([]NodeEntry{{ID: "Parent"}, {ID: "Done"}})

	s.Apply(MsgNodeStarted{NodeID: "Parent/Child"})

	got := nodeIDs(s.Nodes())
	want := []string{"Parent", "Parent/Child", "Done"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("node order mismatch\n got: %v\nwant: %v", got, want)
	}
	if s.NodeStatus("Parent/Child") != NodeRunning {
		t.Fatalf("expected Parent/Child running, got %v", s.NodeStatus("Parent/Child"))
	}
}

func TestStateStoreLazyInsertKeepsSiblingArrivalOrder(t *testing.T) {
	s := NewStateStore(nil)
	s.SetNodes([]NodeEntry{{ID: "Parent"}, {ID: "Done"}})

	s.Apply(MsgNodeStarted{NodeID: "Parent/ChildA"})
	s.Apply(MsgNodeStarted{NodeID: "Parent/ChildB"})

	got := nodeIDs(s.Nodes())
	want := []string{"Parent", "Parent/ChildA", "Parent/ChildB", "Done"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("node order mismatch\n got: %v\nwant: %v", got, want)
	}
}
