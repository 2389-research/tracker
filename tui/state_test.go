// ABOUTME: Tests for the StateStore central state container.
// ABOUTME: Verifies state updates via Apply and reads via getters.
package tui

import (
	"reflect"
	"testing"
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

func nodeIDs(entries []NodeEntry) []string {
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	return ids
}
