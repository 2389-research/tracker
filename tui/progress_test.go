// ABOUTME: Tests for the progress bar and ETA tracker.
// ABOUTME: Verifies rendering, ETA estimation, and node duration recording.
package tui

import (
	"strings"
	"testing"
	"time"
)

func TestProgressTrackerEmptyStore(t *testing.T) {
	store := NewStateStore(nil)
	pt := NewProgressTracker(store)
	if pt.View() != "" {
		t.Error("expected empty view with no nodes")
	}
}

func TestProgressTrackerShowsFraction(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}, {ID: "n3"}})
	store.Apply(MsgNodeStarted{NodeID: "n1"})
	store.Apply(MsgNodeCompleted{NodeID: "n1"})

	pt := NewProgressTracker(store)
	pt.SetWidth(20)
	view := pt.View()
	if !strings.Contains(view, "1/3") {
		t.Errorf("expected '1/3' in progress view, got: %s", view)
	}
}

func TestProgressTrackerETA(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}, {ID: "n3"}})
	store.Apply(MsgNodeStarted{NodeID: "n1"})
	store.Apply(MsgNodeCompleted{NodeID: "n1"})

	pt := NewProgressTracker(store)
	pt.SetWidth(20)

	// Need at least 2 recorded durations for ETA to show.
	pt.RecordNodeDuration(30 * time.Second)
	pt.RecordNodeDuration(30 * time.Second)

	view := pt.View()
	if !strings.Contains(view, "left") {
		t.Errorf("expected ETA with 'left' in view, got: %s", view)
	}
}

func TestProgressTrackerAllDone(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}})
	store.Apply(MsgNodeStarted{NodeID: "n1"})
	store.Apply(MsgNodeCompleted{NodeID: "n1"})
	store.Apply(MsgNodeStarted{NodeID: "n2"})
	store.Apply(MsgNodeCompleted{NodeID: "n2"})

	pt := NewProgressTracker(store)
	view := pt.View()
	// When all done, remaining=0, no ETA.
	if strings.Contains(view, "left") {
		t.Error("expected no ETA when all nodes done")
	}
}

func TestFormatETA(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s left"},
		{90 * time.Second, "1m30s left"},
		{5 * time.Minute, "5m left"},
	}
	for _, tt := range tests {
		got := formatETA(tt.d)
		if got != tt.want {
			t.Errorf("formatETA(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestProgressTrackerMinWidth(t *testing.T) {
	store := NewStateStore(nil)
	pt := NewProgressTracker(store)
	pt.SetWidth(3) // should clamp to 10
	if pt.width < 10 {
		t.Errorf("expected min width 10, got %d", pt.width)
	}
}
