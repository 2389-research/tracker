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

// Cancellable is an optional interface for modal content that can be
// cancelled externally (e.g., on Ctrl+C quit). Implementations should
// close their reply channel to unblock the pipeline handler.
type Cancellable interface {
	Cancel()
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

// FullscreenContent is an optional interface for modal content that wants
// to fill the entire terminal instead of being centered in a bordered box.
type FullscreenContent interface {
	IsFullscreen() bool
}

// Show displays the modal with the given content.
func (m *Modal) Show(content ModalContent) {
	m.content = content
	m.visible = true
	m.propagateSize()
}

// Hide removes the modal from view.
func (m *Modal) Hide() {
	m.visible = false
	m.content = nil
}

// CancelAndHide cancels the modal content (closing reply channels)
// and hides it. Used on Ctrl+C to prevent pipeline goroutine hangs.
func (m *Modal) CancelAndHide() {
	if m.content != nil {
		if c, ok := m.content.(Cancellable); ok {
			c.Cancel()
		}
	}
	m.Hide()
}

// Visible reports whether the modal is currently displayed.
func (m *Modal) Visible() bool {
	return m.visible
}

// SetSize updates the terminal dimensions used for centering.
func (m *Modal) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.propagateSize()
}

// propagateSize sends dimensions to content types that need them.
func (m *Modal) propagateSize() {
	switch c := m.content.(type) {
	case *FreeformContent:
		c.SetWidth(m.width)
	case *HybridContent:
		c.SetWidth(m.width)
	case *ReviewContent:
		c.SetSize(m.width, m.height)
	case *ReviewHybridContent:
		c.SetSize(m.width, m.height)
	case *InterviewContent:
		c.SetSize(m.width, m.height)
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
// Fullscreen content fills the terminal; other content is centered in a box.
func (m *Modal) View(background string) string {
	if !m.visible || m.content == nil {
		return background
	}

	// Fullscreen content replaces the background entirely.
	if fs, ok := m.content.(FullscreenContent); ok && fs.IsFullscreen() {
		return m.content.View()
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
	return c.handleChoiceKey(km)
}

// handleChoiceKey processes a key event for choice navigation.
func (c *ChoiceContent) handleChoiceKey(km tea.KeyMsg) tea.Cmd {
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
		return c.confirmChoice()
	case tea.KeyEscape:
		return c.cancelChoice()
	}
	return nil
}

// confirmChoice selects the current choice and dismisses the modal.
func (c *ChoiceContent) confirmChoice() tea.Cmd {
	if len(c.choices) == 0 {
		return nil
	}
	c.done = true
	selected := c.choices[c.cursor]
	if c.replyCh != nil {
		c.replyCh <- selected
		c.replyCh = nil
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

// cancelChoice cancels the choice and dismisses the modal.
func (c *ChoiceContent) cancelChoice() tea.Cmd {
	c.done = true
	if c.replyCh != nil {
		close(c.replyCh)
		c.replyCh = nil
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

// Cancel implements Cancellable for external cancellation (e.g., Ctrl+C).
func (c *ChoiceContent) Cancel() {
	if !c.done && c.replyCh != nil {
		c.done = true
		close(c.replyCh)
		c.replyCh = nil
	}
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

// ── AutopilotContent ─────────────────────────────────────────────────────────

// AutopilotContent shows the autopilot's gate decision briefly before auto-closing.
// Read-only — the user can press Enter to dismiss early.
type AutopilotContent struct {
	prompt   string
	decision string
	replyCh  chan<- string
	closed   bool
}

// NewAutopilotContent creates a display-only modal showing the autopilot decision.
func NewAutopilotContent(prompt, decision string, replyCh chan<- string) *AutopilotContent {
	return &AutopilotContent{
		prompt:   prompt,
		decision: decision,
		replyCh:  replyCh,
	}
}

func (a *AutopilotContent) Update(msg tea.Msg) tea.Cmd {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && isDismissKey(keyMsg) {
		a.closeWithDecision()
	}
	return nil
}

// isDismissKey returns true for keys that dismiss the autopilot modal.
func isDismissKey(km tea.KeyMsg) bool {
	return km.Type == tea.KeyEnter || km.Type == tea.KeyEsc
}

// closeWithDecision sends the decision to the reply channel if not already closed.
func (a *AutopilotContent) closeWithDecision() {
	if a.closed {
		return
	}
	a.closed = true
	select {
	case a.replyCh <- a.decision:
	default:
	}
}

func (a *AutopilotContent) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("208"))
	decisionStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("82"))
	hintStyle := lipgloss.NewStyle().Faint(true)

	var sb strings.Builder
	sb.WriteString(titleStyle.Render("AUTOPILOT"))
	sb.WriteString("\n\n")

	// Truncate prompt for display
	prompt := a.prompt
	lines := strings.Split(prompt, "\n")
	if len(lines) > 3 {
		prompt = strings.Join(lines[:3], "\n") + "\n..."
	}
	if len(prompt) > 200 {
		prompt = prompt[:197] + "..."
	}
	sb.WriteString(prompt)
	sb.WriteString("\n\n")
	sb.WriteString(fmt.Sprintf("→ %s", decisionStyle.Render(a.decision)))
	sb.WriteString("\n\n")
	sb.WriteString(hintStyle.Render("auto-closing in 2s · enter to dismiss"))
	return sb.String()
}

func (a *AutopilotContent) Cancel() {
	if !a.closed {
		a.closed = true
		close(a.replyCh)
	}
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
			// Esc with empty textarea dismisses without submitting (cancel).
			// Esc with content submits (quick approval).
			if strings.TrimSpace(f.textarea.Value()) == "" {
				return f.cancel()
			}
			return f.submit()
		}
	}
	var cmd tea.Cmd
	f.textarea, cmd = f.textarea.Update(msg)
	return cmd
}

// Cancel implements Cancellable for external cancellation (e.g., Ctrl+C).
func (f *FreeformContent) Cancel() { f.cancel() }

// cancel dismisses the modal without submitting any value.
// Closes the reply channel so the pipeline handler unblocks.
func (f *FreeformContent) cancel() tea.Cmd {
	if f.done {
		return nil
	}
	f.done = true
	if f.replyCh != nil {
		close(f.replyCh)
		f.replyCh = nil
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

// submit sends the current textarea value and dismisses the modal.
func (f *FreeformContent) submit() tea.Cmd {
	val := strings.TrimSpace(f.textarea.Value())
	if val == "" {
		return nil
	}
	if f.done {
		return nil
	}
	f.done = true
	if f.replyCh != nil {
		f.replyCh <- val
		f.replyCh = nil
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
	sb.WriteString(hintStyle.Render("enter newline  ctrl+s submit  esc submit/cancel"))
	return sb.String()
}
