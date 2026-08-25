// ABOUTME: Tests that search selection owns the activity-log viewport.
// ABOUTME: Covers match visibility at both ends, n/N navigation, wraparound, and the return to tail-follow.
package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// searchLog builds an AgentLog whose viewport is far shorter than its content,
// so a match outside the tail window is only visible if search drives the
// window. Lines are "entry-N", with "NEEDLE-<k>" markers at the given indices.
func searchLog(t *testing.T, height, total int, needleAt ...int) *AgentLog {
	t.Helper()
	al := NewAgentLog(NewStateStore(nil), NewThinkingTracker(), height)
	al.SetSize(80, height)
	marks := make(map[int]int, len(needleAt))
	for k, idx := range needleAt {
		marks[idx] = k
	}
	for i := 0; i < total; i++ {
		text := fmt.Sprintf("entry-%d\n", i)
		if k, ok := marks[i]; ok {
			text = fmt.Sprintf("entry-%d NEEDLE-%d\n", i, k)
		}
		al.Update(MsgTextChunk{NodeID: "n1", Text: text})
	}
	return al
}

// typeSearch activates the search bar and types term, then confirms it with
// Enter (the state a user is in while pressing n/N).
func typeSearch(al *AgentLog, term string) {
	sb := al.Search()
	sb.Activate()
	for _, r := range term {
		sb.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	sb.Update(tea.KeyMsg{Type: tea.KeyEnter})
}

func TestSearchViewportShowsMatchNearStart(t *testing.T) {
	al := searchLog(t, 8, 200, 5)
	typeSearch(al, "NEEDLE")
	view := al.View()
	if !strings.Contains(view, "NEEDLE-0") {
		t.Errorf("expected the selected match to be scrolled into view, got:\n%s", view)
	}
	if strings.Contains(view, "entry-199") {
		t.Errorf("viewport should have left the tail to show the match, got:\n%s", view)
	}
}

func TestSearchViewportShowsMatchInMiddle(t *testing.T) {
	al := searchLog(t, 8, 200, 100)
	typeSearch(al, "NEEDLE")
	view := al.View()
	if !strings.Contains(view, "NEEDLE-0") {
		t.Errorf("expected mid-log match visible, got:\n%s", view)
	}
}

func TestSearchViewportAdvancesWithNextMatch(t *testing.T) {
	al := searchLog(t, 8, 300, 10, 150, 290)
	typeSearch(al, "NEEDLE")
	al.View() // build the match index

	if v := al.View(); !strings.Contains(v, "NEEDLE-0") {
		t.Fatalf("expected first match selected initially, got:\n%s", v)
	}
	al.Search().NextMatch()
	if v := al.View(); !strings.Contains(v, "NEEDLE-1") {
		t.Errorf("expected n to bring the second match into view, got:\n%s", v)
	}
	al.Search().NextMatch()
	if v := al.View(); !strings.Contains(v, "NEEDLE-2") {
		t.Errorf("expected n to bring the third match into view, got:\n%s", v)
	}
}

func TestSearchViewportWrapsAround(t *testing.T) {
	al := searchLog(t, 8, 300, 10, 290)
	typeSearch(al, "NEEDLE")
	al.View()

	al.Search().NextMatch() // -> last
	if v := al.View(); !strings.Contains(v, "NEEDLE-1") {
		t.Fatalf("expected second match in view, got:\n%s", v)
	}
	al.Search().NextMatch() // wraps -> first
	if v := al.View(); !strings.Contains(v, "NEEDLE-0") {
		t.Errorf("expected wraparound to bring the first match back into view, got:\n%s", v)
	}
}

func TestSearchViewportPrevMatch(t *testing.T) {
	al := searchLog(t, 8, 300, 10, 290)
	typeSearch(al, "NEEDLE")
	al.View()

	al.Search().PrevMatch() // from first, wraps back to last
	if v := al.View(); !strings.Contains(v, "NEEDLE-1") {
		t.Errorf("expected N to wrap back to the last match, got:\n%s", v)
	}
}

func TestSearchViewportReturnsToTailWhenCleared(t *testing.T) {
	al := searchLog(t, 8, 200, 5)
	typeSearch(al, "NEEDLE")
	if v := al.View(); !strings.Contains(v, "NEEDLE-0") {
		t.Fatalf("expected match in view before clearing, got:\n%s", v)
	}
	al.Search().Deactivate()
	v := al.View()
	if !strings.Contains(v, "entry-199") {
		t.Errorf("expected tail-follow to resume after clearing search, got:\n%s", v)
	}
	if strings.Contains(v, "NEEDLE-0") {
		t.Errorf("expected the far-back match to scroll away after clearing, got:\n%s", v)
	}
}

// A confirmed search (Enter pressed, bar hidden) must keep tracking new lines:
// the match index is what n/N walks, so letting it go stale silently breaks
// navigation as the log grows.
func TestSearchMatchesTrackNewLinesAfterConfirm(t *testing.T) {
	al := searchLog(t, 8, 50, 5)
	typeSearch(al, "NEEDLE")
	al.View()
	if got := al.Search().MatchCount(); got != 1 {
		t.Fatalf("expected 1 match, got %d", got)
	}
	al.Update(MsgTextChunk{NodeID: "n1", Text: "later NEEDLE-1 line\n"})
	al.View()
	if got := al.Search().MatchCount(); got != 2 {
		t.Errorf("expected the confirmed search to pick up the new match, got %d", got)
	}
}

// A match taller than the whole viewport must still be shown rather than
// skipped, even though it cannot fit.
func TestSearchViewportShowsOversizedMatch(t *testing.T) {
	al := NewAgentLog(NewStateStore(nil), NewThinkingTracker(), 5)
	al.SetSize(20, 5)
	for i := 0; i < 40; i++ {
		al.Update(MsgTextChunk{NodeID: "n1", Text: fmt.Sprintf("entry-%d\n", i)})
	}
	al.Update(MsgTextChunk{NodeID: "n1", Text: "NEEDLE " + strings.Repeat("wide ", 40) + "\n"})
	for i := 0; i < 40; i++ {
		al.Update(MsgTextChunk{NodeID: "n1", Text: fmt.Sprintf("tail-%d\n", i)})
	}
	typeSearch(al, "NEEDLE")
	if v := al.View(); !strings.Contains(v, "NEEDLE") {
		t.Errorf("expected an oversized match to still be rendered, got:\n%s", v)
	}
}
