// ABOUTME: labelPicker — the radio list of edge labels plus an "other" freeform textarea.
// ABOUTME: HybridContent and ReviewHybridContent embed it, so both gates answer and cancel the same way.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// labelPicker holds a labeled gate's answer state: the radio cursor, the
// "other" textarea, and the reply channel it answers on exactly once.
type labelPicker struct {
	labels   []string
	cursor   int  // index into labels + "other"
	onOther  bool // true when textarea is focused
	textarea textarea.Model
	replyCh  chan<- string
	done     bool
}

// findDefaultCursor returns the cursor index for the given default label (case-insensitive).
func findDefaultCursor(labels []string, defaultLabel string) int {
	if defaultLabel == "" {
		return 0
	}
	for i, l := range labels {
		if strings.EqualFold(l, defaultLabel) {
			return i
		}
	}
	return 0
}

// totalOptions returns the count of labels + 1 for "other".
func (p *labelPicker) totalOptions() int { return len(p.labels) + 1 }

// isOnOther returns true if cursor is on the "other" option.
func (p *labelPicker) isOnOther() bool { return p.cursor >= len(p.labels) }

// update routes a message until the gate answers: keys go to the textarea
// while it is focused and otherwise to radioKey, the gate's own handler for
// the radio list. Other messages reach only a focused textarea.
func (p *labelPicker) update(msg tea.Msg, radioKey func(tea.KeyMsg) tea.Cmd) tea.Cmd {
	if p.done {
		return nil
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		if p.onOther {
			var cmd tea.Cmd
			p.textarea, cmd = p.textarea.Update(msg)
			return cmd
		}
		return nil
	}

	// When textarea is active, handle its keys.
	if p.onOther {
		return p.updateOtherMode(km)
	}
	return radioKey(km)
}

// updateOtherMode handles keys when the textarea is focused.
func (p *labelPicker) updateOtherMode(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "ctrl+s":
		return p.submitOther()
	case "esc":
		p.onOther = false
		p.textarea.Blur()
		return nil
	case "up":
		p.onOther = false
		p.textarea.Blur()
		p.cursor = len(p.labels) // stay on "other"
		return nil
	}
	var cmd tea.Cmd
	p.textarea, cmd = p.textarea.Update(km)
	return cmd
}

// moveCursor handles Up/Down cursor movement.
func (p *labelPicker) moveCursor(km tea.KeyMsg) {
	switch km.Type {
	case tea.KeyUp:
		if p.cursor > 0 {
			p.cursor--
		}
	case tea.KeyDown:
		if p.cursor < p.totalOptions()-1 {
			p.cursor++
		}
	}
}

func (p *labelPicker) submitLabel(label string) tea.Cmd {
	if p.done {
		return nil
	}
	p.done = true
	if p.replyCh != nil {
		p.replyCh <- label
		p.replyCh = nil
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

func (p *labelPicker) submitOther() tea.Cmd {
	val := strings.TrimSpace(p.textarea.Value())
	if val == "" || p.done {
		return nil
	}
	p.done = true
	if p.replyCh != nil {
		p.replyCh <- val
		p.replyCh = nil
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

// Cancel implements Cancellable for external cancellation (e.g., Ctrl+C).
func (p *labelPicker) Cancel() { p.cancel() }

func (p *labelPicker) cancel() tea.Cmd {
	if p.done {
		return nil
	}
	p.done = true
	if p.replyCh != nil {
		close(p.replyCh)
		p.replyCh = nil
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

// writeChoices renders the radio options, the "other" option, the textarea
// while it is focused, and the keyboard hint. radioHint is the hint for the
// radio list; the textarea's hint is the same for every gate.
func (p *labelPicker) writeChoices(sb *strings.Builder, radioHint string) {
	p.writeRadioOptions(sb)
	p.writeOtherOption(sb)

	if p.onOther {
		sb.WriteString("\n")
		sb.WriteString(p.textarea.View())
		sb.WriteString("\n")
	}

	hint := radioHint
	if p.onOther {
		hint = "type feedback  ctrl+s submit  esc back to options  ↑ back"
	}
	sb.WriteString("\n")
	sb.WriteString(Styles.Muted.Render(hint))
}

// writeRadioOptions renders each labeled radio option.
func (p *labelPicker) writeRadioOptions(sb *strings.Builder) {
	selectedStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorGreen)
	normalStyle := lipgloss.NewStyle()
	for i, label := range p.labels {
		if i == p.cursor && !p.onOther {
			sb.WriteString(selectedStyle.Render(fmt.Sprintf("  ● %s", label)))
		} else {
			sb.WriteString(normalStyle.Render(fmt.Sprintf("  ○ %s", label)))
		}
		sb.WriteString("\n")
	}
}

// writeOtherOption renders the "other" radio option.
func (p *labelPicker) writeOtherOption(sb *strings.Builder) {
	selectedStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorGreen)
	normalStyle := lipgloss.NewStyle()
	switch {
	case p.isOnOther() && !p.onOther:
		sb.WriteString(selectedStyle.Render("  ● other (provide feedback)"))
	case p.onOther:
		sb.WriteString(selectedStyle.Render("  ● other:"))
	default:
		sb.WriteString(normalStyle.Render("  ○ other (provide feedback)"))
	}
	sb.WriteString("\n")
}
