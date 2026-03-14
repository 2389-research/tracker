// ABOUTME: Tests for the reusable ScrollView component.
// ABOUTME: Covers auto-scroll, manual override, and viewport calculations.
package tui

import "testing"

func TestScrollViewAutoScroll(t *testing.T) {
	sv := NewScrollView(5)
	for i := 0; i < 10; i++ {
		sv.Append("line")
	}
	start, end := sv.VisibleRange()
	if start != 5 || end != 10 {
		t.Errorf("expected 5-10, got %d-%d", start, end)
	}
}

func TestScrollViewManualScrollDisablesAuto(t *testing.T) {
	sv := NewScrollView(5)
	for i := 0; i < 10; i++ {
		sv.Append("line")
	}
	sv.ScrollUp(2)
	start, _ := sv.VisibleRange()
	if start != 3 {
		t.Errorf("expected start=3, got %d", start)
	}
	if sv.AutoScroll() {
		t.Error("expected auto-scroll disabled")
	}
}

func TestScrollViewScrollToBottom(t *testing.T) {
	sv := NewScrollView(5)
	for i := 0; i < 10; i++ {
		sv.Append("line")
	}
	sv.ScrollUp(3)
	sv.ScrollToBottom()
	if !sv.AutoScroll() {
		t.Error("expected auto-scroll re-enabled")
	}
	start, _ := sv.VisibleRange()
	if start != 5 {
		t.Errorf("expected start=5, got %d", start)
	}
}

func TestScrollViewEmpty(t *testing.T) {
	sv := NewScrollView(5)
	start, end := sv.VisibleRange()
	if start != 0 || end != 0 {
		t.Errorf("expected 0-0, got %d-%d", start, end)
	}
}

func TestScrollViewFewerThanHeight(t *testing.T) {
	sv := NewScrollView(10)
	sv.Append("a")
	sv.Append("b")
	start, end := sv.VisibleRange()
	if start != 0 || end != 2 {
		t.Errorf("expected 0-2, got %d-%d", start, end)
	}
}

func TestScrollViewSetHeight(t *testing.T) {
	sv := NewScrollView(5)
	for i := 0; i < 20; i++ {
		sv.Append("line")
	}
	sv.SetHeight(10)
	start, end := sv.VisibleRange()
	if end-start != 10 {
		t.Errorf("expected window of 10, got %d", end-start)
	}
}

func TestScrollViewUpdateLast(t *testing.T) {
	sv := NewScrollView(5)
	sv.Append("first")
	sv.Append("second")
	sv.UpdateLast("updated")
	if sv.Lines()[1] != "updated" {
		t.Errorf("expected 'updated', got %q", sv.Lines()[1])
	}
}
