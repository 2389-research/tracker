// ABOUTME: Bubbletea model for arrow-key driven choice selection at human gate nodes.
// ABOUTME: Renders a styled list of choices; enter confirms, esc cancels.
package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
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
	height        int
	vp            viewport.Model
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
func (m *ChoiceModel) SetWidth(w int) {
	m.width = w
	m.refreshViewport()
}

// SetHeight sets a maximum height for the component. When height > 0, the
// prompt text is rendered inside a scrollable viewport while the choice list
// and hint text remain fixed at the bottom. When height is 0, the component
// renders without any height constraint (backward-compatible default).
func (m *ChoiceModel) SetHeight(h int) {
	m.height = h
	m.refreshViewport()
}

// refreshViewport initializes the internal viewport with proper dimensions and
// content so that PgUp/PgDown scroll events actually work. Called from SetWidth
// and SetHeight since both affect the viewport layout.
func (m *ChoiceModel) refreshViewport() {
	if m.height <= 0 || m.width <= 0 {
		return
	}
	footer := m.choicesAndHints()
	footerLines := strings.Count(footer, "\n") + 1
	vpHeight := m.height - footerLines - 1
	if vpHeight < 1 {
		vpHeight = 1
	}
	m.vp = viewport.New(m.width, vpHeight)
	m.vp.SetContent(render.Prompt(m.prompt, m.width))
}

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
		case "pgup", "pgdown":
			// Route page-up/page-down to the viewport when height is set
			if m.height > 0 {
				var cmd tea.Cmd
				m.vp, cmd = m.vp.Update(msg)
				return m, cmd
			}
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
	case tea.MouseMsg:
		// Route mouse messages to viewport when height is set
		if m.height > 0 {
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

// choicesAndHints renders the interactive choice list and hint text as a
// fixed-height block. This is used by View() both in unbounded mode and as
// the fixed footer when viewport scrolling is active.
func (m ChoiceModel) choicesAndHints() string {
	var sb strings.Builder
	for i, choice := range m.choices {
		if i == m.cursor {
			cursor := choiceCursorStyle.Render("▶ ")
			sb.WriteString(choiceSelectedStyle.Render(cursor + choice))
		} else {
			sb.WriteString(choiceNormalStyle.Render("  " + choice))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	hint := "↑/↓ navigate  enter select  esc cancel"
	if m.height > 0 {
		hint = "↑/↓ navigate  enter select  esc cancel  pgup/pgdn scroll"
	}
	sb.WriteString(lipgloss.NewStyle().Faint(true).Render(hint))
	return sb.String()
}

// View renders the choice list.
func (m ChoiceModel) View() string {
	if m.done || m.cancelled {
		return ""
	}

	// When no height constraint is set, render everything unbounded.
	if m.height <= 0 {
		var sb strings.Builder
		sb.WriteString(render.Prompt(m.prompt, m.width))
		sb.WriteString("\n\n")
		sb.WriteString(m.choicesAndHints())
		return sb.String()
	}

	// Height-constrained mode: render prompt in viewport, choices fixed below.
	var sb strings.Builder
	sb.WriteString(m.vp.View())
	sb.WriteString("\n")
	sb.WriteString(m.choicesAndHints())
	return sb.String()
}

// IsDone reports whether the user confirmed a selection.
func (m ChoiceModel) IsDone() bool { return m.done }

// IsCancelled reports whether the user pressed Esc.
func (m ChoiceModel) IsCancelled() bool { return m.cancelled }

// Selected returns the confirmed choice value (only valid when IsDone is true).
func (m ChoiceModel) Selected() string { return m.selected }
