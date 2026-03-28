// ABOUTME: Tests for the search bar and highlighting features.
// ABOUTME: Verifies activation, matching, navigation, and ANSI stripping.
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSearchBarInitialState(t *testing.T) {
	sb := NewSearchBar()
	if sb.Active() {
		t.Error("expected search bar initially inactive")
	}
	if sb.Term() != "" {
		t.Error("expected empty search term")
	}
}

func TestSearchBarActivateDeactivate(t *testing.T) {
	sb := NewSearchBar()
	sb.Activate()
	if !sb.Active() {
		t.Error("expected search bar active after Activate()")
	}
	sb.Deactivate()
	if sb.Active() {
		t.Error("expected search bar inactive after Deactivate()")
	}
	if sb.Term() != "" {
		t.Error("expected term cleared after deactivation")
	}
}

func TestSearchBarEscDeactivates(t *testing.T) {
	sb := NewSearchBar()
	sb.Activate()
	_, consumed := sb.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if !consumed {
		t.Error("expected Esc to be consumed by search bar")
	}
	if sb.Active() {
		t.Error("expected search bar deactivated after Esc")
	}
}

func TestSearchBarNotConsumedWhenInactive(t *testing.T) {
	sb := NewSearchBar()
	_, consumed := sb.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if consumed {
		t.Error("inactive search bar should not consume events")
	}
}

func TestSearchBarUpdateMatches(t *testing.T) {
	sb := NewSearchBar()
	sb.Activate()
	sb.term = "hello"

	lines := []styledLine{
		{nodeID: "n1", text: "Hello world"},
		{nodeID: "n1", text: "goodbye"},
		{nodeID: "n1", text: "say hello again"},
	}

	sb.UpdateMatches(lines)
	if sb.MatchCount() != 2 {
		t.Errorf("expected 2 matches, got %d", sb.MatchCount())
	}
}

func TestSearchBarNoMatchesOnEmptyTerm(t *testing.T) {
	sb := NewSearchBar()
	sb.Activate()
	sb.term = ""

	lines := []styledLine{
		{nodeID: "n1", text: "Hello world"},
	}

	sb.UpdateMatches(lines)
	if sb.MatchCount() != 0 {
		t.Errorf("expected 0 matches with empty term, got %d", sb.MatchCount())
	}
}

func TestSearchBarNextPrevMatch(t *testing.T) {
	sb := NewSearchBar()
	sb.Activate()
	sb.term = "x"
	sb.matches = []int{0, 5, 10}

	if sb.CurrentMatchLine() != 0 {
		t.Errorf("expected initial match at index 0, got %d", sb.CurrentMatchLine())
	}

	sb.NextMatch()
	if sb.CurrentMatchLine() != 5 {
		t.Errorf("expected match at index 5, got %d", sb.CurrentMatchLine())
	}

	sb.NextMatch()
	if sb.CurrentMatchLine() != 10 {
		t.Errorf("expected match at index 10, got %d", sb.CurrentMatchLine())
	}

	sb.NextMatch() // wraps around
	if sb.CurrentMatchLine() != 0 {
		t.Errorf("expected wrap to index 0, got %d", sb.CurrentMatchLine())
	}

	sb.PrevMatch() // wraps backwards
	if sb.CurrentMatchLine() != 10 {
		t.Errorf("expected wrap back to index 10, got %d", sb.CurrentMatchLine())
	}
}

func TestSearchBarNoMatchNavigation(t *testing.T) {
	sb := NewSearchBar()
	sb.NextMatch() // should not panic
	sb.PrevMatch() // should not panic
	if sb.CurrentMatchLine() != -1 {
		t.Errorf("expected -1 with no matches, got %d", sb.CurrentMatchLine())
	}
}

func TestSearchBarView(t *testing.T) {
	sb := NewSearchBar()
	if sb.View() != "" {
		t.Error("inactive search bar should return empty view")
	}
	sb.Activate()
	view := sb.View()
	if !strings.Contains(view, "/") {
		t.Errorf("expected '/' prompt in search bar view, got: %s", view)
	}
}

func TestStripAnsi(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"plain text", "plain text"},
		{"\x1b[31mred\x1b[0m", "red"},
		{"\x1b[1;34mbold blue\x1b[0m", "bold blue"},
		{"no escape", "no escape"},
	}
	for _, tt := range tests {
		got := stripAnsi(tt.input)
		if got != tt.want {
			t.Errorf("stripAnsi(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestHighlightLineNoMatch(t *testing.T) {
	result := HighlightLine("hello world", "xyz")
	if !strings.Contains(stripAnsi(result), "hello world") {
		t.Error("expected original text when no match")
	}
}

func TestHighlightLineEmptyTerm(t *testing.T) {
	original := "hello world"
	result := HighlightLine(original, "")
	if result != original {
		t.Error("expected unchanged line with empty term")
	}
}

func TestHighlightLineMatch(t *testing.T) {
	result := HighlightLine("hello world", "world")
	plain := stripAnsi(result)
	if !strings.Contains(plain, "hello") || !strings.Contains(plain, "world") {
		t.Errorf("expected full text preserved in highlighted line, got: %s", plain)
	}
}

func TestHighlightLineCaseInsensitive(t *testing.T) {
	result := HighlightLine("Hello World", "hello")
	plain := stripAnsi(result)
	if !strings.Contains(plain, "Hello") {
		t.Errorf("expected original casing preserved, got: %s", plain)
	}
}

func TestAgentLogSearchIntegration(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 40)
	al.SetSize(80, 40)

	al.Update(MsgTextChunk{NodeID: "n1", Text: "line one\n"})
	al.Update(MsgTextChunk{NodeID: "n1", Text: "line two\n"})
	al.Update(MsgTextChunk{NodeID: "n1", Text: "line three\n"})

	// Activate search.
	al.Search().Activate()
	al.Search().term = "two"
	al.Search().UpdateMatches(al.lines)

	view := al.View()
	if !strings.Contains(view, "/") {
		t.Error("expected search bar visible in view")
	}
	if al.Search().MatchCount() != 1 {
		t.Errorf("expected 1 match for 'two', got %d", al.Search().MatchCount())
	}
}

func TestFormatMatchStatus(t *testing.T) {
	tests := []struct {
		total, current int
		want           string
	}{
		{0, 0, "no matches"},
		{1, 0, "1/1"},
		{5, 2, "3/5"},
		{15, 14, "15/15"}, // double-digit regression test
	}
	for _, tt := range tests {
		got := formatMatchStatus(tt.total, tt.current)
		if got != tt.want {
			t.Errorf("formatMatchStatus(%d, %d) = %q, want %q", tt.total, tt.current, got, tt.want)
		}
	}
}

func TestSearchWithVerbosityFilter(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 40)
	al.SetSize(80, 40)

	al.Update(MsgTextChunk{NodeID: "n1", Text: "general error line\n"})
	al.Update(MsgAgentError{NodeID: "n1", Error: "real error"})
	al.Update(MsgTextChunk{NodeID: "n1", Text: "another general\n"})

	// Set verbosity to errors only.
	al.CycleVerbosity() // Tools
	al.CycleVerbosity() // Errors

	// Search for "error" — should only match within the filtered (errors) view.
	al.Search().Activate()
	al.Search().term = "error"

	// Trigger view to rebuild filter and search.
	view := al.View()
	_ = view

	// The match count should only reflect lines visible in errors mode.
	// "general error line" is LineGeneral, so hidden. "real error" is LineError, visible.
	if al.Search().MatchCount() != 1 {
		t.Errorf("expected 1 match in errors mode, got %d", al.Search().MatchCount())
	}
}

func TestSearchEnterConfirmsKeepsHighlights(t *testing.T) {
	sb := NewSearchBar()
	sb.Activate()
	// Simulate typing and pressing Enter.
	sb.term = "test"
	sb.matches = []int{0, 3, 7}

	_, consumed := sb.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !consumed {
		t.Error("expected Enter consumed by search bar")
	}
	// Bar should be hidden (not active) but term and matches persist.
	if sb.Active() {
		t.Error("expected search bar inactive after Enter (confirmed)")
	}
	if sb.Term() != "test" {
		t.Error("expected term preserved after Enter")
	}
	if sb.MatchCount() != 3 {
		t.Error("expected matches preserved after Enter")
	}
}
