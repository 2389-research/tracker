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
	s.Apply(MsgPipelineTerminated{Status: pipeline.OutcomeSuccess})
	if !s.PipelineDone() {
		t.Error("expected pipeline done")
	}
	if s.PipelineStatus() != pipeline.OutcomeSuccess {
		t.Errorf("expected status=success, got %q", s.PipelineStatus())
	}
	if s.HeadlineOverride() != nil {
		t.Errorf("expected nil headline override, got %+v", s.HeadlineOverride())
	}
}

func TestStateStorePipelineFailed(t *testing.T) {
	s := NewStateStore(nil)
	s.Apply(MsgPipelineTerminated{Status: pipeline.OutcomeFail, Error: "fatal"})
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

// TestStateStorePipelineBudgetExceeded pins that a budget terminal — which the
// old adapter dropped entirely — now lands in the store with the authoritative
// status so the completion row renders it.
func TestStateStorePipelineBudgetExceeded(t *testing.T) {
	s := NewStateStore(nil)
	s.Apply(MsgPipelineTerminated{Status: pipeline.OutcomeBudgetExceeded, Error: "over budget"})
	if !s.PipelineDone() {
		t.Error("expected pipeline done on budget exceeded")
	}
	if s.PipelineStatus() != pipeline.OutcomeBudgetExceeded {
		t.Errorf("expected status=budget_exceeded, got %q", s.PipelineStatus())
	}
}

// TestStateStorePipelinePausedBilling pins that a recoverable billing pause —
// also dropped by the old adapter — lands with the paused status.
func TestStateStorePipelinePausedBilling(t *testing.T) {
	s := NewStateStore(nil)
	s.Apply(MsgPipelineTerminated{Status: pipeline.OutcomePausedBilling, Error: "credit balance too low"})
	if !s.PipelineDone() {
		t.Error("expected pipeline done on billing pause")
	}
	if s.PipelineStatus() != pipeline.OutcomePausedBilling {
		t.Errorf("expected status=paused_billing, got %q", s.PipelineStatus())
	}
}

// TestStateStore_ValidationOverridesAccumulate verifies that
// MsgValidationOverridden events build up the in-memory override list in
// chronological order for display (ValidationOverrides()), while the terminal
// status + headline come from the authoritative MsgPipelineTerminated — the
// store no longer infers the classification from override presence.
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
	// The authoritative terminal message (as the stateful adapter would build
	// it) carries the status and the latest headline (G2) per spec D5a.
	s.Apply(MsgPipelineTerminated{
		Status:   pipeline.OutcomeValidationOverridden,
		Override: &pipeline.OverrideDetail{GateNodeID: "G2", Label: "force", Actor: pipeline.ActorAutopilot},
	})
	if s.PipelineStatus() != pipeline.OutcomeValidationOverridden {
		t.Errorf("expected status=validation_overridden, got %q", s.PipelineStatus())
	}
	if h := s.HeadlineOverride(); h == nil || h.GateNodeID != "G2" {
		t.Errorf("expected headline=G2 (D5a latest), got %+v", h)
	}
}

// TestStateStore_TerminatedStatusIsAuthoritative verifies the store consumes the
// terminal message's Status verbatim and does NOT re-derive it from accumulated
// overrides. Even with override events accumulated, an authoritative fail status
// stays fail (the finding: status must not be reconstructed in the store).
func TestStateStore_TerminatedStatusIsAuthoritative(t *testing.T) {
	s := NewStateStore(nil)
	s.Apply(MsgValidationOverridden{
		NodeID: "G1",
		Detail: pipeline.OverrideDetail{GateNodeID: "G1", Label: "lgtm", Actor: pipeline.ActorHuman},
	})
	s.Apply(MsgPipelineTerminated{Status: pipeline.OutcomeFail, Error: "later node blew up"})
	if s.PipelineStatus() != pipeline.OutcomeFail {
		t.Errorf("store must not flip an authoritative fail to overridden; got %q", s.PipelineStatus())
	}
	if s.PipelineError() != "later node blew up" {
		t.Errorf("expected fail error preserved, got %q", s.PipelineError())
	}
	if s.HeadlineOverride() != nil {
		t.Errorf("fail terminal carries no headline override; got %+v", s.HeadlineOverride())
	}
}

// TestStateStore_ExactlyOneTerminalTransition verifies that a scoped child
// terminal event never reaches the store (the adapter drops it), so the store
// records exactly one terminal transition from the root-level message.
func TestStateStore_ExactlyOneTerminalTransition(t *testing.T) {
	a := NewPipelineAdapter()
	s := NewStateStore(nil)
	// A child-scoped budget terminal is dropped by the adapter — nothing to apply.
	if msg := a.Adapt(pipeline.PipelineEvent{
		Type:           pipeline.EventBudgetExceeded,
		NodeID:         "Parent/Child",
		TerminalStatus: string(pipeline.OutcomeBudgetExceeded),
	}); msg != nil {
		s.Apply(msg)
	}
	if s.PipelineDone() {
		t.Fatal("child-scoped terminal must not mark the run done")
	}
	// The root-level terminal is the single transition.
	if msg := a.Adapt(pipeline.PipelineEvent{
		Type:           pipeline.EventBudgetExceeded,
		TerminalStatus: string(pipeline.OutcomeBudgetExceeded),
	}); msg != nil {
		s.Apply(msg)
	}
	if !s.PipelineDone() || s.PipelineStatus() != pipeline.OutcomeBudgetExceeded {
		t.Errorf("expected done+budget_exceeded from root terminal, got done=%v status=%q",
			s.PipelineDone(), s.PipelineStatus())
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
