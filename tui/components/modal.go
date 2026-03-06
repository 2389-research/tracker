// ABOUTME: Lipgloss-bordered modal overlay for human gate prompts in TUI mode.
// ABOUTME: Centers a styled box over the background terminal content.
package components

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	modalBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.DoubleBorder()).
				BorderForeground(lipgloss.Color("12")).
				Padding(1, 2)
	modalTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14")).MarginBottom(1)
)

// ModalModel renders a lipgloss-bordered modal overlay centered in the terminal.
type ModalModel struct {
	title        string
	termWidth    int
	termHeight   int
	innerContent string
}

// NewModal creates a modal with the given title and terminal dimensions.
func NewModal(title string, termWidth, termHeight int) ModalModel {
	return ModalModel{
		title:      title,
		termWidth:  termWidth,
		termHeight: termHeight,
	}
}

// SetContent updates the inner content rendered inside the modal border.
func (m *ModalModel) SetContent(content string) {
	m.innerContent = content
}

// SetSize updates the terminal dimensions used for centering.
func (m *ModalModel) SetSize(width, height int) {
	m.termWidth = width
	m.termHeight = height
}

// View renders the modal overlaid on background content.
// background is rendered behind the modal if non-empty (best-effort overlay).
func (m ModalModel) View(background string) string {
	inner := m.innerContent
	if inner == "" {
		inner = "(empty)"
	}

	body := modalTitleStyle.Render(m.title) + "\n" + inner
	box := modalBorderStyle.Render(body)

	if m.termWidth <= 0 && m.termHeight <= 0 {
		// No dimensions — just return the box
		return box
	}

	return centerOverBackground(box, background, m.termWidth, m.termHeight)
}

// centerOverBackground places content centered in the terminal over background.
func centerOverBackground(overlay, background string, width, height int) string {
	overlayLines := strings.Split(overlay, "\n")
	overlayH := len(overlayLines)

	// Find the widest line in overlay
	overlayW := 0
	for _, line := range overlayLines {
		if lw := lipgloss.Width(line); lw > overlayW {
			overlayW = lw
		}
	}

	// Build background as a grid of lines
	bgLines := strings.Split(background, "\n")
	// Pad/trim to height
	for len(bgLines) < height {
		bgLines = append(bgLines, "")
	}
	bgLines = bgLines[:height]

	topPad := (height - overlayH) / 2
	if topPad < 0 {
		topPad = 0
	}
	leftPad := (width - overlayW) / 2
	if leftPad < 0 {
		leftPad = 0
	}
	leftPadStr := strings.Repeat(" ", leftPad)

	// Build result
	var sb strings.Builder
	for i := range bgLines {
		if i > 0 {
			sb.WriteString("\n")
		}
		oi := i - topPad
		if oi >= 0 && oi < overlayH {
			sb.WriteString(leftPadStr)
			sb.WriteString(overlayLines[oi])
		} else {
			sb.WriteString(bgLines[i])
		}
	}
	return sb.String()
}
