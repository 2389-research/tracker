// ABOUTME: Modal overlay with pluggable content (choice selection, freeform input).
// ABOUTME: Renders a centered bordered box over background content using lipgloss.
package tui

import (
	"fmt"
	"strings"

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
func (m *Modal) SetSize(width, height int) {
	m.width = width
	m.height = height
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

// FreeformContent captures free-text input with basic line editing.
// Sends the submitted value on replyCh when Enter is pressed.
type FreeformContent struct {
	prompt  string
	buffer  []rune
	replyCh chan<- string
	done    bool
}

// NewFreeformContent creates a freeform input content model. If replyCh is nil,
// no reply is sent on submit (useful for rendering-only tests).
func NewFreeformContent(prompt string, replyCh chan<- string) *FreeformContent {
	return &FreeformContent{
		prompt:  prompt,
		replyCh: replyCh,
	}
}

// Update handles rune input, backspace, and Enter for submission.
func (f *FreeformContent) Update(msg tea.Msg) tea.Cmd {
	if f.done {
		return nil
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch km.Type {
	case tea.KeyRunes:
		f.buffer = append(f.buffer, km.Runes...)
	case tea.KeySpace:
		f.buffer = append(f.buffer, ' ')
	case tea.KeyBackspace:
		if len(f.buffer) > 0 {
			f.buffer = f.buffer[:len(f.buffer)-1]
		}
	case tea.KeyEnter:
		val := strings.TrimSpace(string(f.buffer))
		if val != "" {
			f.done = true
			if f.replyCh != nil {
				f.replyCh <- val
			}
			return func() tea.Msg { return MsgModalDismiss{} }
		}
	}
	return nil
}

// View renders the prompt and current text input buffer.
func (f *FreeformContent) View() string {
	var sb strings.Builder
	promptStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorReadout)
	sb.WriteString(promptStyle.Render(f.prompt))
	sb.WriteString("\n\n")

	inputStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorLabel).
		Padding(0, 1)
	sb.WriteString(inputStyle.Render(string(f.buffer) + "█"))
	sb.WriteString("\n\n")

	hintStyle := lipgloss.NewStyle().Faint(true)
	sb.WriteString(hintStyle.Render("enter submit"))
	return sb.String()
}
