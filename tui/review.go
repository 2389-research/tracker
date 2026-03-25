// ABOUTME: Split-pane review content for long human gate prompts.
// ABOUTME: Glamour-rendered scrollable viewport on top, textarea on bottom. Used for plan approval.
package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// longPromptThreshold is the line count above which we use the split-pane
// review view instead of the simple freeform modal.
const longPromptThreshold = 20

// reviewChrome is the number of rows consumed by non-viewport elements:
// textarea(4) + divider(1) + header(1) + hint(1) + padding(2) = 9
const reviewChrome = 9

// ReviewContent presents a split-pane view for reviewing long content
// (e.g., execution plans). Top pane is a scrollable glamour-rendered
// viewport, bottom pane is a textarea for the user's response.
type ReviewContent struct {
	viewport viewport.Model
	textarea textarea.Model
	replyCh  chan<- string
	done     bool
	width    int
	height   int
	tmpFile  string // path to temp file with the plan markdown
}

// IsFullscreen signals the modal wrapper to skip centering and use the full terminal.
func (r *ReviewContent) IsFullscreen() bool { return true }

// NewReviewContent creates a split-pane review view. The markdown content is
// rendered via glamour in the viewport. A temp file is written so the user
// can open the plan in an external editor.
func NewReviewContent(prompt string, replyCh chan<- string, width, height int) *ReviewContent {
	if width < 40 {
		width = 80
	}
	if height < 10 {
		height = 24
	}

	// Split prompt into the gate label and the plan content.
	label, plan := splitPromptAndPlan(prompt)

	// Render plan as markdown via glamour.
	vpWidth := width - 4
	if vpWidth < 40 {
		vpWidth = 40
	}
	rendered := renderMarkdownForReview(plan, vpWidth)

	// Prepend the gate label as a styled header.
	if label != "" {
		header := lipgloss.NewStyle().Bold(true).Foreground(ColorReadout).Render(label)
		rendered = header + "\n\n" + rendered
	}

	// Calculate split: viewport gets most of the space, textarea gets 4 lines.
	vpHeight := height - reviewChrome
	if vpHeight < 5 {
		vpHeight = 5
	}

	vp := viewport.New(width-2, vpHeight)
	vp.SetContent(rendered)
	vp.Style = lipgloss.NewStyle().Padding(0, 1)

	ta := textarea.New()
	ta.Placeholder = "approve / adjust / reject (or type feedback)"
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.SetWidth(width - 4)
	ta.SetHeight(4)
	ta.CharLimit = 0
	ta.Focus()
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()

	// Write plan to temp file for external reading.
	tmpFile := writeTempPlan(plan)

	return &ReviewContent{
		viewport: vp,
		textarea: ta,
		replyCh:  replyCh,
		width:    width,
		height:   height,
		tmpFile:  tmpFile,
	}
}

// SetSize updates dimensions for the review panes.
func (r *ReviewContent) SetSize(w, h int) {
	r.width = w
	r.height = h
	vpHeight := h - reviewChrome
	if vpHeight < 5 {
		vpHeight = 5
	}
	r.viewport.Width = w - 2
	r.viewport.Height = vpHeight
	r.textarea.SetWidth(w - 4)
}

// Update routes keys: PgUp/PgDn/arrows to viewport, typing to textarea, Ctrl+S to submit.
func (r *ReviewContent) Update(msg tea.Msg) tea.Cmd {
	if r.done {
		return nil
	}
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "ctrl+s":
			return r.submit()
		case "esc":
			if strings.TrimSpace(r.textarea.Value()) == "" {
				return r.cancel()
			}
			return r.submit()
		case "pgup", "pgdown":
			var cmd tea.Cmd
			r.viewport, cmd = r.viewport.Update(msg)
			return cmd
		}
	}
	var cmd tea.Cmd
	r.textarea, cmd = r.textarea.Update(msg)
	return cmd
}

func (r *ReviewContent) submit() tea.Cmd {
	val := strings.TrimSpace(r.textarea.Value())
	if val == "" || r.done {
		return nil
	}
	r.done = true
	r.cleanup()
	if r.replyCh != nil {
		r.replyCh <- val
		r.replyCh = nil
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

// cleanup removes the temp file if one was created.
func (r *ReviewContent) cleanup() {
	if r.tmpFile != "" {
		os.Remove(r.tmpFile)
		r.tmpFile = ""
	}
}

// Cancel implements Cancellable for external cancellation (e.g., Ctrl+C).
func (r *ReviewContent) Cancel() { r.cancel() }

func (r *ReviewContent) cancel() tea.Cmd {
	if r.done {
		return nil
	}
	r.done = true
	r.cleanup()
	if r.replyCh != nil {
		close(r.replyCh)
		r.replyCh = nil
	}
	return func() tea.Msg { return MsgModalDismiss{} }
}

// View renders the split-pane: viewport, divider, textarea, hints.
func (r *ReviewContent) View() string {
	var sb strings.Builder

	// Viewport (scrollable plan).
	sb.WriteString(r.viewport.View())
	sb.WriteString("\n")

	// Divider with scroll hint and temp file path.
	divider := Styles.Muted.Render(fmt.Sprintf(
		"─── Plan Review (PgUp/PgDn to scroll, %d%%) ",
		int(r.viewport.ScrollPercent()*100)))
	if r.tmpFile != "" {
		divider += Styles.DimText.Render(fmt.Sprintf("[ %s ]", r.tmpFile))
	}
	sb.WriteString(divider)
	sb.WriteString("\n")

	// Textarea.
	sb.WriteString(r.textarea.View())
	sb.WriteString("\n")

	// Hints.
	sb.WriteString(Styles.Muted.Render("ctrl+s submit  esc cancel  pgup/pgdn scroll"))

	return sb.String()
}

// splitPromptAndPlan separates the gate label from the appended plan content.
// The human handler joins them with "\n\n---\n".
func splitPromptAndPlan(prompt string) (label, plan string) {
	if idx := strings.Index(prompt, "\n\n---\n"); idx >= 0 {
		return prompt[:idx], prompt[idx+6:]
	}
	return "", prompt
}

// renderMarkdownForReview renders markdown via glamour for the review viewport.
// Falls back to raw markdown with a notice if glamour fails.
func renderMarkdownForReview(md string, width int) string {
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

// writeTempPlan writes the plan markdown to a temp file and returns the path.
func writeTempPlan(plan string) string {
	f, err := os.CreateTemp("", "tracker-plan-*.md")
	if err != nil {
		return ""
	}
	defer f.Close()
	f.WriteString(plan)
	return f.Name()
}
