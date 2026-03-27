// ABOUTME: HistoryTrail component — reverse-chronological list of node visits.
// ABOUTME: Shows the execution path through the pipeline as a compact trail.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// HistoryTrail renders a compact reverse-chronological trail of node visits.
type HistoryTrail struct {
	store  *StateStore
	height int
	width  int
}

// NewHistoryTrail creates a trail that reads from the given state store.
func NewHistoryTrail(store *StateStore) *HistoryTrail {
	return &HistoryTrail{store: store}
}

// SetSize updates the display dimensions.
func (h *HistoryTrail) SetSize(w, height int) {
	h.width = w
	h.height = height
}

// View renders the trail as a compact reverse-chronological list.
func (h *HistoryTrail) View() string {
	path := h.store.VisitPath()
	if len(path) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(Styles.ZoneLabel.Render("TRAIL"))
	sb.WriteString("\n")

	// Build trail entries in reverse order, deduplicating consecutive repeats
	type entry struct {
		nodeID string
		count  int
	}
	var trail []entry
	for i := len(path) - 1; i >= 0; i-- {
		id := path[i]
		if len(trail) > 0 && trail[len(trail)-1].nodeID == id {
			trail[len(trail)-1].count++
			continue
		}
		trail = append(trail, entry{nodeID: id, count: 1})
	}

	// Render entries, newest first, clipped to available height
	maxLines := h.height - 1 // account for header
	if maxLines < 1 {
		maxLines = 1
	}

	dimStyle := Styles.DimText
	countStyle := lipgloss.NewStyle().Foreground(ColorAmber)

	for i, e := range trail {
		if i >= maxLines {
			break
		}
		status := h.store.NodeStatus(e.nodeID)
		lamp, style := StatusLamp(status)

		label := h.resolveLabel(e.nodeID)
		maxLabel := h.width - 6
		if maxLabel > 0 && len(label) > maxLabel {
			label = label[:maxLabel-1] + "…"
		}

		line := dimStyle.Render("  ") + style.Render(lamp) + " " + label
		if e.count > 1 {
			line += " " + countStyle.Render(fmt.Sprintf("×%d", e.count))
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	return sb.String()
}

// resolveLabel finds the display label for a node ID.
func (h *HistoryTrail) resolveLabel(nodeID string) string {
	for _, n := range h.store.Nodes() {
		if n.ID == nodeID {
			if n.Label != "" {
				return n.Label
			}
			return n.ID
		}
	}
	return nodeID
}

// Elapsed returns the time since the first node visit.
func (h *HistoryTrail) Elapsed() time.Duration {
	return 0
}
