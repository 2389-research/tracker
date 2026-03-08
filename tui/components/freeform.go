// ABOUTME: Bubbletea model for styled freeform text input at human gate nodes.
// ABOUTME: Enter submits (non-empty), Esc cancels.
package components

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/2389-research/tracker/tui/render"
)

// FreeformDoneMsg is emitted when the user submits a non-empty freeform response.
type FreeformDoneMsg struct{ Value string }

// FreeformCancelMsg is emitted when the user presses Esc.
type FreeformCancelMsg struct{}

var (
	freeformPromptStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	freeformBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("8")).
				Padding(0, 1)
	freeformErrorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Italic(true)
)

// FreeformModel is a bubbletea Model for capturing open-ended text input.
type FreeformModel struct {
	prompt    string
	input     textinput.Model
	done      bool
	cancelled bool
	err       string
	width     int
}

// NewFreeformModel creates a freeform input model with the given prompt.
func NewFreeformModel(prompt string) FreeformModel {
	ti := textinput.New()
	ti.Placeholder = "Type your response…"
	ti.Focus()
	ti.CharLimit = 500
	ti.Width = 60

	return FreeformModel{
		prompt: prompt,
		input:  ti,
		width:  76,
	}
}

// SetWidth updates the width used for rendering the prompt and text input.
func (m *FreeformModel) SetWidth(w int) {
	m.width = w
	m.input.Width = w - 4
}

// Init satisfies tea.Model; focuses the text input.
func (m FreeformModel) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles keyboard input.
func (m FreeformModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			val := strings.TrimSpace(m.input.Value())
			if val == "" {
				m.err = "Response cannot be empty"
				return m, nil
			}
			m.done = true
			v := val
			return m, func() tea.Msg { return FreeformDoneMsg{Value: v} }
		case tea.KeyEsc, tea.KeyCtrlC:
			m.cancelled = true
			return m, func() tea.Msg { return FreeformCancelMsg{} }
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// View renders the freeform input.
func (m FreeformModel) View() string {
	if m.done || m.cancelled {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(render.Prompt(m.prompt, m.width))
	sb.WriteString("\n\n")
	sb.WriteString(freeformBorderStyle.Width(m.width - 4).Render(m.input.View()))
	sb.WriteString("\n")

	if m.err != "" {
		sb.WriteString(freeformErrorStyle.Render(m.err))
		sb.WriteString("\n")
	}

	sb.WriteString(lipgloss.NewStyle().Faint(true).Render("enter submit  esc cancel"))
	return sb.String()
}

// IsDone reports whether the user submitted a response.
func (m FreeformModel) IsDone() bool { return m.done }

// IsCancelled reports whether the user pressed Esc.
func (m FreeformModel) IsCancelled() bool { return m.cancelled }

// Value returns the submitted text (only valid when IsDone is true).
func (m FreeformModel) Value() string { return strings.TrimSpace(m.input.Value()) }
