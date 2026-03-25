// ABOUTME: ReviewHybridContent — scrollable context viewport with radio selection below.
// ABOUTME: Used when a labeled human gate has substantial context (agent output, errors).
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// ReviewHybridContent shows a glamour-rendered scrollable viewport with
// radio label selection below. Used when an escalation gate has both
// context content (what failed) and labeled options (accept/retry/abandon).
type ReviewHybridContent struct {
	viewport viewport.Model
	labels   []string
	cursor   int
	onOther  bool
	replyCh  chan<- string
	done     bool
	width    int
	height   int
}

// IsFullscreen signals the modal to use the full terminal.
func (r *ReviewHybridContent) IsFullscreen() bool { return true }

// NewReviewHybridContent creates a split view: scrollable context on top,
// radio options on bottom.
func NewReviewHybridContent(label, context string, labels []string, defaultLabel string, replyCh chan<- string, width, height int) *ReviewHybridContent {
	if width < 40 {
		width = 80
	}
	if height < 10 {
		height = 24
	}

	// Render context via glamour.
	vpWidth := width - 4
	rendered := renderReviewHybridMarkdown(context, vpWidth)
	if label != "" {
		header := lipgloss.NewStyle().Bold(true).Foreground(ColorReadout).Render(label)
		rendered = header + "\n\n" + rendered
	}

	// Radio options take ~(len(labels)+3) lines. Viewport gets the rest.
	radioHeight := len(labels) + 4 // labels + other + hints + divider
	vpHeight := height - radioHeight - 1
	if vpHeight < 5 {
		vpHeight = 5
	}

	vp := viewport.New(width-2, vpHeight)
	vp.SetContent(rendered)
	vp.Style = lipgloss.NewStyle().Padding(0, 1)

	cursor := 0
	if defaultLabel != "" {
		for i, l := range labels {
			if strings.EqualFold(l, defaultLabel) {
				cursor = i
				break
			}
		}
	}

	return &ReviewHybridContent{
		viewport: vp,
		labels:   labels,
		cursor:   cursor,
		replyCh:  replyCh,
		width:    width,
		height:   height,
	}
}

// SetSize updates dimensions.
func (r *ReviewHybridContent) SetSize(w, h int) {
	r.width = w
	r.height = h
	radioHeight := len(r.labels) + 4
	vpHeight := h - radioHeight - 1
	if vpHeight < 5 {
		vpHeight = 5
	}
	r.viewport.Width = w - 2
	r.viewport.Height = vpHeight
}

func (r *ReviewHybridContent) totalOptions() int { return len(r.labels) + 1 }
func (r *ReviewHybridContent) isOnOther() bool   { return r.cursor >= len(r.labels) }

// Update handles navigation and selection.
func (r *ReviewHybridContent) Update(msg tea.Msg) tea.Cmd {
	if r.done {
		return nil
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch km.String() {
	case "pgup", "pgdown":
		var cmd tea.Cmd
		r.viewport, cmd = r.viewport.Update(msg)
		return cmd
	case "ctrl+s", "enter":
		if r.isOnOther() {
			return nil // can't submit "other" without a textarea in this view
		}
		return r.submitLabel(r.labels[r.cursor])
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

// View renders viewport + divider + radio options.
func (r *ReviewHybridContent) View() string {
	var sb strings.Builder

	sb.WriteString(r.viewport.View())
	sb.WriteString("\n")

	// Divider.
	sb.WriteString(Styles.Muted.Render(fmt.Sprintf(
		"─── Review (%d%%) ── PgUp/PgDn scroll ───",
		int(r.viewport.ScrollPercent()*100))))
	sb.WriteString("\n")

	// Radio options.
	selectedStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorGreen)
	normalStyle := lipgloss.NewStyle()

	for i, label := range r.labels {
		if i == r.cursor {
			sb.WriteString(selectedStyle.Render(fmt.Sprintf("  ● %s", label)))
		} else {
			sb.WriteString(normalStyle.Render(fmt.Sprintf("  ○ %s", label)))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(Styles.Muted.Render("↑↓ navigate  enter select  esc cancel  pgup/pgdn scroll"))

	return sb.String()
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
