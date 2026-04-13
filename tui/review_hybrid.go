// ABOUTME: ReviewHybridContent — scrollable context viewport with radio selection + freeform below.
// ABOUTME: Used when a labeled human gate has substantial context (agent output, errors).
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// ReviewHybridContent shows a glamour-rendered scrollable viewport with
// radio label selection and an "other" freeform option below. Used when
// an escalation gate has both context content (what failed) and labeled
// options (accept/retry/abandon), plus the ability to provide custom feedback.
type ReviewHybridContent struct {
	viewport viewport.Model
	labels   []string
	cursor   int
	onOther  bool // true when textarea is focused
	textarea textarea.Model
	replyCh  chan<- string
	done     bool
	width    int
	height   int
}

// IsFullscreen signals the modal to use the full terminal.
func (r *ReviewHybridContent) IsFullscreen() bool { return true }

// NewReviewHybridContent creates a split view: scrollable context on top,
// radio options + freeform textarea on bottom.
func NewReviewHybridContent(label, context string, labels []string, defaultLabel string, replyCh chan<- string, width, height int) *ReviewHybridContent {
	if width < 40 {
		width = 80
	}
	if height < 10 {
		height = 24
	}

	rendered := renderReviewHybridMarkdown(buildReviewHybridMarkdown(label, context), width-4)
	ta := buildReviewHybridTextarea(width)

	radioHeight := len(labels) + 5 // labels + other + hint + divider + blank
	vpHeight := height - radioHeight - 1
	if vpHeight < 5 {
		vpHeight = 5
	}

	vp := viewport.New(width-2, vpHeight)
	vp.SetContent(rendered)
	vp.Style = lipgloss.NewStyle().Padding(0, 1)

	return &ReviewHybridContent{
		viewport: vp,
		labels:   labels,
		cursor:   findDefaultCursor(labels, defaultLabel),
		textarea: ta,
		replyCh:  replyCh,
		width:    width,
		height:   height,
	}
}

// buildReviewHybridMarkdown combines label and context into a single markdown string.
func buildReviewHybridMarkdown(label, context string) string {
	if label != "" && context != "" {
		return label + "\n\n---\n\n" + context
	}
	if label != "" {
		return label
	}
	return context
}

// buildReviewHybridTextarea creates and configures the "other" textarea.
func buildReviewHybridTextarea(width int) textarea.Model {
	ta := textarea.New()
	ta.Placeholder = "Type specific feedback or instructions..."
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.SetWidth(width - 6)
	ta.SetHeight(3)
	ta.MaxHeight = 6
	ta.CharLimit = 0
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Base = lipgloss.NewStyle().BorderForeground(ColorLabel)
	ta.BlurredStyle.Base = ta.FocusedStyle.Base
	ta.Blur()
	return ta
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

// SetSize updates dimensions.
func (r *ReviewHybridContent) SetSize(w, h int) {
	r.width = w
	r.height = h
	radioHeight := len(r.labels) + 5
	vpHeight := h - radioHeight - 1
	if r.onOther {
		vpHeight -= 4 // textarea takes extra space
	}
	if vpHeight < 5 {
		vpHeight = 5
	}
	r.viewport.Width = w - 2
	r.viewport.Height = vpHeight
	r.textarea.SetWidth(w - 6)
}

// totalOptions returns labels + 1 for the "other" option.
func (r *ReviewHybridContent) totalOptions() int { return len(r.labels) + 1 }

// isOnOther returns true if cursor is on the "other" option.
func (r *ReviewHybridContent) isOnOther() bool { return r.cursor >= len(r.labels) }

// Update handles navigation and selection.
func (r *ReviewHybridContent) Update(msg tea.Msg) tea.Cmd {
	if r.done {
		return nil
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		if r.onOther {
			var cmd tea.Cmd
			r.textarea, cmd = r.textarea.Update(msg)
			return cmd
		}
		return nil
	}

	// When textarea is active, handle its keys.
	if r.onOther {
		return r.updateOtherMode(km)
	}
	return r.updateRadioMode(km)
}

// updateOtherMode handles keys when the textarea is focused.
func (r *ReviewHybridContent) updateOtherMode(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "ctrl+s":
		return r.submitOther()
	case "esc":
		r.onOther = false
		r.textarea.Blur()
		return nil
	case "up":
		r.onOther = false
		r.textarea.Blur()
		r.cursor = len(r.labels) // stay on "other"
		return nil
	}
	var cmd tea.Cmd
	r.textarea, cmd = r.textarea.Update(km)
	return cmd
}

// updateRadioMode handles keys when navigating the radio list.
func (r *ReviewHybridContent) updateRadioMode(km tea.KeyMsg) tea.Cmd {
	switch km.String() {
	case "pgup", "pgdown":
		var cmd tea.Cmd
		r.viewport, cmd = r.viewport.Update(km)
		return cmd
	case "ctrl+s", "enter":
		if r.isOnOther() {
			r.onOther = true
			r.textarea.Focus()
			return nil
		}
		if len(r.labels) > 0 {
			return r.submitLabel(r.labels[r.cursor])
		}
	case "esc":
		return r.cancel()
	}
	switch km.Type {
	case tea.KeyUp:
		if r.cursor > 0 {
			r.cursor--
		}
	case tea.KeyDown:
		if r.cursor < r.totalOptions()-1 {
			r.cursor++
		}
	}
	return nil
}

func (r *ReviewHybridContent) submitLabel(label string) tea.Cmd {
	if r.done {
		return nil
	}
	r.done = true
	if r.replyCh != nil {
		r.replyCh <- label
		r.replyCh = nil
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

func (r *ReviewHybridContent) submitOther() tea.Cmd {
	val := strings.TrimSpace(r.textarea.Value())
	if val == "" || r.done {
		return nil
	}
	r.done = true
	if r.replyCh != nil {
		r.replyCh <- val
		r.replyCh = nil
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

// Cancel implements Cancellable.
func (r *ReviewHybridContent) Cancel() { r.cancel() }

func (r *ReviewHybridContent) cancel() tea.Cmd {
	if r.done {
		return nil
	}
	r.done = true
	if r.replyCh != nil {
		close(r.replyCh)
		r.replyCh = nil
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

// View renders viewport + divider + radio options + other + textarea.
func (r *ReviewHybridContent) View() string {
	var sb strings.Builder

	sb.WriteString(r.viewport.View())
	sb.WriteString("\n")
	sb.WriteString(Styles.Muted.Render(fmt.Sprintf(
		"─── Review (%d%%) ── PgUp/PgDn scroll ───",
		int(r.viewport.ScrollPercent()*100))))
	sb.WriteString("\n")

	r.writeReviewRadioOptions(&sb)
	r.writeReviewOtherOption(&sb)

	if r.onOther {
		sb.WriteString("\n")
		sb.WriteString(r.textarea.View())
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(Styles.Muted.Render(r.reviewHintText()))

	return sb.String()
}

// writeReviewRadioOptions renders each labeled radio option for ReviewHybridContent.
func (r *ReviewHybridContent) writeReviewRadioOptions(sb *strings.Builder) {
	selectedStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorGreen)
	normalStyle := lipgloss.NewStyle()
	for i, label := range r.labels {
		if i == r.cursor && !r.onOther {
			sb.WriteString(selectedStyle.Render(fmt.Sprintf("  ● %s", label)))
		} else {
			sb.WriteString(normalStyle.Render(fmt.Sprintf("  ○ %s", label)))
		}
		sb.WriteString("\n")
	}
}

// writeReviewOtherOption renders the "other" radio option for ReviewHybridContent.
func (r *ReviewHybridContent) writeReviewOtherOption(sb *strings.Builder) {
	selectedStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorGreen)
	normalStyle := lipgloss.NewStyle()
	switch {
	case r.isOnOther() && !r.onOther:
		sb.WriteString(selectedStyle.Render("  ● other (provide feedback)"))
	case r.onOther:
		sb.WriteString(selectedStyle.Render("  ● other:"))
	default:
		sb.WriteString(normalStyle.Render("  ○ other (provide feedback)"))
	}
	sb.WriteString("\n")
}

// reviewHintText returns the keyboard hint string for ReviewHybridContent.
func (r *ReviewHybridContent) reviewHintText() string {
	if r.onOther {
		return "type feedback  ctrl+s submit  esc back to options  ↑ back"
	}
	return "↑↓ navigate  enter select  esc cancel  pgup/pgdn scroll"
}

func renderReviewHybridMarkdown(md string, width int) string {
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "[markdown rendering unavailable]\n\n" + md
	}
	rendered, err := r.Render(md)
	if err != nil {
		return "[markdown rendering unavailable]\n\n" + md
	}
	return strings.TrimSpace(rendered)
}
