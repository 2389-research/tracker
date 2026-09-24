// ABOUTME: ReviewHybridContent — scrollable context viewport with radio selection + freeform below.
// ABOUTME: Used when a labeled human gate has substantial context (agent output, errors).
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ReviewHybridContent shows a glamour-rendered scrollable viewport with
// radio label selection and an "other" freeform option below. Used when
// an escalation gate has both context content (what failed) and labeled
// options (accept/retry/abandon), plus the ability to provide custom feedback.
type ReviewHybridContent struct {
	labelPicker
	viewport viewport.Model
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

	rendered := renderMarkdownForReview(buildReviewHybridMarkdown(label, context), width-4)
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
		labelPicker: labelPicker{
			labels:   labels,
			cursor:   findDefaultCursor(labels, defaultLabel),
			textarea: ta,
			replyCh:  replyCh,
		},
		viewport: vp,
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

// Update handles navigation and selection.
func (r *ReviewHybridContent) Update(msg tea.Msg) tea.Cmd {
	return r.update(msg, r.updateRadioMode)
}

// updateRadioMode handles keys when navigating the radio list.
func (r *ReviewHybridContent) updateRadioMode(km tea.KeyMsg) tea.Cmd {
	if cmd := r.handleReviewActionKey(km); cmd != nil {
		return cmd
	}
	r.moveCursor(km)
	return nil
}

// handleReviewActionKey handles page, submit, and cancel keys.
func (r *ReviewHybridContent) handleReviewActionKey(km tea.KeyMsg) tea.Cmd {
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
	return nil
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

	r.writeChoices(&sb, "↑↓ navigate  enter select  esc cancel  pgup/pgdn scroll")

	return sb.String()
}

// (renderMarkdownForReview lives in review.go — shared across review modals, #395 C1)
