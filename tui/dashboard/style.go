// ABOUTME: Shared style constants for the TUI dashboard — "Signal Cabin" aesthetic.
// ABOUTME: Train control panel inspired palette with instrument cluster borders and signal lamp indicators.
package dashboard

import "github.com/charmbracelet/lipgloss"

// ─── "Platform Edge" color palette ───────────────────────────────────────────
// Derived from train driving control panels: dark instrument background,
// colored signal lamps, high-contrast readouts.

var (
	colorPanel      = lipgloss.Color("234") // #1c1c1c — instrument panel dark
	colorBrightText = lipgloss.Color("255") // white — high-visibility primary
	colorAmber      = lipgloss.Color("214") // #ffaf00 — amber signal lamp (running)
	colorGreen      = lipgloss.Color("34")  // #00af00 — green clear signal (done)
	colorRed        = lipgloss.Color("196") // #ff0000 — red alarm (failed)
	colorBezel      = lipgloss.Color("24")  // #005f87 — instrument bezel blue
	colorReadout    = lipgloss.Color("117") // #87d7ff — digital readout blue
	colorLabel      = lipgloss.Color("244") // #808080 — panel label grey
	colorOff        = lipgloss.Color("239") // #4e4e4e — off indicator (pending)
	colorDim        = lipgloss.Color("240") // dim grey for secondary text
)

// ─── Signal lamp indicators ──────────────────────────────────────────────────
// These replace checkmarks and emoji with control panel style indicator lights.

const (
	lampOn      = "●" // lit signal lamp
	lampActive  = "◉" // active/pulsing signal
	lampOff     = "○" // off/pending signal
	lampError   = "✖" // fault indicator
	connectorH  = "━" // horizontal track connector
	connectorV  = "│" // vertical connector
	connectorDn = "┃" // heavy vertical for emphasis
)

// ─── Shared styles ───────────────────────────────────────────────────────────

var (
	// Zone labels — ALL CAPS engraved panel text
	zoneLabelStyle = lipgloss.NewStyle().
			Foreground(colorLabel).
			Bold(true)

	// Digital readout numbers
	readoutStyle = lipgloss.NewStyle().
			Foreground(colorReadout)

	// High-visibility primary text
	primaryTextStyle = lipgloss.NewStyle().
				Foreground(colorBrightText)

	// Dim secondary text
	dimTextStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	// Panel border style — bezel blue double-line
	panelBorder = lipgloss.DoubleBorder()
)
