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

// SetWidth adjusts the textarea width.
func (h *HybridContent) SetWidth(w int) {
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
	h.done = true
	if h.replyCh != nil {
		h.replyCh <- label
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

func (h *HybridContent) submitOther() tea.Cmd {
	val := strings.TrimSpace(h.textarea.Value())
	if val == "" {
		return nil
	}
	h.done = true
	if h.replyCh != nil {
		h.replyCh <- val
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

func (h *HybridContent) cancel() tea.Cmd {
	h.done = true
	return func() tea.Msg { return MsgModalDismiss{} }
}

// View renders the prompt, radio options, and textarea.
func (h *HybridContent) View() string {
	var sb strings.Builder

	promptStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorReadout)
	sb.WriteString(promptStyle.Render(h.prompt))
	sb.WriteString("\n\n")

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

	// "Other" option.
	if h.isOnOther() && !h.onOther {
		sb.WriteString(selectedStyle.Render("  ● other (provide feedback)"))
	} else if h.onOther {
		sb.WriteString(selectedStyle.Render("  ● other:"))
	} else {
		sb.WriteString(normalStyle.Render("  ○ other (provide feedback)"))
	}
	sb.WriteString("\n")

	if h.onOther {
		sb.WriteString("\n")
		sb.WriteString(h.textarea.View())
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	hint := "↑↓ navigate  enter select  ctrl+s submit  esc cancel"
	if h.onOther {
		hint = "type feedback  ctrl+s submit  esc back to options  ↑ back"
	}
	sb.WriteString(Styles.Muted.Render(hint))

	return sb.String()
}
