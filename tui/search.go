// ABOUTME: SearchBar for the agent log — uses bubbles/textinput for inline search.
// ABOUTME: Highlights matching lines in yellow; n/N jump between matches.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// SearchBar provides incremental search within the agent log.
type SearchBar struct {
	input   textinput.Model
	active  bool
	term    string
	matches []int // indices into the agent log lines that match
	current int   // index into matches for n/N navigation
}

// NewSearchBar creates a search bar styled for the TUI.
func NewSearchBar() *SearchBar {
	ti := textinput.New()
	ti.Placeholder = "search..."
	ti.Prompt = "/ "
	ti.PromptStyle = lipgloss.NewStyle().Foreground(ColorAmber).Bold(true)
	ti.TextStyle = lipgloss.NewStyle().Foreground(ColorBrightText)
	ti.Width = 40
	return &SearchBar{input: ti}
}

// Active returns whether the search bar is currently displayed.
func (s *SearchBar) Active() bool { return s.active }

// Term returns the current search term.
func (s *SearchBar) Term() string { return s.term }

// Activate shows the search bar and focuses the input.
func (s *SearchBar) Activate() {
	s.active = true
	s.input.SetValue("")
	s.input.Focus()
	s.term = ""
	s.matches = nil
	s.current = 0
}

// Deactivate hides the search bar and clears the search.
func (s *SearchBar) Deactivate() {
	s.active = false
	s.input.Blur()
	s.term = ""
	s.matches = nil
	s.current = 0
}

// Confirm exits input mode but keeps the term and highlights active.
// The search bar is hidden but n/N navigation still works.
func (s *SearchBar) Confirm() {
	s.active = false
	s.input.Blur()
	// term and matches are preserved for n/N navigation.
}

// Update handles input events when the search bar is active.
// Returns a command and whether the event was consumed.
func (s *SearchBar) Update(msg tea.Msg) (tea.Cmd, bool) {
	if !s.active {
		return nil, false
	}

	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil, false
	}

	switch km.Type {
	case tea.KeyEscape:
		s.Deactivate()
		return nil, true
	case tea.KeyEnter:
		// Confirm search: exit input mode but keep term and highlights.
		s.Confirm()
		return nil, true
	default:
		// Forward to textinput.
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(msg)
		s.term = s.input.Value()
		return cmd, true
	}
}

// NextMatch advances to the next match index.
func (s *SearchBar) NextMatch() {
	if len(s.matches) == 0 {
		return
	}
	s.current = (s.current + 1) % len(s.matches)
}

// PrevMatch moves to the previous match index.
func (s *SearchBar) PrevMatch() {
	if len(s.matches) == 0 {
		return
	}
	s.current--
	if s.current < 0 {
		s.current = len(s.matches) - 1
	}
}

// CurrentMatchLine returns the line index of the current match, or -1.
func (s *SearchBar) CurrentMatchLine() int {
	if len(s.matches) == 0 {
		return -1
	}
	return s.matches[s.current]
}

// UpdateMatches rebuilds the match index list from the given lines.
// The indices stored in matches correspond to positions in the provided slice.
func (s *SearchBar) UpdateMatches(lines []styledLine) {
	s.matches = nil
	if s.term == "" {
		return
	}
	lower := strings.ToLower(s.term)
	for i, line := range lines {
		if strings.Contains(strings.ToLower(stripAnsi(line.text)), lower) {
			s.matches = append(s.matches, i)
		}
	}
	// Clamp current to valid range.
	if s.current >= len(s.matches) {
		s.current = 0
	}
}

// UpdateMatchesFiltered rebuilds match indices against a filtered subset of lines.
// filteredIndices maps positions in the filtered view back to the original line array.
func (s *SearchBar) UpdateMatchesFiltered(lines []styledLine, filteredIndices []int) {
	s.matches = nil
	if s.term == "" {
		return
	}
	lower := strings.ToLower(s.term)
	for i, origIdx := range filteredIndices {
		if origIdx < len(lines) {
			if strings.Contains(strings.ToLower(stripAnsi(lines[origIdx].text)), lower) {
				s.matches = append(s.matches, i)
			}
		}
	}
	if s.current >= len(s.matches) {
		s.current = 0
	}
}

// MatchCount returns the number of matches.
func (s *SearchBar) MatchCount() int { return len(s.matches) }

// View renders the search bar.
func (s *SearchBar) View() string {
	if !s.active {
		return ""
	}
	status := ""
	if s.term != "" {
		status = lipgloss.NewStyle().Foreground(ColorLabel).
			Render(strings.Repeat(" ", 2) + formatMatchStatus(len(s.matches), s.current))
	}
	return s.input.View() + status
}

// formatMatchStatus formats "N/M" match counter.
func formatMatchStatus(total, current int) string {
	if total == 0 {
		return "no matches"
	}
	return fmt.Sprintf("%d/%d", current+1, total)
}

// stripAnsi removes ANSI escape sequences for plain-text search matching.
func stripAnsi(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '~' {
				inEsc = false
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// HighlightLine highlights all occurrences of the search term in a styled line.
// Uses rune-aware operations for correct multi-byte character handling.
func HighlightLine(line, term string) string {
	if term == "" {
		return line
	}
	plain := stripAnsi(line)
	lowerPlain := strings.ToLower(plain)
	lowerTerm := strings.ToLower(term)

	// If no match in the plain text, return original.
	if !strings.Contains(lowerPlain, lowerTerm) {
		return line
	}

	// Rebuild with highlights on the plain text.
	var result strings.Builder
	pos := 0
	termLen := len(lowerTerm) // byte length matches since ToLower preserves byte count for ASCII
	for {
		idx := strings.Index(lowerPlain[pos:], lowerTerm)
		if idx < 0 {
			result.WriteString(Styles.PrimaryText.Render(plain[pos:]))
			break
		}
		if idx > 0 {
			result.WriteString(Styles.PrimaryText.Render(plain[pos : pos+idx]))
		}
		result.WriteString(Styles.SearchMatch.Render(plain[pos+idx : pos+idx+termLen]))
		pos += idx + termLen
	}
	return result.String()
}
