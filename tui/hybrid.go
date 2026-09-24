// ABOUTME: Hybrid gate content — radio selection of known labels with optional freeform "other" input.
// ABOUTME: Replaces pure freeform when a human gate has labeled outgoing edges.
package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// HybridContent presents labeled options as a radio list with a freeform
// textarea for custom input. Selecting a label submits it directly.
// Selecting "other" focuses the textarea for custom text.
type HybridContent struct {
	labelPicker
	prompt string
	width  int
}

// NewHybridContent creates a hybrid gate with labeled options and freeform fallback.
func NewHybridContent(prompt string, labels []string, defaultLabel string, replyCh chan<- string) *HybridContent {
	ta := textarea.New()
	ta.Placeholder = "Type specific feedback..."
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.SetWidth(60)
	ta.SetHeight(3)
	ta.MaxHeight = 10
	ta.CharLimit = 0

	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Base = lipgloss.NewStyle().BorderForeground(ColorLabel)
	ta.BlurredStyle.Base = ta.FocusedStyle.Base
	ta.Blur()

	return &HybridContent{
		labelPicker: labelPicker{
			labels:   labels,
			cursor:   findDefaultCursor(labels, defaultLabel),
			textarea: ta,
			replyCh:  replyCh,
		},
		prompt: prompt,
	}
}

// SetWidth adjusts the content width for prompt wrapping and textarea.
func (h *HybridContent) SetWidth(w int) {
	h.width = w
	inner := w - 8
	if inner < 20 {
		inner = 20
	}
	h.textarea.SetWidth(inner)
}

// Update handles navigation, selection, and textarea input.
func (h *HybridContent) Update(msg tea.Msg) tea.Cmd {
	return h.update(msg, h.updateRadioMode)
}

// updateRadioMode handles keys when navigating the radio list.
func (h *HybridContent) updateRadioMode(km tea.KeyMsg) tea.Cmd {
	if cmd := h.handleRadioNavKey(km); cmd != nil {
		return cmd
	}
	return h.handleRadioActionKey(km)
}

// handleRadioNavKey handles Up/Down/Enter navigation keys.
func (h *HybridContent) handleRadioNavKey(km tea.KeyMsg) tea.Cmd {
	switch km.Type {
	case tea.KeyUp, tea.KeyDown:
		h.moveCursor(km)
	case tea.KeyEnter:
		if h.isOnOther() {
			h.onOther = true
			h.textarea.Focus()
			return nil
		}
		return h.submitLabel(h.labels[h.cursor])
	}
	return nil
}

// handleRadioActionKey handles Ctrl+S (submit) and Esc (cancel) action keys.
func (h *HybridContent) handleRadioActionKey(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "ctrl+s":
		if h.isOnOther() {
			return h.submitOther()
		}
		return h.submitLabel(h.labels[h.cursor])
	case "esc":
		return h.cancel()
	}
	return nil
}

// View renders the prompt, radio options, and textarea.
func (h *HybridContent) View() string {
	var sb strings.Builder

	promptWidth := h.width - 4
	if promptWidth < 20 {
		promptWidth = 20
	}
	promptStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorReadout).Width(promptWidth)
	sb.WriteString(promptStyle.Render(h.prompt))
	sb.WriteString("\n\n")

	h.writeChoices(&sb, "↑↓ navigate  enter select  ctrl+s submit  esc cancel")

	return sb.String()
}
