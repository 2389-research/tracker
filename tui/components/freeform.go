// ABOUTME: Bubbletea model for styled freeform text input at human gate nodes.
// ABOUTME: Enter submits (non-empty), Esc cancels.
package components

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
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
	height    int
	vp        viewport.Model
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

// SetHeight sets a maximum height for the component. When height > 0, the
// prompt text is rendered inside a scrollable viewport while the text input
// and hint text remain fixed at the bottom. When height is 0, the component
// renders without any height constraint (backward-compatible default).
func (m *FreeformModel) SetHeight(h int) { m.height = h }

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
		case tea.KeyPgUp, tea.KeyPgDown:
			// Route page-up/page-down to the viewport when height is set
			if m.height > 0 {
				var cmd tea.Cmd
				m.vp, cmd = m.vp.Update(msg)
				return m, cmd
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

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// inputAndHints renders the bordered text input, error text, and hint line as
// a fixed-height block. This is used by View() both in unbounded mode and as
// the fixed footer when viewport scrolling is active.
func (m FreeformModel) inputAndHints() string {
	var sb strings.Builder
	sb.WriteString(freeformBorderStyle.Width(m.width - 4).Render(m.input.View()))
	sb.WriteString("\n")

	if m.err != "" {
		sb.WriteString(freeformErrorStyle.Render(m.err))
		sb.WriteString("\n")
	}

	hint := "enter submit  esc cancel"
	if m.height > 0 {
		hint = "enter submit  esc cancel  pgup/pgdn scroll"
	}
	sb.WriteString(lipgloss.NewStyle().Faint(true).Render(hint))
	return sb.String()
}

// View renders the freeform input.
func (m FreeformModel) View() string {
	if m.done || m.cancelled {
		return ""
	}

	// When no height constraint is set, render everything unbounded.
	if m.height <= 0 {
		var sb strings.Builder
		sb.WriteString(render.Prompt(m.prompt, m.width))
		sb.WriteString("\n\n")
		sb.WriteString(m.inputAndHints())
		return sb.String()
	}

	// Height-constrained mode: render prompt in a viewport, input fixed below.
	footer := m.inputAndHints()
	footerLines := strings.Count(footer, "\n") + 1
	// Reserve 1 line for the blank separator between prompt and input
	vpHeight := m.height - footerLines - 1
	if vpHeight < 1 {
		vpHeight = 1
	}

	// Re-create viewport on each View() call (value receiver means mutations
	// don't persist). Preserve the scroll offset from m.vp which IS persisted
	// through Update().
	vp := viewport.New(m.width, vpHeight)
	vp.SetContent(render.Prompt(m.prompt, m.width))
	vp.YOffset = m.vp.YOffset

	var sb strings.Builder
	sb.WriteString(vp.View())
	sb.WriteString("\n")
	sb.WriteString(footer)
	return sb.String()
}

// IsDone reports whether the user submitted a response.
func (m FreeformModel) IsDone() bool { return m.done }

// IsCancelled reports whether the user pressed Esc.
func (m FreeformModel) IsCancelled() bool { return m.cancelled }

// Value returns the submitted text (only valid when IsDone is true).
func (m FreeformModel) Value() string { return strings.TrimSpace(m.input.Value()) }
