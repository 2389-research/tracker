// ABOUTME: AgentLog component — append-only streaming log with per-node streams.
// ABOUTME: Each node gets its own line buffer. Parallel branches interleave with labeled separators.
package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const defaultMaxCollapsedLines = 4
const maxLogLines = 10000

// nodeStream holds per-node streaming state.
type nodeStream struct {
	current     strings.Builder // in-progress line (no newline yet)
	inCodeBlock bool
}

// LineKind tags log lines for verbosity filtering.
type LineKind int

const (
	LineGeneral   LineKind = iota // text output, headers, etc.
	LineTool                      // tool call start/end
	LineError                     // errors, failures
	LineReasoning                 // reasoning/thinking output
)

// styledLine is one rendered line in the log, tagged with its source node.
type styledLine struct {
	nodeID string
	text   string
	kind   LineKind
}

// Verbosity levels for log filtering.
type Verbosity int

const (
	VerbosityAll Verbosity = iota
	VerbosityTools
	VerbosityErrors
	VerbosityReasoning
)

// VerbosityLabel returns the display name for a verbosity level.
func (v Verbosity) Label() string {
	switch v {
	case VerbosityAll:
		return "all"
	case VerbosityTools:
		return "tools"
	case VerbosityErrors:
		return "errors"
	case VerbosityReasoning:
		return "reasoning"
	default:
		return "all"
	}
}

// AgentLog renders a streaming activity log. Each pipeline node gets its own
// line accumulation buffer. Lines from concurrent nodes interleave in the
// unified log with separators when the source node changes. Lines are styled
// once on newline and never re-rendered.
type AgentLog struct {
	store        *StateStore
	thinking     *ThinkingTracker
	scroll       *ScrollView
	height       int
	width        int
	expanded     bool
	verboseTrace bool

	// Per-node streaming state.
	streams map[string]*nodeStream

	// Unified styled line buffer (append-only). Each line is tagged with
	// its source node so we can insert separators when the source changes.
	lines    []styledLine
	lastNode string // node ID of the last line appended (for separator logic)

	// Verbosity filter (view-level only — lines always stored).
	verbosity     Verbosity
	filteredCache []int // cached indices into lines matching current filter
	filterDirty   bool  // true when filter cache needs rebuild

	// Node focus for drill-down.
	focusNodeID string

	// Search state.
	search *SearchBar
}

// NewAgentLog creates an AgentLog with the given state, thinking tracker, and viewport height.
func NewAgentLog(store *StateStore, thinking *ThinkingTracker, height int) *AgentLog {
	return &AgentLog{
		store:       store,
		thinking:    thinking,
		scroll:      NewScrollView(height),
		height:      height,
		streams:     make(map[string]*nodeStream),
		search:      NewSearchBar(),
		filterDirty: true,
	}
}

// Search returns the search bar for external access (key routing).
func (al *AgentLog) Search() *SearchBar { return al.search }

// Verbosity returns the current verbosity level.
func (al *AgentLog) Verbosity() Verbosity { return al.verbosity }

// CycleVerbosity advances to the next verbosity level.
func (al *AgentLog) CycleVerbosity() {
	al.verbosity = (al.verbosity + 1) % 4
	al.filterDirty = true
}

// SetFocusNodeID sets the node ID to filter by (empty = show all).
func (al *AgentLog) SetFocusNodeID(nodeID string) {
	al.focusNodeID = nodeID
	al.filterDirty = true
}

// SetSize updates both width and height for the agent log viewport.
func (al *AgentLog) SetSize(w, h int) {
	al.width = w
	al.height = h
	al.scroll.SetHeight(h)
}

// SetFocusedNode is a no-op kept for interface compatibility.
// The activity log no longer tracks a single focused node —
// it shows all active nodes with separators.
func (al *AgentLog) SetFocusedNode(nodeID string) {}

// SetVerboseTrace enables or disables verbose trace output.
func (al *AgentLog) SetVerboseTrace(v bool) {
	al.verboseTrace = v
}

// stream returns the per-node stream, creating it if needed.
func (al *AgentLog) stream(nodeID string) *nodeStream {
	s, ok := al.streams[nodeID]
	if !ok {
		s = &nodeStream{}
		al.streams[nodeID] = s
	}
	return s
}

// Init implements tea.Model.
func (al *AgentLog) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (al *AgentLog) Update(msg tea.Msg) tea.Cmd {
	if al.applyStreamMsg(msg) {
		return nil
	}
	al.applyControlMsg(msg)
	return nil
}

// applyStreamMsg handles LLM streaming messages. Returns true if the message was consumed.
func (al *AgentLog) applyStreamMsg(msg tea.Msg) bool {
	switch m := msg.(type) {
	case MsgTextChunk:
		al.appendText(m.NodeID, m.Text)
	case MsgReasoningChunk:
		al.appendReasoning(m.NodeID, m.Text)
	case MsgToolCallStart:
		al.flushNode(m.NodeID)
		al.addTaggedLine(m.NodeID, toolStyle(m.ToolName).Render(formatToolDisplay(m.ToolName, m.ToolInput)), LineTool)
	case MsgToolCallEnd:
		al.flushNode(m.NodeID)
		al.appendToolEnd(m)
	case MsgAgentError:
		al.flushNode(m.NodeID)
		al.addTaggedLine(m.NodeID, Styles.Error.Render("ERROR: "+m.Error), LineError)
	case MsgLLMProviderRaw:
		al.handleProviderRaw(m)
	case MsgNodeFailed:
		al.flushNode(m.NodeID)
		al.addTaggedLine(m.NodeID, Styles.Error.Render("FAILED: "+m.Error), LineError)
		delete(al.streams, m.NodeID)
	case MsgNodeRetrying:
		al.flushNode(m.NodeID)
		al.addTaggedLine(m.NodeID, Styles.Warn.Render("RETRYING: "+m.Message), LineError)
	case MsgNodeCompleted:
		al.flushNode(m.NodeID)
		delete(al.streams, m.NodeID)
	default:
		return false
	}
	return true
}

// applyControlMsg handles UI control messages (verbosity, focus, expand).
func (al *AgentLog) applyControlMsg(msg tea.Msg) {
	switch m := msg.(type) {
	case MsgToggleExpand:
		al.expanded = !al.expanded
	case MsgCycleVerbosity:
		al.CycleVerbosity()
	case MsgFocusNode:
		al.SetFocusNodeID(m.NodeID)
	case MsgClearFocus:
		al.SetFocusNodeID("")
	}
}

// handleProviderRaw logs raw provider events when verbose trace is enabled.
func (al *AgentLog) handleProviderRaw(m MsgLLMProviderRaw) {
	if al.verboseTrace {
		al.flushNode(m.NodeID)
		al.addLine(m.NodeID, Styles.DimText.Render(m.Data))
	}
}

// appendText processes streaming LLM text for a specific node.
// Complete lines (ending with \n) get styled and appended to the unified log.
// The partial trailing line stays in the node's stream buffer.
func (al *AgentLog) appendText(nodeID, text string) {
	s := al.stream(nodeID)
	for _, ch := range text {
		if ch == '\n' {
			line := s.current.String()
			s.current.Reset()
			al.addLine(nodeID, al.styleLine(s, line))
		} else {
			s.current.WriteRune(ch)
		}
	}
}

// appendReasoning adds reasoning text as muted lines.
func (al *AgentLog) appendReasoning(nodeID, text string) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			al.addTaggedLine(nodeID, Styles.Muted.Render(line), LineReasoning)
		}
	}
}

// appendToolEnd adds collapsed or expanded tool output.
func (al *AgentLog) appendToolEnd(m MsgToolCallEnd) {
	if m.Error != "" {
		al.addTaggedLine(m.NodeID, Styles.Error.Render("  ✗ "+m.ToolName+": "+m.Error), LineError)
		return
	}
	if m.Output == "" {
		al.addTaggedLine(m.NodeID, Styles.Muted.Render("  ✓ "+m.ToolName), LineTool)
		return
	}
	lines := strings.Split(m.Output, "\n")
	if !al.expanded && len(lines) > defaultMaxCollapsedLines {
		al.addTaggedLine(m.NodeID,
			Styles.Muted.Render(fmt.Sprintf("  ✓ %s (%d lines, ctrl+o to expand)", m.ToolName, len(lines))), LineTool)
		return
	}
	for _, line := range lines {
		al.addTaggedLine(m.NodeID, Styles.DimText.Render(line), LineTool)
	}
}

// addLine appends a styled line to the unified log with LineGeneral kind.
// Inserts a node separator when the source node changes.
// Trims oldest entries when the log exceeds maxLogLines.
func (al *AgentLog) addLine(nodeID, text string) {
	al.addTaggedLine(nodeID, text, LineGeneral)
}

// addTaggedLine appends a styled line with a specific LineKind tag.
func (al *AgentLog) addTaggedLine(nodeID, text string, kind LineKind) {
	if nodeID != "" && nodeID != al.lastNode && al.lastNode != "" {
		al.lines = append(al.lines, styledLine{
			nodeID: "",
			text:   Styles.Muted.Render(fmt.Sprintf("─── %s ", nodeID)),
		})
	}
	al.lastNode = nodeID
	al.lines = append(al.lines, styledLine{nodeID: nodeID, text: text, kind: kind})
	al.filterDirty = true

	// Cap the line buffer to prevent unbounded memory growth.
	if len(al.lines) > maxLogLines {
		trim := len(al.lines) - maxLogLines
		al.lines = al.lines[trim:]
	}
}

// flushNode finalizes any in-progress line for a specific node.
func (al *AgentLog) flushNode(nodeID string) {
	s, ok := al.streams[nodeID]
	if !ok || s.current.Len() == 0 {
		return
	}
	al.addLine(nodeID, al.styleLine(s, s.current.String()))
	s.current.Reset()
	// Reset code block state on flush — an unclosed fence from a crashed
	// or interrupted node should not permanently corrupt styling.
	s.inCodeBlock = false
}

// styleLine applies lightweight line-level formatting.
func (al *AgentLog) styleLine(s *nodeStream, line string) string {
	trimmed := strings.TrimSpace(line)

	// Code fence toggle.
	if strings.HasPrefix(trimmed, "```") {
		s.inCodeBlock = !s.inCodeBlock
		return Styles.Muted.Render(line)
	}

	// Inside code block.
	if s.inCodeBlock {
		return Styles.DimText.Render(line)
	}

	return styleMarkdownLine(trimmed, line)
}

// styleMarkdownLine applies markdown-aware styling for a non-code-block line.
func styleMarkdownLine(trimmed, line string) string {
	// Headers.
	if strings.HasPrefix(trimmed, "# ") {
		return lipgloss.NewStyle().Bold(true).Foreground(ColorReadout).Render(trimmed)
	}
	if strings.HasPrefix(trimmed, "## ") {
		return lipgloss.NewStyle().Bold(true).Foreground(ColorLabel).Render(trimmed)
	}
	if strings.HasPrefix(trimmed, "### ") {
		return lipgloss.NewStyle().Bold(true).Render(trimmed)
	}

	// Bullets and numbered lists.
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
		return Styles.PrimaryText.Render(line)
	}
	if isNumberedListItem(trimmed) {
		return Styles.PrimaryText.Render(line)
	}

	if trimmed == "" {
		return ""
	}

	return Styles.PrimaryText.Render(line)
}

// isNumberedListItem returns true if the line looks like "1. " or "12. ".
func isNumberedListItem(trimmed string) bool {
	if len(trimmed) < 3 {
		return false
	}
	if trimmed[0] < '0' || trimmed[0] > '9' {
		return false
	}
	return trimmed[1] == '.' || (len(trimmed) > 2 && trimmed[2] == '.')
}

// termLines counts how many terminal rows a styled string occupies.
func termLines(s string, width int) int {
	if width <= 0 {
		width = 80
	}
	n := 0
	for _, line := range strings.Split(s, "\n") {
		w := lipgloss.Width(line)
		if w == 0 {
			n++
		} else {
			n += (w-1)/width + 1
		}
	}
	return n
}

// activeNodeIndicators builds a multi-line indicator showing all currently
// active nodes (thinking, running tools, waiting for provider).
func (al *AgentLog) activeNodeIndicators() string {
	var indicators []string

	// Collect all nodes that are currently running.
	for _, entry := range al.store.Nodes() {
		if al.store.NodeStatus(entry.ID) != NodeRunning {
			continue
		}
		nodeLabel := entry.Label
		if nodeLabel == "" {
			nodeLabel = entry.ID
		}

		if toolName := al.thinking.ToolName(entry.ID); toolName != "" {
			elapsed := al.thinking.Elapsed(entry.ID).Seconds()
			indicators = append(indicators,
				toolStyle(toolName).Render(fmt.Sprintf("» %s: %s (%.1fs)", nodeLabel, toolName, elapsed)))
		} else if al.store.IsWaiting(entry.ID) {
			indicators = append(indicators,
				Styles.Muted.Render(fmt.Sprintf(":: %s: waiting for provider...", nodeLabel)))
		} else if al.thinking.IsThinking(entry.ID) {
			elapsed := al.thinking.Elapsed(entry.ID).Seconds()
			indicators = append(indicators,
				Styles.Thinking.Render(fmt.Sprintf("⟳ %s: thinking... (%.1fs)", nodeLabel, elapsed)))
		}
	}

	if len(indicators) == 0 {
		return " "
	}
	return strings.Join(indicators, "\n")
}

// rebuildFilter rebuilds the cached filtered indices based on verbosity and focus.
func (al *AgentLog) rebuildFilter() {
	if !al.filterDirty {
		return
	}
	al.filterDirty = false
	al.filteredCache = al.filteredCache[:0]

	for i, line := range al.lines {
		if al.linePassesFilter(line) {
			al.filteredCache = append(al.filteredCache, i)
		}
	}
}

// linePassesFilter returns true if the line should be included in the filtered view.
func (al *AgentLog) linePassesFilter(line styledLine) bool {
	// Always include separator lines (empty nodeID) — they provide
	// structural context between nodes regardless of filter state.
	isSeparator := line.nodeID == "" && line.kind == LineGeneral

	// Node focus filter.
	if !isSeparator && al.focusNodeID != "" && line.nodeID != "" && line.nodeID != al.focusNodeID {
		return false
	}
	// Verbosity filter (separators pass through).
	if !isSeparator && al.verbosity != VerbosityAll {
		return al.lineMatchesVerbosity(line)
	}
	return true
}

// lineMatchesVerbosity returns true if the line's kind matches the current verbosity level.
func (al *AgentLog) lineMatchesVerbosity(line styledLine) bool {
	switch al.verbosity {
	case VerbosityTools:
		return line.kind == LineTool
	case VerbosityErrors:
		return line.kind == LineError
	case VerbosityReasoning:
		return line.kind == LineReasoning
	}
	return true
}

// View renders the agent log viewport. The indicator is always rendered at the
// bottom — content fills upward from the remaining space. This guarantees the
// indicator is never pushed off-screen regardless of content size or wrapping.
func (al *AgentLog) View() string {
	width := al.width
	if width <= 0 {
		width = 80
	}

	// 1. Build the fixed bottom section: indicator + partials.
	indicator := al.activeNodeIndicators()
	partials, bottomRows := al.buildPartials(indicator, width)

	// Account for search bar at the bottom.
	searchBarRows := 0
	if al.search.Active() {
		searchBarRows = 1
	}

	// 2. Calculate how many rows are available for styled content.
	// height = header(1) + content + partials + indicator + searchbar
	contentBudget := al.height - 1 - bottomRows - searchBarRows
	if contentBudget < 1 {
		contentBudget = 1
	}

	// 3. Rebuild filter cache if needed, resolve visible window start.
	start, filtered, searchTerm := al.resolveViewWindow(contentBudget, width)

	// 4. Render: header, then content, then partials, then indicator, then search.
	var sb strings.Builder
	sb.WriteString(al.renderHeader())
	sb.WriteString(al.renderContent(filtered, start, searchTerm, partials, indicator))
	sb.WriteString(indicator + "\n")
	if al.search.Active() {
		sb.WriteString(al.search.View())
		sb.WriteString("\n")
	}
	return sb.String()
}

// buildPartials collects in-progress partial lines from active node streams and
// returns them along with the total bottom row count (partials + indicator).
func (al *AgentLog) buildPartials(indicator string, width int) ([]string, int) {
	bottomRows := termLines(indicator, width)
	var partials []string
	for nodeID, s := range al.streams {
		if s.current.Len() > 0 {
			prefix := ""
			if len(al.streams) > 1 {
				prefix = Styles.Muted.Render(nodeID+": ") + ""
			}
			line := prefix + Styles.PrimaryText.Render(s.current.String())
			partials = append(partials, line)
			bottomRows += termLines(line, width)
		}
	}
	return partials, bottomRows
}

// resolveViewWindow rebuilds the filter cache, updates search matches, and
// walks backward from the end to find the start index that fits contentBudget rows.
// Returns start index into filtered, the filtered slice, and the active search term.
func (al *AgentLog) resolveViewWindow(contentBudget, width int) (int, []int, string) {
	al.rebuildFilter()
	filtered := al.filteredCache
	totalFiltered := len(filtered)

	searchTerm := al.search.Term()
	if al.search.Active() {
		al.search.UpdateMatchesFiltered(al.lines, filtered)
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
	return start, filtered, searchTerm
}

// renderHeader returns the header label line for the activity log.
func (al *AgentLog) renderHeader() string {
	headerLabel := "ACTIVITY LOG"
	if al.focusNodeID != "" {
		headerLabel += " [" + al.focusNodeID + "]"
	}
	return Styles.ZoneLabel.Render(headerLabel) + "\n"
}

// renderContent renders the visible log lines, partials, and empty-state placeholder.
func (al *AgentLog) renderContent(filtered []int, start int, searchTerm string, partials []string, indicator string) string {
	totalFiltered := len(filtered)
	var sb strings.Builder
	if totalFiltered == 0 && len(partials) == 0 && indicator == " " {
		sb.WriteString(Styles.DimText.Render("awaiting activity..."))
		sb.WriteString("\n")
	} else {
		for i := start; i < totalFiltered; i++ {
			idx := filtered[i]
			text := al.lines[idx].text
			if searchTerm != "" {
				text = HighlightLine(text, searchTerm)
			}
			sb.WriteString(text)
			sb.WriteString("\n")
		}
	}
	for _, p := range partials {
		sb.WriteString(p)
		sb.WriteString("\n")
	}
	return sb.String()
}

// VisibleText returns the plain text of the visible log (for clipboard copy).
func (al *AgentLog) VisibleText() string {
	al.rebuildFilter()
	var sb strings.Builder
	for _, idx := range al.filteredCache {
		sb.WriteString(stripAnsi(al.lines[idx].text))
		sb.WriteString("\n")
	}
	return sb.String()
}

// thinkingTickCmd returns a command that sends a MsgThinkingTick after 150ms.
func thinkingTickCmd() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg {
		return MsgThinkingTick{}
	})
}

// headerTickCmd returns a command that sends a MsgHeaderTick after 1s.
func headerTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return MsgHeaderTick{}
	})
}

// toolStyle returns a lipgloss style colored by tool category.
func toolStyle(toolName string) lipgloss.Style {
	switch toolName {
	case "bash":
		return lipgloss.NewStyle().Foreground(ColorBash).Bold(true)
	case "read", "write":
		return lipgloss.NewStyle().Foreground(ColorFile).Bold(true)
	case "edit", "apply_patch":
		return lipgloss.NewStyle().Foreground(ColorPatch).Bold(true)
	case "grep", "glob":
		return lipgloss.NewStyle().Foreground(ColorGrep).Bold(true)
	case "spawn_agent":
		return lipgloss.NewStyle().Foreground(ColorAgent).Bold(true)
	default:
		return Styles.ToolName
	}
}

const toolDisplayLimit = 80

// formatToolDisplay renders a tool invocation with context extracted from the input JSON.
func formatToolDisplay(toolName, toolInput string) string {
	input := parseToolInputJSON(toolInput)

	if s := formatKnownTool(toolName, input); s != "" {
		return s
	}

	for _, key := range []string{"path", "command", "pattern", "task", "query", "name", "url"} {
		if v := input[key]; v != "" {
			return "▸ " + toolName + " " + truncateToolText(v, toolDisplayLimit)
		}
	}
	return "▸ " + toolName
}

// formatKnownTool returns a formatted display string for known tool types,
// or empty string if the tool is not recognized or has no relevant input.
func formatKnownTool(toolName string, input map[string]string) string {
	switch toolName {
	case "bash":
		return formatToolPath("▸ $ ", input["command"], true)
	case "read":
		return formatToolPath("▸ read ", input["path"], false)
	case "write":
		return formatToolPath("▸ write ", input["path"], false)
	case "edit", "apply_patch":
		return formatToolPath("▸ edit ", input["path"], false)
	case "grep":
		return formatGrepTool(input)
	case "glob":
		return formatToolPath("▸ glob ", input["pattern"], false)
	case "spawn_agent":
		return formatToolPath("▸ spawn: ", input["task"], true)
	}
	return ""
}

// formatToolPath formats a tool display with an optional truncation.
func formatToolPath(prefix, value string, truncate bool) string {
	if value == "" {
		return ""
	}
	if truncate {
		return prefix + truncateToolText(value, toolDisplayLimit)
	}
	return prefix + value
}

// formatGrepTool formats the grep tool display with pattern and optional path.
func formatGrepTool(input map[string]string) string {
	pattern := input["pattern"]
	if pattern == "" {
		return ""
	}
	s := "▸ grep " + pattern
	if p := input["path"]; p != "" {
		s += " " + p
	}
	return s
}

// parseToolInputJSON extracts string values from tool input JSON.
func parseToolInputJSON(raw string) map[string]string {
	result := make(map[string]string)
	if raw == "" {
		return result
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return result
	}
	for key, val := range parsed {
		var s string
		if err := json.Unmarshal(val, &s); err == nil {
			result[key] = s
		}
	}
	return result
}

// truncateToolText trims and truncates text for display, collapsing newlines.
func truncateToolText(text string, limit int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if len(text) <= limit {
		return text
	}
	return text[:limit-1] + "…"
}
