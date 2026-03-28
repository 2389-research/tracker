// ABOUTME: Help overlay — modal showing all keyboard shortcuts in a styled table.
// ABOUTME: Implements ModalContent so it integrates with the existing modal system.
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// shortcut describes a single keyboard shortcut for the help overlay.
type shortcut struct {
	key  string
	desc string
}

var shortcuts = []shortcut{
	{"v", "Cycle log verbosity (all/tools/errors/reasoning)"},
	{"z", "Toggle zen mode (hide sidebar)"},
	{"/", "Search agent log"},
	{"n / N", "Next / previous search match"},
	{"Enter", "Drill down into selected node"},
	{"Esc", "Exit drill-down / search"},
	{"Up/Down", "Navigate node list"},
	{"y", "Copy visible log to clipboard"},
	{"?", "Toggle this help overlay"},
	{"Ctrl+O", "Expand/collapse tool output"},
	{"q", "Quit"},
	{"Ctrl+C", "Force quit (cancels gates)"},
}

// HelpContent implements ModalContent to display the shortcut help overlay.
type HelpContent struct{}

// NewHelpContent creates the help overlay content.
func NewHelpContent() *HelpContent {
	return &HelpContent{}
}

// Update handles key events — Esc or ? dismisses the help.
func (h *HelpContent) Update(msg tea.Msg) tea.Cmd {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch {
		case km.Type == tea.KeyEscape:
			return func() tea.Msg { return MsgModalDismiss{} }
		case km.Type == tea.KeyRunes && string(km.Runes) == "?":
			return func() tea.Msg { return MsgModalDismiss{} }
		}
	}
	return nil
}

// View renders the two-column shortcut table.
func (h *HelpContent) View() string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorReadout)
	keyStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorAmber).Width(12)
	descStyle := lipgloss.NewStyle().Foreground(ColorBrightText)
	hintStyle := lipgloss.NewStyle().Faint(true)

	var sb strings.Builder
	sb.WriteString(titleStyle.Render("KEYBOARD SHORTCUTS"))
	sb.WriteString("\n\n")

	for _, s := range shortcuts {
		sb.WriteString(keyStyle.Render(s.key))
		sb.WriteString(descStyle.Render(s.desc))
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(hintStyle.Render("press ? or esc to close"))
	return sb.String()
}

// Cancel is a no-op (help has no reply channels).
func (h *HelpContent) Cancel() {}
