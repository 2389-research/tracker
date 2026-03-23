// ABOUTME: Modal overlay with pluggable content (choice selection, freeform input).
// ABOUTME: Renders a centered bordered box over background content using lipgloss.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ModalContent is the interface for content rendered inside the modal overlay.
type ModalContent interface {
	Update(msg tea.Msg) tea.Cmd
	View() string
}

// Modal renders a bordered overlay centered over background terminal content.
type Modal struct {
	width   int
	height  int
	content ModalContent
	visible bool
}

// NewModal creates a modal with the given terminal dimensions.
func NewModal(width, height int) *Modal {
	return &Modal{width: width, height: height}
}

// Show displays the modal with the given content.
func (m *Modal) Show(content ModalContent) {
	m.content = content
	m.visible = true
	if fc, ok := content.(*FreeformContent); ok {
		fc.SetWidth(m.width)
	}
}

// Hide removes the modal from view.
func (m *Modal) Hide() {
	m.visible = false
	m.content = nil
}

// Visible reports whether the modal is currently displayed.
func (m *Modal) Visible() bool {
	return m.visible
}

// SetSize updates the terminal dimensions used for centering.
// Propagates width to freeform content so the textarea fills the modal.
func (m *Modal) SetSize(width, height int) {
	m.width = width
	m.height = height
	if fc, ok := m.content.(*FreeformContent); ok {
		fc.SetWidth(width)
	}
}

// Update forwards messages to the modal content.
func (m *Modal) Update(msg tea.Msg) tea.Cmd {
	if !m.visible || m.content == nil {
		return nil
	}
	return m.content.Update(msg)
}

// View renders the modal overlaid on the given background content.
func (m *Modal) View(background string) string {
	if !m.visible || m.content == nil {
		return background
	}

	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(ColorBezel).
		Padding(1, 2)

	box := borderStyle.Render(m.content.View())

	if m.width <= 0 && m.height <= 0 {
		return box
	}

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box,
		lipgloss.WithWhitespaceChars(" "))
}

// ── ChoiceContent ────────────────────────────────────────────────────────────

// ChoiceContent presents a list of choices with arrow-key navigation.
// Sends the selected value on replyCh when Enter is pressed.
type ChoiceContent struct {
	prompt  string
	choices []string
	cursor  int
	replyCh chan<- string
	done    bool
}

// NewChoiceContent creates a choice content model. If replyCh is nil, no
// reply is sent on selection (useful for rendering-only tests).
func NewChoiceContent(prompt string, choices []string, replyCh chan<- string) *ChoiceContent {
	return &ChoiceContent{
		prompt:  prompt,
		choices: choices,
		replyCh: replyCh,
	}
}

// Update handles arrow keys and Enter for choice selection.
func (c *ChoiceContent) Update(msg tea.Msg) tea.Cmd {
	if c.done {
		return nil
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch km.Type {
	case tea.KeyUp:
		if c.cursor > 0 {
			c.cursor--
		}
	case tea.KeyDown:
		if c.cursor < len(c.choices)-1 {
			c.cursor++
		}
	case tea.KeyEnter:
		if len(c.choices) > 0 {
			c.done = true
			selected := c.choices[c.cursor]
			if c.replyCh != nil {
				c.replyCh <- selected
			}
			return func() tea.Msg { return MsgModalDismiss{} }
		}
	}
	return nil
}

// View renders the prompt and choice list with a cursor indicator.
func (c *ChoiceContent) View() string {
	var sb strings.Builder
	promptStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorReadout)
	sb.WriteString(promptStyle.Render(c.prompt))
	sb.WriteString("\n\n")

	for i, choice := range c.choices {
		if i == c.cursor {
			cursorStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorGreen)
			sb.WriteString(cursorStyle.Render(fmt.Sprintf("  > %s", choice)))
		} else {
			sb.WriteString(fmt.Sprintf("    %s", choice))
		}
		sb.WriteString("\n")
	}

	hintStyle := lipgloss.NewStyle().Faint(true)
	sb.WriteString("\n")
	sb.WriteString(hintStyle.Render("arrow keys navigate  enter select"))
	return sb.String()
}

// ── FreeformContent ──────────────────────────────────────────────────────────

// FreeformContent captures free-text input using a wrapping textarea.
// Enter inserts newlines; Ctrl+S submits. The textarea expands vertically
// as the user types, wrapping at the viewport width.
type FreeformContent struct {
	prompt   string
	textarea textarea.Model
	replyCh  chan<- string
	done     bool
}

// NewFreeformContent creates a freeform input content model with a wrapping
// textarea. If replyCh is nil, no reply is sent on submit (useful for tests).
func NewFreeformContent(prompt string, replyCh chan<- string) *FreeformContent {
	ta := textarea.New()
	ta.Placeholder = "Type your response..."
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.SetWidth(60)
	ta.SetHeight(4)
	ta.MaxHeight = 20
	ta.CharLimit = 0 // no limit
	ta.Focus()

	// Style the textarea to match the TUI palette.
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Base = lipgloss.NewStyle().
		BorderForeground(ColorLabel)
	ta.BlurredStyle.Base = ta.FocusedStyle.Base

	return &FreeformContent{
		prompt:   prompt,
		textarea: ta,
		replyCh:  replyCh,
	}
}

// SetWidth adjusts the textarea to fit the available modal width.
func (f *FreeformContent) SetWidth(w int) {
	// Account for modal padding and borders.
	innerWidth := w - 8
	if innerWidth < 20 {
		innerWidth = 20
	}
	f.textarea.SetWidth(innerWidth)
}

// Update handles keyboard input. Ctrl+S submits, everything else goes
// to the textarea (Enter inserts newlines, arrow keys navigate, etc.).
func (f *FreeformContent) Update(msg tea.Msg) tea.Cmd {
	if f.done {
		return nil
	}
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "ctrl+s":
			return f.submit()
		case "esc":
			// Also allow Esc to submit if there's content (quick approval).
			return f.submit()
		}
	}
	var cmd tea.Cmd
	f.textarea, cmd = f.textarea.Update(msg)
	return cmd
}

// submit sends the current textarea value and dismisses the modal.
func (f *FreeformContent) submit() tea.Cmd {
	val := strings.TrimSpace(f.textarea.Value())
	if val == "" {
		return nil
	}
	f.done = true
	if f.replyCh != nil {
		f.replyCh <- val
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

// View renders the prompt, wrapping textarea, and key hints.
func (f *FreeformContent) View() string {
	var sb strings.Builder
	promptStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorReadout)
	sb.WriteString(promptStyle.Render(f.prompt))
	sb.WriteString("\n\n")

	sb.WriteString(f.textarea.View())
	sb.WriteString("\n\n")

	hintStyle := lipgloss.NewStyle().Faint(true)
	sb.WriteString(hintStyle.Render("enter newline  ctrl+s submit  esc submit"))
	return sb.String()
}
