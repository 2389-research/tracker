// ABOUTME: Tests for the NodeList component.
// ABOUTME: Verifies signal lamp rendering, thinking animation, and scroll behavior.
package tui

import (
	"strings"
	"testing"
)

func TestNodeListRendersNodes(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "step1"}, {ID: "step2"}, {ID: "step3"}})
	tr := NewThinkingTracker()
	nl := NewNodeList(store, tr, 10)
	view := nl.View()
	if !strings.Contains(view, "step1") {
		t.Errorf("expected step1 in view, got: %s", view)
	}
	if !strings.Contains(view, LampPending) {
		t.Errorf("expected pending lamp, got: %s", view)
	}
}

func TestNodeListSignalLamps(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}, {ID: "n3"}})
	store.Apply(MsgNodeStarted{NodeID: "n1"})
	store.Apply(MsgNodeCompleted{NodeID: "n1"})
	store.Apply(MsgNodeFailed{NodeID: "n2"})
	tr := NewThinkingTracker()
	nl := NewNodeList(store, tr, 10)
	view := nl.View()
	if !strings.Contains(view, LampDone) {
		t.Errorf("expected done lamp for n1")
	}
	if !strings.Contains(view, LampFailed) {
		t.Errorf("expected failed lamp for n2")
	}
	if !strings.Contains(view, LampPending) {
		t.Errorf("expected pending lamp for n3")
	}
}

func TestNodeListRetryDisplay(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}})
	store.Apply(MsgNodeRetrying{NodeID: "n1", Message: "retrying in 5s"})
	tr := NewThinkingTracker()
	nl := NewNodeList(store, tr, 10)
	view := nl.View()
	if !strings.Contains(view, "↻") {
		t.Errorf("expected retry lamp in view, got: %s", view)
	}
	if !strings.Contains(view, "retrying in 5s") {
		t.Errorf("expected retry message in view, got: %s", view)
	}
}

func TestNodeListSubgraphChildrenAppear(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{
		{ID: "Start", Label: "Start"},
		{ID: "SubA", Label: "SubA"},
		{ID: "Done", Label: "Done"},
	})

	// Simulate subgraph children arriving dynamically.
	store.Apply(MsgNodeStarted{NodeID: "Start"})
	store.Apply(MsgNodeCompleted{NodeID: "Start"})
	store.Apply(MsgNodeStarted{NodeID: "SubA"})
	store.Apply(MsgNodeStarted{NodeID: "SubA/Child1"})
	store.Apply(MsgNodeCompleted{NodeID: "SubA/Child1"})
	store.Apply(MsgNodeStarted{NodeID: "SubA/Child2"})

	tr := NewThinkingTracker()
	nl := NewNodeList(store, tr, 20)
	nl.SetSize(60, 20)
	view := nl.View()

	// Subgraph children should appear in the rendered view.
	if !strings.Contains(view, "Child1") {
		t.Errorf("expected Child1 in sidebar view, got:\n%s", view)
	}
	if !strings.Contains(view, "Child2") {
		t.Errorf("expected Child2 in sidebar view, got:\n%s", view)
	}

	// Children should be indented (SubgraphDepth=1 → 2 spaces).
	// The view should show them between SubA and Done.
	lines := strings.Split(view, "\n")
	subAIdx, child1Idx, child2Idx, doneIdx := -1, -1, -1, -1
	for i, line := range lines {
		if strings.Contains(line, "SubA") && !strings.Contains(line, "Child") {
			subAIdx = i
		}
		if strings.Contains(line, "Child1") {
			child1Idx = i
		}
		if strings.Contains(line, "Child2") {
			child2Idx = i
		}
		if strings.Contains(line, "Done") {
			doneIdx = i
		}
	}

	if subAIdx < 0 || child1Idx < 0 || child2Idx < 0 || doneIdx < 0 {
		t.Fatalf("missing expected nodes in view (SubA=%d, Child1=%d, Child2=%d, Done=%d):\n%s",
			subAIdx, child1Idx, child2Idx, doneIdx, view)
	}

	// Order: SubA < Child1 < Child2 < Done
	if !(subAIdx < child1Idx && child1Idx < child2Idx && child2Idx < doneIdx) {
		t.Errorf("wrong order: SubA@%d, Child1@%d, Child2@%d, Done@%d\n%s",
			subAIdx, child1Idx, child2Idx, doneIdx, view)
	}
}

func TestNodeListThinkingAnimation(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}})
	store.Apply(MsgNodeStarted{NodeID: "n1"})
	tr := NewThinkingTracker()
	tr.Start("n1")
	nl := NewNodeList(store, tr, 10)
	view := nl.View()
	if !strings.Contains(view, ThinkingFrames[0]) {
		t.Errorf("expected thinking frame, got: %s", view)
	}
}
