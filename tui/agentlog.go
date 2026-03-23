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

// nodeStream holds per-node streaming state.
type nodeStream struct {
	current     strings.Builder // in-progress line (no newline yet)
	inCodeBlock bool
}

// styledLine is one rendered line in the log, tagged with its source node.
type styledLine struct {
	nodeID string
	text   string
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
}

// NewAgentLog creates an AgentLog with the given state, thinking tracker, and viewport height.
func NewAgentLog(store *StateStore, thinking *ThinkingTracker, height int) *AgentLog {
	return &AgentLog{
		store:    store,
		thinking: thinking,
		scroll:   NewScrollView(height),
		height:   height,
		streams:  make(map[string]*nodeStream),
	}
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

// Update processes messages and updates the log.
func (al *AgentLog) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case MsgTextChunk:
		al.appendText(m.NodeID, m.Text)
	case MsgReasoningChunk:
		al.appendReasoning(m.NodeID, m.Text)
	case MsgToolCallStart:
		al.flushNode(m.NodeID)
		al.addLine(m.NodeID, toolStyle(m.ToolName).Render(formatToolDisplay(m.ToolName, m.ToolInput)))
	case MsgToolCallEnd:
		al.flushNode(m.NodeID)
		al.appendToolEnd(m)
	case MsgAgentError:
		al.flushNode(m.NodeID)
		al.addLine(m.NodeID, Styles.Error.Render("ERROR: "+m.Error))
	case MsgLLMProviderRaw:
		if al.verboseTrace {
			al.flushNode(m.NodeID)
			al.addLine(m.NodeID, Styles.DimText.Render(m.Data))
		}
	case MsgToggleExpand:
		al.expanded = !al.expanded
	}
	return nil
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
			al.addLine(nodeID, Styles.Muted.Render(line))
		}
	}
}

// appendToolEnd adds collapsed or expanded tool output.
func (al *AgentLog) appendToolEnd(m MsgToolCallEnd) {
	if m.Error != "" {
		al.addLine(m.NodeID, Styles.Error.Render("  ✗ "+m.ToolName+": "+m.Error))
		return
	}
	if m.Output == "" {
		al.addLine(m.NodeID, Styles.Muted.Render("  ✓ "+m.ToolName))
		return
	}
	lines := strings.Split(m.Output, "\n")
	if !al.expanded && len(lines) > defaultMaxCollapsedLines {
		al.addLine(m.NodeID,
			Styles.Muted.Render(fmt.Sprintf("  ✓ %s (%d lines, ctrl+o to expand)", m.ToolName, len(lines))))
		return
	}
	for _, line := range lines {
		al.addLine(m.NodeID, Styles.DimText.Render(line))
	}
}

// addLine appends a styled line to the unified log.
// Inserts a node separator when the source node changes.
func (al *AgentLog) addLine(nodeID, text string) {
	if nodeID != "" && nodeID != al.lastNode && al.lastNode != "" {
		al.lines = append(al.lines, styledLine{
			nodeID: "",
			text:   Styles.Muted.Render(fmt.Sprintf("─── %s ", nodeID)),
		})
	}
	al.lastNode = nodeID
	al.lines = append(al.lines, styledLine{nodeID: nodeID, text: text})
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
	if len(trimmed) > 2 && trimmed[0] >= '0' && trimmed[0] <= '9' && (trimmed[1] == '.' || (len(trimmed) > 2 && trimmed[2] == '.')) {
		return Styles.PrimaryText.Render(line)
	}

	if trimmed == "" {
		return ""
	}

	return Styles.PrimaryText.Render(line)
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
				toolStyle(toolName).Render(fmt.Sprintf("⚡ %s: %s (%.1fs)", nodeLabel, toolName, elapsed)))
		} else if al.store.IsWaiting(entry.ID) {
			indicators = append(indicators,
				Styles.Muted.Render(fmt.Sprintf("⏳ %s: waiting for provider...", nodeLabel)))
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

// View renders the agent log viewport.
func (al *AgentLog) View() string {
	var sb strings.Builder
	sb.WriteString(Styles.ZoneLabel.Render("ACTIVITY LOG"))
	sb.WriteString("\n")

	// Build the indicator (may be multi-line for parallel nodes).
	indicator := al.activeNodeIndicators()
	indicatorRows := termLines(indicator, al.width)

	// Count in-progress partial lines from all active streams.
	var partials []string
	for nodeID, s := range al.streams {
		if s.current.Len() > 0 {
			prefix := ""
			if len(al.streams) > 1 {
				prefix = Styles.Muted.Render(nodeID+": ") + ""
			}
			partials = append(partials, prefix+Styles.PrimaryText.Render(s.current.String()))
		}
	}
	partialRows := len(partials)

	// Budget for styled content lines.
	// Total height - header(1) - indicator rows - partial line rows.
	budget := al.height - 1 - indicatorRows - partialRows
	if budget < 1 {
		budget = 1
	}

	// Walk backwards through styled lines counting terminal rows.
	totalLines := len(al.lines)
	usedRows := 0
	start := totalLines
	for start > 0 {
		rows := termLines(al.lines[start-1].text, al.width)
		if usedRows+rows > budget {
			break
		}
		usedRows += rows
		start--
	}

	// Render styled lines.
	if totalLines == 0 && len(partials) == 0 && indicator == " " {
		sb.WriteString(Styles.DimText.Render("awaiting activity..."))
		sb.WriteString("\n")
	} else {
		for i := start; i < totalLines; i++ {
			sb.WriteString(al.lines[i].text)
			sb.WriteString("\n")
		}
	}

	// Render in-progress partial lines.
	for _, p := range partials {
		sb.WriteString(p)
		sb.WriteString("\n")
	}

	// Activity indicators (always present).
	sb.WriteString(indicator)
	sb.WriteString("\n")

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

	switch toolName {
	case "bash":
		if cmd := input["command"]; cmd != "" {
			return "▸ $ " + truncateToolText(cmd, toolDisplayLimit)
		}
	case "read":
		if path := input["path"]; path != "" {
			return "▸ read " + path
		}
	case "write":
		if path := input["path"]; path != "" {
			return "▸ write " + path
		}
	case "edit", "apply_patch":
		if path := input["path"]; path != "" {
			return "▸ edit " + path
		}
	case "grep":
		if pattern := input["pattern"]; pattern != "" {
			s := "▸ grep " + pattern
			if p := input["path"]; p != "" {
				s += " " + p
			}
			return s
		}
	case "glob":
		if p := input["pattern"]; p != "" {
			return "▸ glob " + p
		}
	case "spawn_agent":
		if task := input["task"]; task != "" {
			return "▸ spawn: " + truncateToolText(task, toolDisplayLimit)
		}
	}

	for _, key := range []string{"path", "command", "pattern", "task", "query", "name", "url"} {
		if v := input[key]; v != "" {
			return "▸ " + toolName + " " + truncateToolText(v, toolDisplayLimit)
		}
	}
	return "▸ " + toolName
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
