// ABOUTME: AgentLog component — append-only streaming log with line-level styling.
// ABOUTME: Styles lines once on newline, never re-renders. Stable viewport via fixed tail slice.
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

// AgentLog renders a streaming activity log. Text arrives as token-level chunks
// and is accumulated into lines. Each line is styled once when a newline arrives
// and cached permanently. The viewport always shows the last N styled lines.
type AgentLog struct {
	store        *StateStore
	thinking     *ThinkingTracker
	scroll       *ScrollView
	height       int
	width        int
	expanded     bool
	verboseTrace bool
	focusedNode  string

	// styledLines is the append-only buffer of rendered lines.
	// Lines are styled once and never re-rendered.
	styledLines []string

	// current accumulates the in-progress line (no newline yet).
	current     strings.Builder
	currentNode string

	// inCodeBlock tracks whether we're inside a fenced code block.
	inCodeBlock bool
}

// NewAgentLog creates an AgentLog with the given state, thinking tracker, and viewport height.
func NewAgentLog(store *StateStore, thinking *ThinkingTracker, height int) *AgentLog {
	return &AgentLog{
		store:    store,
		thinking: thinking,
		scroll:   NewScrollView(height),
		height:   height,
	}
}

// SetSize updates both width and height for the agent log viewport.
func (al *AgentLog) SetSize(w, h int) {
	al.width = w
	al.height = h
	al.scroll.SetHeight(h)
}

// SetFocusedNode sets the node ID to show the thinking indicator for.
func (al *AgentLog) SetFocusedNode(nodeID string) {
	if nodeID != "" && nodeID != al.focusedNode && len(al.styledLines) > 0 {
		al.flushCurrent()
		sep := Styles.Muted.Render(fmt.Sprintf("─── %s ", nodeID))
		al.styledLines = append(al.styledLines, sep)
	}
	al.focusedNode = nodeID
}

// SetVerboseTrace enables or disables verbose trace output.
func (al *AgentLog) SetVerboseTrace(v bool) {
	al.verboseTrace = v
}

// Update processes messages and updates the log.
func (al *AgentLog) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case MsgTextChunk:
		al.appendText(m.NodeID, m.Text)
	case MsgReasoningChunk:
		al.appendReasoning(m.Text)
	case MsgToolCallStart:
		al.flushCurrent()
		line := toolStyle(m.ToolName).Render(formatToolDisplay(m.ToolName, m.ToolInput))
		al.styledLines = append(al.styledLines, line)
	case MsgToolCallEnd:
		al.flushCurrent()
		al.appendToolEnd(m)
	case MsgAgentError:
		al.flushCurrent()
		al.styledLines = append(al.styledLines, Styles.Error.Render("ERROR: "+m.Error))
	case MsgLLMProviderRaw:
		if al.verboseTrace {
			al.flushCurrent()
			al.styledLines = append(al.styledLines, Styles.DimText.Render(m.Data))
		}
	case MsgToggleExpand:
		al.expanded = !al.expanded
	}
	return nil
}

// appendText processes streaming LLM text, splitting on newlines.
// Complete lines get styled immediately. The trailing partial line
// stays in al.current and renders as plain text.
func (al *AgentLog) appendText(nodeID, text string) {
	al.currentNode = nodeID
	for _, ch := range text {
		if ch == '\n' {
			line := al.current.String()
			al.current.Reset()
			al.styledLines = append(al.styledLines, al.styleLine(line))
		} else {
			al.current.WriteRune(ch)
		}
	}
}

// appendReasoning adds reasoning text as muted lines.
func (al *AgentLog) appendReasoning(text string) {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			al.styledLines = append(al.styledLines, Styles.Muted.Render(line))
		}
	}
}

// appendToolEnd adds collapsed or expanded tool output.
func (al *AgentLog) appendToolEnd(m MsgToolCallEnd) {
	if m.Error != "" {
		al.styledLines = append(al.styledLines, Styles.Error.Render("  ✗ "+m.ToolName+": "+m.Error))
		return
	}
	if m.Output == "" {
		al.styledLines = append(al.styledLines, Styles.Muted.Render("  ✓ "+m.ToolName))
		return
	}
	lines := strings.Split(m.Output, "\n")
	if !al.expanded && len(lines) > defaultMaxCollapsedLines {
		al.styledLines = append(al.styledLines,
			Styles.Muted.Render(fmt.Sprintf("  ✓ %s (%d lines, ctrl+o to expand)", m.ToolName, len(lines))))
		return
	}
	for _, line := range lines {
		al.styledLines = append(al.styledLines, Styles.DimText.Render(line))
	}
}

// flushCurrent finalizes any in-progress line.
func (al *AgentLog) flushCurrent() {
	if al.current.Len() > 0 {
		al.styledLines = append(al.styledLines, al.styleLine(al.current.String()))
		al.current.Reset()
	}
}

// styleLine applies lightweight line-level formatting.
// Styled once, cached forever. No re-interpretation.
func (al *AgentLog) styleLine(line string) string {
	trimmed := strings.TrimSpace(line)

	// Code fence toggle.
	if strings.HasPrefix(trimmed, "```") {
		al.inCodeBlock = !al.inCodeBlock
		return Styles.Muted.Render(line)
	}

	// Inside code block — dim monospace.
	if al.inCodeBlock {
		return Styles.DimText.Render(line)
	}

	// Headers — bold.
	if strings.HasPrefix(trimmed, "# ") {
		return lipgloss.NewStyle().Bold(true).Foreground(ColorReadout).Render(trimmed)
	}
	if strings.HasPrefix(trimmed, "## ") {
		return lipgloss.NewStyle().Bold(true).Foreground(ColorLabel).Render(trimmed)
	}
	if strings.HasPrefix(trimmed, "### ") {
		return lipgloss.NewStyle().Bold(true).Render(trimmed)
	}

	// Bullet lists — keep indent, primary text.
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
		return Styles.PrimaryText.Render(line)
	}

	// Numbered lists.
	if len(trimmed) > 2 && trimmed[0] >= '0' && trimmed[0] <= '9' && (trimmed[1] == '.' || (len(trimmed) > 2 && trimmed[2] == '.')) {
		return Styles.PrimaryText.Render(line)
	}

	// Bold text (**...**) — just render as primary, lipgloss handles bold inline
	// Empty lines.
	if trimmed == "" {
		return ""
	}

	// Default.
	return Styles.PrimaryText.Render(line)
}

// View renders the agent log viewport.
func (al *AgentLog) View() string {
	var sb strings.Builder
	sb.WriteString(Styles.ZoneLabel.Render("ACTIVITY LOG"))
	sb.WriteString("\n")

	// Reserve lines for: header (already written), activity indicator, and
	// optionally the in-progress partial line.
	reserved := 2 // header + indicator
	if al.current.Len() > 0 {
		reserved = 3 // header + in-progress + indicator
	}
	maxContent := al.height - reserved
	if maxContent < 1 {
		maxContent = 1
	}

	totalStyled := len(al.styledLines)

	// Show the tail of styled lines, leaving room for reserved lines.
	start := totalStyled - maxContent
	if start < 0 {
		start = 0
	}

	for i := start; i < totalStyled; i++ {
		sb.WriteString(al.styledLines[i])
		sb.WriteString("\n")
	}

	// In-progress line (unstyled, still accumulating tokens).
	if al.current.Len() > 0 {
		sb.WriteString(Styles.PrimaryText.Render(al.current.String()))
		sb.WriteString("\n")
	} else if totalStyled == 0 && !al.isThinking() && !al.isToolRunning() {
		sb.WriteString(Styles.DimText.Render("awaiting activity..."))
		sb.WriteString("\n")
	}

	// Activity indicator — always present (space when idle).
	indicator := al.activityIndicator()
	if indicator == "" {
		indicator = " "
	}
	sb.WriteString(indicator)
	sb.WriteString("\n")

	return sb.String()
}

// activityIndicator returns the current phase indicator string for the focused node.
func (al *AgentLog) activityIndicator() string {
	if al.focusedNode == "" {
		return ""
	}
	if toolName := al.thinking.ToolName(al.focusedNode); toolName != "" {
		elapsed := al.thinking.Elapsed(al.focusedNode).Seconds()
		return toolStyle(toolName).Render(fmt.Sprintf("⚡ %s (%.1fs)", toolName, elapsed))
	}
	if al.store.IsWaiting(al.focusedNode) {
		return Styles.Muted.Render("⏳ Waiting for provider...")
	}
	if al.isThinking() {
		elapsed := al.thinking.Elapsed(al.focusedNode).Seconds()
		return Styles.Thinking.Render(fmt.Sprintf("⟳ Thinking... (%.1fs)", elapsed))
	}
	return ""
}

func (al *AgentLog) isThinking() bool {
	if al.focusedNode == "" {
		return false
	}
	return al.thinking.IsThinking(al.focusedNode)
}

func (al *AgentLog) isToolRunning() bool {
	if al.focusedNode == "" {
		return false
	}
	return al.thinking.IsToolRunning(al.focusedNode)
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
