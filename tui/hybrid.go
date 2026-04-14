// ABOUTME: Hybrid gate content — radio selection of known labels with optional freeform "other" input.
// ABOUTME: Replaces pure freeform when a human gate has labeled outgoing edges.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// HybridContent presents labeled options as a radio list with a freeform
// textarea for custom input. Selecting a label submits it directly.
// Selecting "other" focuses the textarea for custom text.
type HybridContent struct {
	prompt   string
	labels   []string
	cursor   int // index into labels + "other"
	onOther  bool
	width    int
	textarea textarea.Model
	replyCh  chan<- string
	done     bool
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

	cursor := 0
	if defaultLabel != "" {
		for i, l := range labels {
			if strings.EqualFold(l, defaultLabel) {
				cursor = i
				break
			}
		}
	}

	return &HybridContent{
		prompt:   prompt,
		labels:   labels,
		cursor:   cursor,
		textarea: ta,
		replyCh:  replyCh,
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

// totalOptions returns the count of labels + 1 for "other".
func (h *HybridContent) totalOptions() int {
	return len(h.labels) + 1
}

// isOnOther returns true if cursor is on the "other" option.
func (h *HybridContent) isOnOther() bool {
	return h.cursor >= len(h.labels)
}

// Update handles navigation, selection, and textarea input.
func (h *HybridContent) Update(msg tea.Msg) tea.Cmd {
	if h.done {
		return nil
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		if h.onOther {
			var cmd tea.Cmd
			h.textarea, cmd = h.textarea.Update(msg)
			return cmd
		}
		return nil
	}
	if h.onOther {
		return h.updateOtherMode(km)
	}
	return h.updateRadioMode(km)
}

// updateOtherMode handles keys when the textarea is active.
func (h *HybridContent) updateOtherMode(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "ctrl+s":
		return h.submitOther()
	case "esc":
		h.onOther = false
		h.textarea.Blur()
		return nil
	case "up":
		h.onOther = false
		h.textarea.Blur()
		h.cursor = len(h.labels)
		return nil
	}
	var cmd tea.Cmd
	h.textarea, cmd = h.textarea.Update(km)
	return cmd
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
	case tea.KeyUp:
		if h.cursor > 0 {
			h.cursor--
		}
	case tea.KeyDown:
		if h.cursor < h.totalOptions()-1 {
			h.cursor++
		}
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

func (h *HybridContent) submitLabel(label string) tea.Cmd {
	if h.done {
		return nil
	}
	h.done = true
	if h.replyCh != nil {
		h.replyCh <- label
		h.replyCh = nil
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

func (h *HybridContent) submitOther() tea.Cmd {
	val := strings.TrimSpace(h.textarea.Value())
	if val == "" || h.done {
		return nil
	}
	h.done = true
	if h.replyCh != nil {
		h.replyCh <- val
		h.replyCh = nil
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

// Cancel implements Cancellable for external cancellation (e.g., Ctrl+C).
func (h *HybridContent) Cancel() { h.cancel() }

func (h *HybridContent) cancel() tea.Cmd {
	if h.done {
		return nil
	}
	h.done = true
	if h.replyCh != nil {
		close(h.replyCh)
		h.replyCh = nil
	}
	return func() tea.Msg { return MsgModalDismiss{} }
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

	h.writeRadioOptions(&sb)
	h.writeOtherOption(&sb)

	if h.onOther {
		sb.WriteString("\n")
		sb.WriteString(h.textarea.View())
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(Styles.Muted.Render(h.hintText()))

	return sb.String()
}

// writeRadioOptions renders each labeled radio option.
func (h *HybridContent) writeRadioOptions(sb *strings.Builder) {
	selectedStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorGreen)
	normalStyle := lipgloss.NewStyle()
	for i, label := range h.labels {
		if i == h.cursor && !h.onOther {
			sb.WriteString(selectedStyle.Render(fmt.Sprintf("  ● %s", label)))
		} else {
			sb.WriteString(normalStyle.Render(fmt.Sprintf("  ○ %s", label)))
		}
		sb.WriteString("\n")
	}
}

// writeOtherOption renders the "other" radio option.
func (h *HybridContent) writeOtherOption(sb *strings.Builder) {
	selectedStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorGreen)
	normalStyle := lipgloss.NewStyle()
	switch {
	case h.isOnOther() && !h.onOther:
		sb.WriteString(selectedStyle.Render("  ● other (provide feedback)"))
	case h.onOther:
		sb.WriteString(selectedStyle.Render("  ● other:"))
	default:
		sb.WriteString(normalStyle.Render("  ○ other (provide feedback)"))
	}
	sb.WriteString("\n")
}

// hintText returns the keyboard hint string based on current state.
func (h *HybridContent) hintText() string {
	if h.onOther {
		return "type feedback  ctrl+s submit  esc back to options  ↑ back"
	}
	return "↑↓ navigate  enter select  ctrl+s submit  esc cancel"
}
