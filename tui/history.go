// ABOUTME: HistoryTrail bubbletea component — reverse-chronological node visit log.
// ABOUTME: Shows the execution path through the pipeline as a compact scrollable trail.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// trailEntry is a deduplicated visit in the trail.
type trailEntry struct {
	nodeID string
	label  string
	count  int
}

// HistoryTrail is a bubbletea model that renders a compact reverse-chronological
// trail of node visits. Consecutive visits to the same node are deduplicated
// with a count (e.g., "TestMilestone ×3").
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

// Init implements tea.Model.
func (h HistoryTrail) Init() tea.Cmd { return nil }

// Update implements tea.Model. The trail is read-only — state comes from the store.
func (h HistoryTrail) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return h, nil
}

// View implements tea.Model.
func (h HistoryTrail) View() string {
	path := h.store.VisitPath()
	if len(path) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(Styles.ZoneLabel.Render("TRAIL"))
	sb.WriteString("\n")

	trail := h.buildTrail(path)
	maxLines := h.height - 1
	if maxLines < 1 {
		maxLines = 1
	}

	countStyle := lipgloss.NewStyle().Foreground(ColorAmber)
	for i, e := range trail {
		if i >= maxLines {
			break
		}
		sb.WriteString(h.renderTrailEntry(e, countStyle))
		sb.WriteString("\n")
	}

	return sb.String()
}

// renderTrailEntry renders a single trail entry line.
func (h HistoryTrail) renderTrailEntry(e trailEntry, countStyle lipgloss.Style) string {
	status := h.store.NodeStatus(e.nodeID)
	lamp, style := StatusLamp(status)

	label := e.label
	maxLabel := h.width - 6
	if maxLabel > 0 && len(label) > maxLabel {
		label = label[:maxLabel-1] + "…"
	}

	line := "  " + style.Render(lamp) + " " + label
	if e.count > 1 {
		line += " " + countStyle.Render(fmt.Sprintf("×%d", e.count))
	}
	return line
}

// buildTrail creates deduplicated entries in reverse chronological order.
func (h *HistoryTrail) buildTrail(path []string) []trailEntry {
	var trail []trailEntry
	for i := len(path) - 1; i >= 0; i-- {
		id := path[i]
		if len(trail) > 0 && trail[len(trail)-1].nodeID == id {
			trail[len(trail)-1].count++
			continue
		}
		trail = append(trail, trailEntry{
			nodeID: id,
			label:  h.resolveLabel(id),
			count:  1,
		})
	}
	return trail
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
