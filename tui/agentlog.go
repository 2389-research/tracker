// ABOUTME: AgentLog component — streaming log with text coalescing, tool formatting, and thinking indicator.
// ABOUTME: Supports expand/collapse for tool output, verbose trace mode, and reasoning chunk display.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
	kind     logEntryKind
	nodeID   string
	text     string
	toolName string
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

	// Coalescing state: accumulate sequential text chunks into one entry.
	coalescing   bool
	coalesceBuf  strings.Builder
	coalesceKind logEntryKind
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
		al.addEntry(logEntry{kind: entryToolStart, nodeID: m.NodeID, toolName: m.ToolName, text: m.ToolName})
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

// appendCoalesced accumulates sequential text/reasoning chunks into one entry.
func (al *AgentLog) appendCoalesced(kind logEntryKind, nodeID, text string) {
	if al.coalescing && al.coalesceKind == kind {
		al.coalesceBuf.WriteString(text)
		// Update the last entry in-place.
		al.entries[len(al.entries)-1].text = al.coalesceBuf.String()
		return
	}
	// Start a new coalesced entry.
	al.resetCoalesce()
	al.coalescing = true
	al.coalesceKind = kind
	al.coalesceBuf.WriteString(text)
	al.addEntry(logEntry{kind: kind, nodeID: nodeID, text: al.coalesceBuf.String()})
}

// resetCoalesce ends any active text accumulation.
func (al *AgentLog) resetCoalesce() {
	al.coalescing = false
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

	if len(al.entries) == 0 && !al.isThinking() {
		sb.WriteString(Styles.DimText.Render("awaiting activity..."))
		sb.WriteString("\n")
		return sb.String()
	}

	for _, entry := range al.entries {
		line := al.renderEntry(entry)
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	// Show thinking indicator at the bottom if the focused node is thinking.
	if al.isThinking() {
		elapsed := al.thinking.Elapsed(al.focusedNode).Seconds()
		indicator := Styles.Thinking.Render(fmt.Sprintf("⟳ Thinking... (%.1fs)", elapsed))
		sb.WriteString(indicator)
		sb.WriteString("\n")
	}

	return sb.String()
}

// isThinking returns true if the focused node is currently thinking.
func (al *AgentLog) isThinking() bool {
	if al.focusedNode == "" {
		return false
	}
	return al.thinking.IsThinking(al.focusedNode)
}

// renderEntry formats a single log entry for display.
func (al *AgentLog) renderEntry(e logEntry) string {
	switch e.kind {
	case entryText:
		return Styles.PrimaryText.Render(e.text)
	case entryReasoning:
		return Styles.Muted.Render(e.text)
	case entryToolStart:
		return Styles.ToolName.Render("▸ " + e.toolName)
	case entryToolEnd:
		return al.renderToolOutput(e.text)
	case entryError:
		return Styles.Error.Render("ERROR: " + e.text)
	case entryRaw:
		return Styles.DimText.Render(e.text)
	default:
		return e.text
	}
}

// renderToolOutput renders tool output, collapsing it if not expanded.
func (al *AgentLog) renderToolOutput(output string) string {
	lines := strings.Split(output, "\n")
	if !al.expanded && len(lines) > defaultMaxCollapsedLines {
		summary := fmt.Sprintf("  ┄┄ %d lines (ctrl+o to expand)", len(lines))
		return Styles.Muted.Render(summary)
	}
	return Styles.DimText.Render(output)
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
