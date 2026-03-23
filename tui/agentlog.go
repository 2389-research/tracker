// ABOUTME: AgentLog component — streaming log with text coalescing, tool formatting, and thinking indicator.
// ABOUTME: Supports expand/collapse for tool output, verbose trace mode, and reasoning chunk display.
package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// logEntryKind categorizes log entries for rendering.
type logEntryKind int

const (
	entryText logEntryKind = iota
	entryReasoning
	entryToolStart
	entryToolEnd
	entryError
	entryRaw
)

// logEntry is a single line in the agent log.
type logEntry struct {
	kind      logEntryKind
	nodeID    string
	text      string
	toolName  string
	toolInput string

	// Cached glamour-rendered markdown for text entries.
	mdCache      string
	mdCacheWidth int
}

const defaultMaxCollapsedLines = 4

// AgentLog renders a streaming activity log with text coalescing and tool output.
type AgentLog struct {
	store        *StateStore
	thinking     *ThinkingTracker
	scroll       *ScrollView
	entries      []logEntry
	height       int
	width        int
	expanded     bool
	verboseTrace bool
	focusedNode  string

	// Markdown renderer for LLM text output, recreated on width change.
	mdRenderer *glamour.TermRenderer
	mdWidth    int

	// Coalescing state: accumulate sequential text chunks into one entry.
	coalescing   bool
	coalesceBuf  strings.Builder
	coalesceKind logEntryKind
	coalesceNode string
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
	if w != al.width {
		// Width changed — invalidate all markdown caches.
		for i := range al.entries {
			al.entries[i].mdCache = ""
			al.entries[i].mdCacheWidth = 0
		}
		al.mdRenderer = nil
		al.mdWidth = 0
	}
	al.width = w
	al.height = h
	al.scroll.SetHeight(h)
}

// getMarkdownRenderer returns a glamour renderer for the current width, creating one if needed.
func (al *AgentLog) getMarkdownRenderer() *glamour.TermRenderer {
	w := al.width
	if w <= 0 {
		w = 80
	}
	if al.mdRenderer != nil && al.mdWidth == w {
		return al.mdRenderer
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(w),
		glamour.WithPreservedNewLines(),
	)
	if err != nil {
		return nil
	}
	al.mdRenderer = r
	al.mdWidth = w
	return r
}

// SetFocusedNode sets the node ID to show the thinking indicator for.
// When the focus changes, a separator is added to visually distinguish
// output from different nodes in the activity log.
func (al *AgentLog) SetFocusedNode(nodeID string) {
	if nodeID != "" && nodeID != al.focusedNode && len(al.entries) > 0 {
		al.resetCoalesce()
		label := nodeID
		sep := fmt.Sprintf("─── %s ", label)
		al.addEntry(logEntry{kind: entryText, nodeID: nodeID, text: sep})
	}
	al.focusedNode = nodeID
}

// SetVerboseTrace enables or disables verbose trace output.
func (al *AgentLog) SetVerboseTrace(v bool) {
	al.verboseTrace = v
}

// Update processes messages and updates the log entries.
func (al *AgentLog) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case MsgTextChunk:
		al.appendCoalesced(entryText, m.NodeID, m.Text)
	case MsgReasoningChunk:
		al.appendCoalesced(entryReasoning, m.NodeID, m.Text)
	case MsgToolCallStart:
		al.resetCoalesce()
		al.addEntry(logEntry{kind: entryToolStart, nodeID: m.NodeID, toolName: m.ToolName, toolInput: m.ToolInput, text: m.ToolName})
	case MsgToolCallEnd:
		al.resetCoalesce()
		if m.Error != "" {
			al.addEntry(logEntry{kind: entryError, nodeID: m.NodeID, toolName: m.ToolName, text: m.Error})
		}
		if m.Output != "" {
			al.addEntry(logEntry{kind: entryToolEnd, nodeID: m.NodeID, toolName: m.ToolName, text: m.Output})
		}
		if m.Error == "" && m.Output == "" {
			al.addEntry(logEntry{kind: entryToolEnd, nodeID: m.NodeID, toolName: m.ToolName, text: ""})
		}
	case MsgAgentError:
		al.resetCoalesce()
		al.addEntry(logEntry{kind: entryError, nodeID: m.NodeID, text: m.Error})
	case MsgLLMProviderRaw:
		if al.verboseTrace {
			al.resetCoalesce()
			al.addEntry(logEntry{kind: entryRaw, nodeID: m.NodeID, text: m.Data})
		}
	case MsgToggleExpand:
		al.expanded = !al.expanded
	}
	return nil
}

// appendCoalesced accumulates sequential text/reasoning chunks into one entry,
// but splits on natural boundaries (double newline, or when the entry exceeds
// a size threshold) to prevent one massive entry from destabilizing the viewport.
const maxCoalesceBytes = 512

func (al *AgentLog) appendCoalesced(kind logEntryKind, nodeID, text string) {
	if al.coalescing && al.coalesceKind == kind && al.coalesceNode == nodeID {
		// If the current entry is getting large, finalize it and start a new one.
		if al.coalesceBuf.Len()+len(text) > maxCoalesceBytes || strings.Contains(text, "\n\n") {
			al.resetCoalesce()
			al.coalescing = true
			al.coalesceKind = kind
			al.coalesceNode = nodeID
			al.coalesceBuf.WriteString(text)
			al.addEntry(logEntry{kind: kind, nodeID: nodeID, text: al.coalesceBuf.String()})
			return
		}
		al.coalesceBuf.WriteString(text)
		// Update the last entry in-place and invalidate its render cache.
		last := &al.entries[len(al.entries)-1]
		last.text = al.coalesceBuf.String()
		last.mdCache = ""
		return
	}
	// Start a new coalesced entry.
	al.resetCoalesce()
	al.coalescing = true
	al.coalesceKind = kind
	al.coalesceNode = nodeID
	al.coalesceBuf.WriteString(text)
	al.addEntry(logEntry{kind: kind, nodeID: nodeID, text: al.coalesceBuf.String()})
}

// resetCoalesce ends any active text accumulation.
func (al *AgentLog) resetCoalesce() {
	al.coalescing = false
	al.coalesceNode = ""
	al.coalesceBuf.Reset()
}

// addEntry appends a log entry.
func (al *AgentLog) addEntry(e logEntry) {
	al.entries = append(al.entries, e)
}

// View renders the agent log viewport.
func (al *AgentLog) View() string {
	var sb strings.Builder
	sb.WriteString(Styles.ZoneLabel.Render("ACTIVITY LOG"))
	sb.WriteString("\n")

	if len(al.entries) == 0 && !al.isThinking() && !al.isToolRunning() {
		sb.WriteString(Styles.DimText.Render("awaiting activity..."))
		sb.WriteString("\n")
		return sb.String()
	}

	// Calculate viewport capacity (reserve 1 for header already written above).
	maxLines := al.height - 1
	if maxLines < 1 {
		maxLines = 1
	}

	// Work backwards to find which entries to render.
	// Each entry may produce multiple lines, so we over-estimate slightly
	// (maxLines+10) to ensure we never clip too aggressively.
	startIdx := len(al.entries)
	estimatedLines := 0
	for startIdx > 0 && estimatedLines < maxLines+10 {
		startIdx--
		e := &al.entries[startIdx]
		// Estimate lines: cached entries have known line count,
		// uncached entries use a rough estimate based on text length.
		if e.mdCache != "" && e.mdCacheWidth == al.width {
			estimatedLines += strings.Count(e.mdCache, "\n") + 1
		} else {
			// Rough estimate: 1 line per 80 chars, minimum 1.
			chars := len(e.text)
			est := chars/80 + 1
			if est < 1 {
				est = 1
			}
			estimatedLines += est
		}
	}

	// Render only the visible tail entries.
	// Cap each entry to half the viewport height so one massive entry
	// (e.g., a long LLM response or tool output) can't consume the
	// entire viewport and make it look like the log "cleared."
	maxEntryLines := maxLines / 2
	if maxEntryLines < 4 {
		maxEntryLines = 4
	}

	var rendered []string
	for i := startIdx; i < len(al.entries); i++ {
		line := al.renderEntry(i)
		// Split multi-line output into separate lines for proper clipping.
		parts := strings.Split(line, "\n")
		// Drop trailing empty element so entries ending with \n don't
		// waste a viewport slot on a blank line.
		if len(parts) > 0 && parts[len(parts)-1] == "" {
			parts = parts[:len(parts)-1]
		}
		// If a single entry exceeds the cap, show only its tail.
		if len(parts) > maxEntryLines {
			parts = parts[len(parts)-maxEntryLines:]
		}
		rendered = append(rendered, parts...)
	}

	// Add activity indicator as the final line.
	if indicator := al.activityIndicator(); indicator != "" {
		rendered = append(rendered, indicator)
	}

	// Clip to viewport height (show tail, auto-scroll behavior).
	if len(rendered) > maxLines {
		rendered = rendered[len(rendered)-maxLines:]
	}

	for _, l := range rendered {
		sb.WriteString(l)
		sb.WriteString("\n")
	}

	return sb.String()
}

// activityIndicator returns the current phase indicator string for the focused node.
// Priority: tool running > waiting for provider > LLM thinking > nothing.
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

// isThinking returns true if the focused node is currently thinking.
func (al *AgentLog) isThinking() bool {
	if al.focusedNode == "" {
		return false
	}
	return al.thinking.IsThinking(al.focusedNode)
}

// isToolRunning returns true if the focused node is currently executing a tool.
func (al *AgentLog) isToolRunning() bool {
	if al.focusedNode == "" {
		return false
	}
	return al.thinking.IsToolRunning(al.focusedNode)
}

// renderEntry formats a single log entry for display, using the entry index
// so text entries can cache their glamour-rendered markdown in place.
func (al *AgentLog) renderEntry(idx int) string {
	e := &al.entries[idx]
	switch e.kind {
	case entryText:
		return al.renderMarkdown(e)
	case entryReasoning:
		return Styles.Muted.Render(e.text)
	case entryToolStart:
		return toolStyle(e.toolName).Render(formatToolDisplay(e.toolName, e.toolInput))
	case entryToolEnd:
		return al.renderToolOutput(*e)
	case entryError:
		return Styles.Error.Render("ERROR: " + e.text)
	case entryRaw:
		return Styles.DimText.Render(e.text)
	default:
		return e.text
	}
}

// renderMarkdown renders a text entry through glamour, caching the result.
func (al *AgentLog) renderMarkdown(e *logEntry) string {
	if e.mdCache != "" && e.mdCacheWidth == al.width {
		return e.mdCache
	}
	r := al.getMarkdownRenderer()
	if r == nil {
		return Styles.PrimaryText.Render(e.text)
	}
	rendered, err := r.Render(e.text)
	if err != nil {
		return Styles.PrimaryText.Render(e.text)
	}
	rendered = strings.TrimRight(rendered, "\n")
	rendered = strings.TrimLeft(rendered, "\n")
	e.mdCache = rendered
	e.mdCacheWidth = al.width
	return rendered
}

// renderToolOutput renders tool output, collapsing it if not expanded.
func (al *AgentLog) renderToolOutput(e logEntry) string {
	if e.text == "" {
		return Styles.Muted.Render("  ✓ " + e.toolName)
	}
	lines := strings.Split(e.text, "\n")
	if !al.expanded && len(lines) > defaultMaxCollapsedLines {
		summary := fmt.Sprintf("  ✓ %s (%d lines, ctrl+o to expand)", e.toolName, len(lines))
		return Styles.Muted.Render(summary)
	}
	return Styles.DimText.Render(e.text)
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

	// Fallback: show tool name with best-effort summary from input fields.
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
