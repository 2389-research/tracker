// ABOUTME: Tests for the HistoryTrail component's rendered visit log.
// ABOUTME: Covers newest-first order, repeat counts and the row limit on long, looping runs.
package tui

import (
	"slices"
	"strings"
	"testing"
)

// renderTrail starts each visit in order and returns the trail's rows at the
// given height, with styling removed.
func renderTrail(visits []string, height int) []string {
	s := NewStateStore(nil)
	s.SetNodes([]NodeEntry{
		{ID: "Plan", Label: "Plan the work"},
		{ID: "Build", Label: "Build"},
		{ID: "Test", Label: "Test"},
	})
	for _, id := range visits {
		s.Apply(MsgNodeStarted{NodeID: id})
	}
	h := NewHistoryTrail(s)
	h.SetSize(80, height)
	return strings.Split(strings.TrimSuffix(stripAnsi(h.View()), "\n"), "\n")
}

// trailRow is the rendered row for a running node's trail entry.
func trailRow(label string) string { return "  " + LampRunning + " " + label }

func assertTrailRows(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("trail rows:\n got %q\nwant %q", got, want)
	}
}

func TestHistoryTrailNewestFirstWithRepeatCounts(t *testing.T) {
	visits := []string{"Plan", "Build", "Test", "Build", "Build", "Test", "Test", "Test"}
	want := []string{"TRAIL", trailRow("Test ×3"), trailRow("Build ×2"), trailRow("Test"), trailRow("Build"), trailRow("Plan the work")}
	assertTrailRows(t, renderTrail(visits, 20), want)
}

func TestHistoryTrailRowLimitKeepsFullRepeatCount(t *testing.T) {
	// Newest first the trail reads Test ×2, Build ×5, Test, Plan. Two rows fit,
	// and the second still counts all five Build visits.
	visits := []string{"Plan", "Test", "Build", "Build", "Build", "Build", "Build", "Test", "Test"}
	want := []string{"TRAIL", trailRow("Test ×2"), trailRow("Build ×5")}
	assertTrailRows(t, renderTrail(visits, 3), want)
}

func TestHistoryTrailLongLoopingRun(t *testing.T) {
	visits := []string{"Plan"}
	for range 1000 {
		visits = append(visits, "Build", "Test", "Test")
	}
	want := []string{"TRAIL", trailRow("Test ×2"), trailRow("Build"), trailRow("Test ×2")}
	assertTrailRows(t, renderTrail(visits, 4), want)
}

func TestHistoryTrailShowsOneRowWhenTooShort(t *testing.T) {
	visits := []string{"Plan", "Build", "Build"}
	want := []string{"TRAIL", trailRow("Build ×2")}
	assertTrailRows(t, renderTrail(visits, 0), want)
}
