// ABOUTME: Reusable scroll container with auto-scroll and manual override.
// ABOUTME: Tracks visible range within a list of lines, re-enabling auto-scroll on ScrollToBottom.
package tui

// ScrollView is a line-buffered viewport that auto-scrolls to the bottom
// unless the user manually scrolls up.
type ScrollView struct {
	lines      []string
	height     int
	offset     int
	autoScroll bool
}

// NewScrollView creates a ScrollView with the given viewport height.
func NewScrollView(height int) *ScrollView {
	return &ScrollView{
		height:     height,
		autoScroll: true,
	}
}

// Append adds a line and auto-scrolls if enabled.
func (sv *ScrollView) Append(line string) {
	sv.lines = append(sv.lines, line)
	if sv.autoScroll {
		sv.scrollToEnd()
	}
}

// UpdateLast replaces the last line in the buffer.
func (sv *ScrollView) UpdateLast(line string) {
	if len(sv.lines) > 0 {
		sv.lines[len(sv.lines)-1] = line
	}
}

// ScrollUp moves the viewport up by n lines and disables auto-scroll.
func (sv *ScrollView) ScrollUp(n int) {
	sv.autoScroll = false
	sv.offset -= n
	if sv.offset < 0 {
		sv.offset = 0
	}
}

// ScrollDown moves the viewport down by n lines.
func (sv *ScrollView) ScrollDown(n int) {
	sv.offset += n
	max := sv.maxOffset()
	if sv.offset > max {
		sv.offset = max
	}
}

// ScrollToBottom re-enables auto-scroll and jumps to the end.
func (sv *ScrollView) ScrollToBottom() {
	sv.autoScroll = true
	sv.scrollToEnd()
}

// SetHeight updates the viewport height.
func (sv *ScrollView) SetHeight(h int) {
	sv.height = h
	if sv.autoScroll {
		sv.scrollToEnd()
	}
}

// VisibleRange returns the start (inclusive) and end (exclusive) indices
// of currently visible lines.
func (sv *ScrollView) VisibleRange() (start, end int) {
	total := len(sv.lines)
	if total == 0 {
		return 0, 0
	}
	if total <= sv.height {
		return 0, total
	}
	start = sv.offset
	end = start + sv.height
	if end > total {
		end = total
	}
	return
}

// Lines returns the full line buffer.
func (sv *ScrollView) Lines() []string { return sv.lines }

// Len returns the total number of lines.
func (sv *ScrollView) Len() int { return len(sv.lines) }

// AutoScroll returns whether auto-scroll is enabled.
func (sv *ScrollView) AutoScroll() bool { return sv.autoScroll }

// scrollToEnd sets the offset so the last lines are visible.
func (sv *ScrollView) scrollToEnd() {
	sv.offset = sv.maxOffset()
}

// maxOffset returns the largest valid offset.
func (sv *ScrollView) maxOffset() int {
	max := len(sv.lines) - sv.height
	if max < 0 {
		return 0
	}
	return max
}
