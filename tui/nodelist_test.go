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

func TestNodeListRendersSubgraphIndentAndLabels(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{
		{ID: "Parent"},
		{ID: "Parent/Child"},
		{ID: "Parent/Child/Grand"},
		{ID: "Done"},
	})
	tr := NewThinkingTracker()
	nl := NewNodeList(store, tr, 10)

	plain := stripANSI(nl.View())
	if !strings.Contains(plain, "  "+LampPending+" Child") {
		t.Fatalf("expected child row with 2-space indent and Child label, got:\n%s", plain)
	}
	if !strings.Contains(plain, "    "+LampPending+" Grand") {
		t.Fatalf("expected grandchild row with 4-space indent and Grand label, got:\n%s", plain)
	}
}
