// ABOUTME: Bubbletea model for arrow-key driven choice selection at human gate nodes.
// ABOUTME: Renders a styled list of choices; enter confirms, esc cancels.
package components

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/2389-research/tracker/tui/render"
)

// ChoiceDoneMsg is emitted when the user confirms a selection.
type ChoiceDoneMsg struct{ Value string }

// ChoiceCancelMsg is emitted when the user presses Esc.
type ChoiceCancelMsg struct{}

var (
	choicePromptStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	choiceSelectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")).PaddingLeft(1)
	choiceNormalStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("7")).PaddingLeft(1)
	choiceCursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
)

// ChoiceModel is a bubbletea Model for presenting a list of choices.
type ChoiceModel struct {
	prompt        string
	choices       []string
	cursor        int
	defaultChoice string
	done          bool
	cancelled     bool
	selected      string
	width         int
}

// NewChoiceModel creates a choice model with the given prompt, choices, and default.
// The cursor starts at the default choice if one is provided.
func NewChoiceModel(prompt string, choices []string, defaultChoice string) ChoiceModel {
	cursor := 0
	if defaultChoice != "" {
		for i, c := range choices {
			if c == defaultChoice {
				cursor = i
				break
			}
		}
	}
	return ChoiceModel{
		prompt:        prompt,
		choices:       choices,
		defaultChoice: defaultChoice,
		cursor:        cursor,
		width:         76,
	}
}

// SetWidth updates the width used for rendering the prompt.
func (m *ChoiceModel) SetWidth(w int) { m.width = w }

// Init satisfies tea.Model; no initial command needed.
func (m ChoiceModel) Init() tea.Cmd { return nil }

// Update handles keyboard input.
func (m ChoiceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		case "enter", " ":
			if len(m.choices) > 0 {
				m.selected = m.choices[m.cursor]
				m.done = true
				return m, func() tea.Msg { return ChoiceDoneMsg{Value: m.selected} }
			}
		case "esc", "ctrl+c":
			m.cancelled = true
			return m, func() tea.Msg { return ChoiceCancelMsg{} }
		default:
			// Number shortcuts: 1-9 select directly
			if len(msg.String()) == 1 {
				var idx int
				if _, err := fmt.Sscanf(msg.String(), "%d", &idx); err == nil {
					if idx >= 1 && idx <= len(m.choices) {
						m.cursor = idx - 1
						m.selected = m.choices[m.cursor]
						m.done = true
						return m, func() tea.Msg { return ChoiceDoneMsg{Value: m.selected} }
					}
				}
			}
		}
	}
	return m, nil
}

// View renders the choice list.
func (m ChoiceModel) View() string {
	if m.done || m.cancelled {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(render.Prompt(m.prompt, m.width))
	sb.WriteString("\n\n")

	for i, choice := range m.choices {
		cursor := "  "
		if i == m.cursor {
			cursor = choiceCursorStyle.Render("▶ ")
			sb.WriteString(choiceSelectedStyle.Render(cursor + choice))
		} else {
			sb.WriteString(choiceNormalStyle.Render("  " + choice))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(lipgloss.NewStyle().Faint(true).Render("↑/↓ navigate  enter select  esc cancel"))
	return sb.String()
}

// IsDone reports whether the user confirmed a selection.
func (m ChoiceModel) IsDone() bool { return m.done }

// IsCancelled reports whether the user pressed Esc.
func (m ChoiceModel) IsCancelled() bool { return m.cancelled }

// Selected returns the confirmed choice value (only valid when IsDone is true).
func (m ChoiceModel) Selected() string { return m.selected }
