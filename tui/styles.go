// ABOUTME: Style registry for the TUI — "Signal Cabin" aesthetic with train control panel colors.
// ABOUTME: Lamp indicators, thinking animation frames, and lipgloss styles for all components.
package tui

import "github.com/charmbracelet/lipgloss"

// Signal lamp indicators — control panel style status lights.
const (
	LampRunning = "◉" // active/pulsing signal
	LampDone    = "●" // lit signal lamp
	LampPending = "○" // off/pending signal
	LampFailed  = "✖" // fault indicator
)

// ThinkingFrames are the animation frames for the thinking spinner.
var ThinkingFrames = [4]string{"◐", "◓", "◑", "◒"}

// "Platform Edge" color palette — derived from train driving control panels.
var (
	ColorPanel      = lipgloss.Color("234") // #1c1c1c — instrument panel dark
	ColorBrightText = lipgloss.Color("255") // white — high-visibility primary
	ColorAmber      = lipgloss.Color("214") // #ffaf00 — amber signal lamp (running)
	ColorGreen      = lipgloss.Color("34")  // #00af00 — green clear signal (done)
	ColorRed        = lipgloss.Color("196") // #ff0000 — red alarm (failed)
	ColorBezel      = lipgloss.Color("24")  // #005f87 — instrument bezel blue
	ColorReadout    = lipgloss.Color("117") // #87d7ff — digital readout blue
	ColorLabel      = lipgloss.Color("244") // #808080 — panel label grey
	ColorOff        = lipgloss.Color("239") // #4e4e4e — off indicator (pending)
	ColorDim        = lipgloss.Color("240") // dim grey for secondary text

	// Tool palette — distinct colors per tool category.
	ColorBash  = lipgloss.Color("178") // #d7af00 — gold terminal
	ColorFile  = lipgloss.Color("75")  // #5fafff — blue file ops
	ColorGrep  = lipgloss.Color("114") // #87d787 — green search
	ColorAgent = lipgloss.Color("213") // #ff87ff — magenta spawn
	ColorPatch = lipgloss.Color("180") // #d7af87 — tan patch/apply
)

// Color aliases for semantic node status colors.
var (
	ColorRunning = ColorAmber
	ColorDone    = ColorGreen
	ColorFailed  = ColorRed
	ColorPending = ColorOff
)

// StyleRegistry holds all shared lipgloss styles for the TUI.
type StyleRegistry struct {
	NodeName    lipgloss.Style
	DimText     lipgloss.Style
	PrimaryText lipgloss.Style
	ZoneLabel   lipgloss.Style
	Readout     lipgloss.Style
	PanelBorder lipgloss.Border
	Header      lipgloss.Style
	Muted       lipgloss.Style
	StatusBar   lipgloss.Style
	ToolName    lipgloss.Style
	Error       lipgloss.Style
	Thinking    lipgloss.Style
}

// StatusLamp returns the indicator character and style for a node status.
func StatusLamp(status NodeState) (string, lipgloss.Style) {
	switch status {
	case NodeDone:
		return LampDone, lipgloss.NewStyle().Foreground(ColorDone)
	case NodeRunning:
		return LampRunning, lipgloss.NewStyle().Foreground(ColorRunning).Bold(true)
	case NodeFailed:
		return LampFailed, lipgloss.NewStyle().Foreground(ColorFailed)
	default:
		return LampPending, lipgloss.NewStyle().Foreground(ColorPending)
	}
}

// Styles is the global style registry instance.
var Styles = StyleRegistry{
	NodeName:    lipgloss.NewStyle().Foreground(ColorBrightText).Bold(true),
	DimText:     lipgloss.NewStyle().Foreground(ColorDim),
	PrimaryText: lipgloss.NewStyle().Foreground(ColorBrightText),
	ZoneLabel:   lipgloss.NewStyle().Foreground(ColorLabel).Bold(true),
	Readout:     lipgloss.NewStyle().Foreground(ColorReadout),
	PanelBorder: lipgloss.DoubleBorder(),
	Header:      lipgloss.NewStyle().Foreground(ColorBrightText).Bold(true),
	Muted:       lipgloss.NewStyle().Foreground(ColorDim),
	StatusBar:   lipgloss.NewStyle().Background(ColorPanel).Padding(0, 1),
	ToolName:    lipgloss.NewStyle().Foreground(ColorAmber).Bold(true),
	Error:       lipgloss.NewStyle().Foreground(ColorRed).Bold(true),
	Thinking:    lipgloss.NewStyle().Foreground(ColorAmber),
}
