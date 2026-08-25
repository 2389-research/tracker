// ABOUTME: Viewport geometry for the activity log — which slice of lines is rendered.
// ABOUTME: The selected search match owns the window; with no match the log tail-follows.
package tui

// resolveViewWindow rebuilds the filter cache, updates search matches, and
// resolves the half-open window [start, end) of filtered lines to render.
//
// The selected search match is the single authority for that window: whenever a
// term is set and matches something, the window is anchored on the current
// match so n/N actually move an off-screen hit into view. With no term (or no
// match) the log tail-follows, walking backward from the end. Returns the
// window bounds, the filtered slice, and the active search term.
func (al *AgentLog) resolveViewWindow(contentBudget, width int) (int, int, []int, string) {
	al.rebuildFilter()
	filtered := al.filteredCache
	totalFiltered := len(filtered)

	// Rebuild matches whenever a term is set, not only while the input is
	// focused: Confirm() (Enter) hides the bar but keeps the term, and that is
	// exactly the state n/N navigate in. Gating on Active() left the index empty
	// there and stale as the log grew.
	searchTerm := al.search.Term()
	if searchTerm != "" {
		al.search.UpdateMatchesFiltered(al.lines, filtered)
		if anchor := al.search.CurrentMatchLine(); anchor >= 0 {
			start, end := al.windowAround(filtered, anchor, contentBudget, width)
			return start, end, filtered, searchTerm
		}
	}

	usedRows := 0
	start := totalFiltered
	for start > 0 {
		idx := filtered[start-1]
		rows := termLines(al.lines[idx].text, width)
		if usedRows+rows > contentBudget {
			break
		}
		usedRows += rows
		start--
	}
	return start, totalFiltered, filtered, searchTerm
}

// windowAround returns the half-open window [start, end) of filtered positions
// that fits contentBudget terminal rows and contains anchor.
//
// The anchor is placed with context on both sides: growth runs downward first
// but no further than half the budget, then upward with what remains, then
// downward again to spend any budget left over when the anchor sits near the
// top. An anchor whose own wrapped height exceeds the budget still renders
// (clipped by the caller) rather than being skipped — showing a match partially
// beats reporting a hit the viewport never displays.
func (al *AgentLog) windowAround(filtered []int, anchor, contentBudget, width int) (int, int) {
	total := len(filtered)
	if total == 0 {
		return 0, 0
	}
	if anchor < 0 {
		anchor = 0
	}
	if anchor >= total {
		anchor = total - 1
	}

	used := termLines(al.lines[filtered[anchor]].text, width)
	start, end := anchor, anchor+1

	// Downward first, but spending at most half the budget below the anchor so
	// it keeps context above rather than pinning to the top of the viewport.
	end, used = al.growDown(filtered, end, used, min(contentBudget, used+contentBudget/2), width)
	start, used = al.growUp(filtered, start, used, contentBudget, width)
	end, _ = al.growDown(filtered, end, used, contentBudget, width)
	return start, end
}

// growDown extends the window forward from end while lines fit in budget.
// Returns the new end and the rows used.
func (al *AgentLog) growDown(filtered []int, end, used, budget, width int) (int, int) {
	for end < len(filtered) {
		rows := termLines(al.lines[filtered[end]].text, width)
		if used+rows > budget {
			break
		}
		used += rows
		end++
	}
	return end, used
}

// growUp extends the window backward from start while lines fit in budget.
// Returns the new start and the rows used.
func (al *AgentLog) growUp(filtered []int, start, used, budget, width int) (int, int) {
	for start > 0 {
		rows := termLines(al.lines[filtered[start-1]].text, width)
		if used+rows > budget {
			break
		}
		used += rows
		start--
	}
	return start, used
}
