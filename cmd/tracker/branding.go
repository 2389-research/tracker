// ABOUTME: 2389.ai brand assets — ASCII art header, logo, taglines, and shared color palette.
// ABOUTME: Used by the setup wizard, pipeline startup banner, and exit screen.
package main

import (
	"fmt"
	"math/rand/v2"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ── Brand colors ────────────────────────────────────────────────────────────

var (
	colorNeon  = lipgloss.Color("#00FFAA")
	colorHot   = lipgloss.Color("#FF006E")
	colorElec  = lipgloss.Color("#7B61FF")
	colorSky   = lipgloss.Color("#00D4FF")
	colorWarm  = lipgloss.Color("#FFB800")
	colorMuted = lipgloss.Color("#666666")
)

// ── Brand styles ────────────────────────────────────────────────────────────

var (
	bannerStyle = lipgloss.NewStyle().
			Foreground(colorNeon).
			Bold(true)

	taglineStyle = lipgloss.NewStyle().
			Foreground(colorElec).
			Italic(true)

	mutedStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	selectedStyle = lipgloss.NewStyle().
			Foreground(colorHot).
			Bold(true)

	headerBox = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(colorNeon).
			Padding(0, 1).
			Align(lipgloss.Center)
)

// ── Renderers ───────────────────────────────────────────────────────────────

// renderHeader returns the compact TRACKER box with "by 2389.ai" subtitle.
func renderHeader() string {
	art := bannerStyle.Render("▀█▀ █▀█ █▀█ █▀▀ █▄▀ █▀▀ █▀█") + "\n" +
		bannerStyle.Render(" █  █▀▄ █▀█ █▄▄ █ █ ██▄ █▀▄")
	box := headerBox.Render(art)

	var b strings.Builder
	b.WriteString(box)
	b.WriteByte('\n')
	b.WriteString(mutedStyle.Render("              by ") + selectedStyle.Render("2389.ai"))
	b.WriteByte('\n')
	return b.String()
}

// renderStartupBanner returns the header plus a tagline, suitable for
// printing to stdout at the start of a pipeline run.
func renderStartupBanner() string {
	var b strings.Builder
	b.WriteByte('\n')
	b.WriteString(renderHeader())
	b.WriteString(taglineStyle.Render("  " + randomTagline()))
	b.WriteString("\n\n")
	return b.String()
}

// logo returns the large block-letter ASCII art for the finish screen.
func logo() string {
	return `
  ████████╗██████╗  █████╗  ██████╗██╗  ██╗███████╗██████╗
  ╚══██╔══╝██╔══██╗██╔══██╗██╔════╝██║ ██╔╝██╔════╝██╔══██╗
     ██║   ██████╔╝███████║██║     █████╔╝ █████╗  ██████╔╝
     ██║   ██╔══██╗██╔══██║██║     ██╔═██╗ ██╔══╝  ██╔══██╗
     ██║   ██║  ██║██║  ██║╚██████╗██║  ██╗███████╗██║  ██║
     ╚═╝   ╚═╝  ╚═╝╚═╝  ╚═╝ ╚═════╝╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝
                        by 2389.ai`
}

// ── Taglines ────────────────────────────────────────────────────────────────

func taglines() []string {
	return []string{
		"Pipelines that think for themselves.",
		"Your agents. Your rules. Your pipeline.",
		"Ship agentic workflows, not YAML.",
		"From DOT to done.",
		"Because your agents deserve better plumbing.",
		"Wire it up. Let it rip.",
		"Orchestration without the orchestration tax.",
		"The agentic pipeline engine.",
		"Graphs in. Intelligence out.",
		"Less glue code, more go time.",
	}
}

func randomTagline() string {
	pool := taglines()
	return pool[rand.IntN(len(pool))]
}

// printStartupBanner writes the branded header to stdout.
func printStartupBanner() {
	fmt.Print(renderStartupBanner())
}
