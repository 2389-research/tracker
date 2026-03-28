// ABOUTME: ProgressTracker — renders an ASCII progress bar with ETA from rolling average.
// ABOUTME: Uses simple block characters styled to match the amber/dim palette.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// ProgressTracker renders a progress bar and estimates ETA from
// rolling average of completed node durations.
type ProgressTracker struct {
	store     *StateStore
	width     int
	startedAt time.Time
	durations []time.Duration // completed node durations for ETA
}

// NewProgressTracker creates a progress bar styled to the TUI palette.
func NewProgressTracker(store *StateStore) *ProgressTracker {
	return &ProgressTracker{
		store:     store,
		width:     30,
		startedAt: time.Now(),
	}
}

// RecordNodeDuration adds a completed node's duration for ETA calculation.
func (p *ProgressTracker) RecordNodeDuration(d time.Duration) {
	p.durations = append(p.durations, d)
}

// SetWidth adjusts the progress bar width.
func (p *ProgressTracker) SetWidth(w int) {
	if w < 10 {
		w = 10
	}
	p.width = w
}

// View renders the progress bar with fraction and ETA.
// Uses simple block characters: filled=━ (amber), empty=─ (dim).
func (p *ProgressTracker) View() string {
	done, total := p.store.Progress()
	if total == 0 {
		return ""
	}

	filled := p.width * done / total
	if filled > p.width {
		filled = p.width
	}
	empty := p.width - filled

	filledStyle := lipgloss.NewStyle().Foreground(ColorAmber)
	emptyStyle := lipgloss.NewStyle().Foreground(ColorOff)
	bar := filledStyle.Render(strings.Repeat("━", filled)) +
		emptyStyle.Render(strings.Repeat("─", empty))

	summary := fmt.Sprintf(" %d/%d", done, total)
	eta := p.estimateETA(done, total)
	if eta != "" {
		summary += " " + lipgloss.NewStyle().Foreground(ColorLabel).Render(eta)
	}

	return bar + summary
}

// estimateETA returns an ETA string based on rolling average, or empty if unknown.
func (p *ProgressTracker) estimateETA(done, total int) string {
	remaining := total - done
	if remaining <= 0 || done == 0 {
		return ""
	}

	// Need at least 2 recorded durations for a meaningful estimate.
	// Early completions are usually trivial setup nodes (0ms) that would
	// produce wildly inaccurate ETAs.
	durations := p.durations
	if len(durations) < 2 {
		return ""
	}

	// Use at most last 5 for the rolling average.
	window := durations
	if len(window) > 5 {
		window = window[len(window)-5:]
	}
	var sum time.Duration
	for _, d := range window {
		sum += d
	}
	avg := sum / time.Duration(len(window))

	// Don't show ETA if average is trivially short (< 1s) — only
	// setup/tool nodes have completed, not real LLM work.
	if avg < time.Second {
		return ""
	}

	est := avg * time.Duration(remaining)
	return "~" + formatETA(est)
}

// formatETA formats a duration as a human-readable ETA string.
func formatETA(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds left", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if s == 0 {
		return fmt.Sprintf("%dm left", m)
	}
	return fmt.Sprintf("%dm%ds left", m, s)
}
