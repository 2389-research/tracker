// ABOUTME: Dashboard activity log — scrolling data recorder showing LLM calls and pipeline events.
// ABOUTME: "Signal Cabin" aesthetic: timestamped entries in [NodeID] format, color-coded by event type.
package dashboard

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// LogEntry represents a single line in the activity log.
type LogEntry struct {
	Time      time.Time
	EventType string
	NodeID    string
	Message   string
	ToolName  string // tool name for color-coding tool events
	IsError   bool
	Dim       bool
}

const defaultCollapseLines = 4

// AgentLogModel is a scrollable data recorder of pipeline events and LLM activity.
type AgentLogModel struct {
	entries  []LogEntry
	viewport viewport.Model
	width    int
	height   int
	ready    bool

	// Text/reasoning coalescing: accumulate streaming chunks into a single entry.
	// coalesceBuf is a pointer to avoid the strings.Builder copy-after-write panic
	// when bubbletea copies the parent AppModel by value during Update.
	coalesceBuf    *strings.Builder
	coalesceKind   llm.TraceKind // which kind we're accumulating (TraceText or TraceReasoning)
	coalesceActive bool

	// activeModel tracks the current provider/model so we only emit a header
	// line when the model changes, rather than repeating it on every line.
	activeModel string

	// expanded toggles between collapsed (4-line max) and full output display.
	expanded bool
}

// NewAgentLogModel creates an activity log model with the given viewport dimensions.
func NewAgentLogModel(width, height int) AgentLogModel {
	vp := viewport.New(width, height)
	vp.SetContent("")
	return AgentLogModel{
		viewport:    vp,
		width:       width,
		height:      height,
		ready:       width > 0 && height > 0,
		coalesceBuf: &strings.Builder{},
	}
}

// Init satisfies tea.Model.
func (a AgentLogModel) Init() tea.Cmd { return nil }

// Update handles scroll messages and size changes.
func (a AgentLogModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.viewport.Width = msg.Width
		a.viewport.Height = msg.Height
		a.ready = true
		a.refreshViewport()
	}
	var cmd tea.Cmd
	a.viewport, cmd = a.viewport.Update(msg)
	return a, cmd
}

// AppendEvent adds a pipeline event to the log.
func (a *AgentLogModel) AppendEvent(evt pipeline.PipelineEvent) {
	isError := evt.Err != nil ||
		evt.Type == pipeline.EventPipelineFailed ||
		evt.Type == pipeline.EventStageFailed

	// Always use the styled event message; append error detail if present.
	msg := eventTypeToMessage(evt.Type)
	if evt.Err != nil {
		msg = msg + "\n  " + evt.Err.Error()
	}

	entry := LogEntry{
		Time:      evt.Timestamp,
		EventType: string(evt.Type),
		NodeID:    evt.NodeID,
		Message:   msg,
		IsError:   isError,
	}
	a.entries = append(a.entries, entry)
	a.refreshViewport()
}

// AppendLine adds a raw text line to the log.
func (a *AgentLogModel) AppendLine(line string) {
	a.entries = append(a.entries, LogEntry{
		Time:    time.Now(),
		Message: line,
	})
	a.refreshViewport()
}

// AppendTrace adds a formatted LLM trace event to the log.
// Text and reasoning chunks are coalesced into a single log entry rather than
// producing one line per streaming delta. In non-verbose mode, internal LLM
// events (start, finish, tool prepare) are suppressed for a clean chat-like log.
func (a *AgentLogModel) AppendTrace(evt llm.TraceEvent, verbose bool) {
	// Coalesce text and reasoning deltas into one entry.
	if evt.Kind == llm.TraceText || evt.Kind == llm.TraceReasoning {
		// Emit a model header when the provider/model changes.
		modelKey := llm.FormatModelHeader(evt.Provider, evt.Model)
		if modelKey != "" && modelKey != a.activeModel {
			a.resetCoalesce()
			a.activeModel = modelKey
			a.entries = append(a.entries, LogEntry{
				Time:      time.Now(),
				EventType: "model_header",
				Message:   "[" + modelKey + "]",
			})
		}

		if a.coalesceActive && a.coalesceKind == evt.Kind {
			// Append to existing buffer and update last entry in-place.
			a.coalesceBuf.WriteString(evt.Preview)
			a.entries[len(a.entries)-1].Message = llm.FormatCoalescedLine(evt.Kind, a.coalesceBuf.String())
			a.refreshViewport()
			return
		}
		// Start a new coalesced entry.
		a.resetCoalesce()
		a.coalesceActive = true
		a.coalesceKind = evt.Kind
		a.coalesceBuf.WriteString(evt.Preview)
		a.entries = append(a.entries, LogEntry{
			Time:      time.Now(),
			EventType: string(evt.Kind),
			Message:   llm.FormatCoalescedLine(evt.Kind, a.coalesceBuf.String()),
		})
		a.refreshViewport()
		return
	}

	// In non-verbose mode, suppress internal LLM plumbing events.
	// The actual tool execution is shown via agent events (EventToolCallStart/End).
	// Tool prepare does NOT break text coalescing — it's an LLM-internal signal.
	if !verbose {
		switch evt.Kind {
		case llm.TraceRequestStart, llm.TraceToolPrepare, llm.TraceProviderRaw:
			return
		case llm.TraceFinish:
			// Finalize any active coalescing but don't add a log entry.
			a.resetCoalesce()
			return
		}
	}

	// In verbose mode (or for event types not filtered above), break coalescing
	// and show the raw trace line.
	a.resetCoalesce()

	line := llm.FormatTraceLine(evt, verbose)
	if line == "" {
		return
	}

	a.entries = append(a.entries, LogEntry{
		Time:      time.Now(),
		EventType: string(evt.Kind),
		Message:   line,
		Dim:       evt.Kind == llm.TraceProviderRaw,
	})
	a.refreshViewport()
}

// resetCoalesce ends any active text/reasoning accumulation.
func (a *AgentLogModel) resetCoalesce() {
	a.coalesceActive = false
	a.coalesceBuf.Reset()
	a.coalesceKind = ""
}

// AppendAgentEvent adds a formatted live agent event to the log.
func (a *AgentLogModel) AppendAgentEvent(evt agent.Event) {
	line := agent.FormatEventLine(evt)
	if line == "" {
		return
	}

	a.entries = append(a.entries, LogEntry{
		Time:      time.Now(),
		EventType: string(evt.Type),
		ToolName:  evt.ToolName,
		Message:   line,
	})
	a.refreshViewport()
}

// ToggleExpanded switches between collapsed (4-line max) and full output display.
func (a *AgentLogModel) ToggleExpanded() {
	a.expanded = !a.expanded
	a.refreshViewport()
}

// SetSize updates the viewport dimensions.
func (a *AgentLogModel) SetSize(width, height int) {
	a.width = width
	a.height = height
	a.viewport.Width = width
	a.viewport.Height = height
	a.ready = true
	a.refreshViewport()
}

// View renders the activity log viewport.
func (a AgentLogModel) View() string {
	title := zoneLabelStyle.Render("ACTIVITY LOG")
	if !a.ready {
		return title + "\n" + dimTextStyle.Render("initializing…")
	}
	return title + "\n" + a.viewport.View()
}

// Len returns the number of log entries.
func (a AgentLogModel) Len() int { return len(a.entries) }

// refreshViewport rebuilds the viewport content from entries.
// Only auto-scrolls to bottom if the user hasn't scrolled up.
func (a *AgentLogModel) refreshViewport() {
	atBottom := a.viewport.AtBottom()

	var sb strings.Builder
	for _, entry := range a.entries {
		sb.WriteString(formatLogEntry(entry, a.width, a.expanded))
		sb.WriteString("\n")
	}
	a.viewport.SetContent(sb.String())

	if atBottom {
		a.viewport.GotoBottom()
	}
}

// formatLogEntry formats a log entry with chat-like styling.
// LLM text entries show as conversation messages, tool calls as action blocks,
// and system events as dim status lines.
func formatLogEntry(e LogEntry, maxWidth int, expanded bool) string {
	var sb strings.Builder

	// [NodeID] as a signal label
	if e.NodeID != "" {
		nodeStyle := lipgloss.NewStyle().Foreground(colorReadout).Bold(true)
		sb.WriteString(nodeStyle.Render("[" + e.NodeID + "]"))
		sb.WriteString(" ")
	}

	// Available width for message content after prefix.
	prefixLen := 0
	if e.NodeID != "" {
		prefixLen += len(e.NodeID) + 3 // "[nodeID] "
	}
	msgWidth := maxWidth - prefixLen - 2
	if msgWidth < 20 {
		msgWidth = 20
	}

	msg := e.Message

	// Multi-line content (LLM text, tool output, errors, banners) gets
	// word-wrapped. Everything else gets single-line truncated.
	wrapContent := isLLMTextEvent(e.EventType) || isToolEndEvent(e.EventType) ||
		e.EventType == "model_header" || e.IsError
	if maxWidth > 0 && !wrapContent {
		if runeLen := len([]rune(msg)); runeLen > msgWidth {
			msg = string([]rune(msg)[:msgWidth-1]) + "…"
		}
	}

	// Build the styled message, applying width constraint for wrapping entries.
	var styledMsg string
	switch {
	case e.IsError:
		styledMsg = lipgloss.NewStyle().Foreground(colorRed).Bold(true).Render(msg)
	case e.Dim:
		styledMsg = dimTextStyle.Render(msg)
	case e.EventType == "model_header":
		styledMsg = lipgloss.NewStyle().Foreground(colorReadout).Bold(true).Render(msg)
	case isLLMTextEvent(e.EventType):
		styledMsg = lipgloss.NewStyle().Foreground(colorBrightText).Bold(true).Width(msgWidth).Render(msg)
	case isToolStartEvent(e.EventType):
		styledMsg = lipgloss.NewStyle().Foreground(toolColor(e.ToolName)).Render(msg)
	case isToolEndEvent(e.EventType):
		styledMsg = lipgloss.NewStyle().Foreground(colorDim).Width(msgWidth).Render(msg)
	case isCompletionEvent(e.EventType):
		styledMsg = lipgloss.NewStyle().Foreground(colorGreen).Render(msg)
	default:
		styledMsg = primaryTextStyle.Render(msg)
	}

	// In collapsed mode, limit multi-line content to defaultCollapseLines.
	if !expanded && wrapContent {
		styledMsg = collapseLines(styledMsg, defaultCollapseLines)
	}

	sb.WriteString(styledMsg)

	return sb.String()
}

// collapseLines truncates rendered text to maxLines, appending a dim ellipsis
// indicator if content was cut. Returns the original string if within limit.
func collapseLines(rendered string, maxLines int) string {
	lines := strings.Split(rendered, "\n")
	if len(lines) <= maxLines {
		return rendered
	}
	truncated := strings.Join(lines[:maxLines], "\n")
	more := fmt.Sprintf("  ┄┄ +%d lines (ctrl+o to expand)", len(lines)-maxLines)
	return truncated + "\n" + dimTextStyle.Render(more)
}

// isLLMTextEvent returns true for coalesced LLM text or reasoning entries.
func isLLMTextEvent(eventType string) bool {
	switch llm.TraceKind(eventType) {
	case llm.TraceText, llm.TraceReasoning:
		return true
	}
	return false
}

// isToolStartEvent returns true for tool call start entries.
func isToolStartEvent(eventType string) bool {
	return agent.EventType(eventType) == agent.EventToolCallStart
}

// isToolEndEvent returns true for tool call end entries.
func isToolEndEvent(eventType string) bool {
	return agent.EventType(eventType) == agent.EventToolCallEnd
}

// toolColor returns a distinct color for each tool category.
func toolColor(toolName string) lipgloss.TerminalColor {
	switch toolName {
	case "bash":
		return colorBash
	case "read", "write", "edit":
		return colorFile
	case "grep", "glob":
		return colorGrep
	case "spawn_agent":
		return colorAgent
	case "apply_patch":
		return colorPatch
	default:
		return colorAmber
	}
}

// eventTypeToMessage converts a pipeline event type to a human-readable message.
// 90s Akihabara arcade aesthetic — neon box-drawing, signal lamps, and vibes.
func eventTypeToMessage(t pipeline.PipelineEventType) string {
	switch t {
	case pipeline.EventPipelineStarted:
		return "▶▶▶ " + lampActive + " PIPELINE GO ━━━━━━━━━━━━━━━━━━"
	case pipeline.EventPipelineCompleted:
		return "━━━━━━━━━━━ " + lampOn + " ALL CLEAR " + lampOn + " ━━━━━━━━━━━"
	case pipeline.EventPipelineFailed:
		return "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n" +
			"  " + lampError + lampError + lampError + "  PIPELINE FAILED  " + lampError + lampError + lampError + "\n" +
			"━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	case pipeline.EventStageStarted:
		return "▸ " + lampActive + " spinning up"
	case pipeline.EventStageCompleted:
		return "▸ " + lampOn + " clear!"
	case pipeline.EventStageFailed:
		return "▸ " + lampError + " fault"
	case pipeline.EventStageRetrying:
		return "▸ " + lampOff + " retry…"
	case pipeline.EventParallelStarted:
		return "┣━▸ " + lampActive + " parallel go"
	case pipeline.EventParallelCompleted:
		return "┗━▸ " + lampOn + " parallel clear"
	case pipeline.EventInterviewStarted:
		return "┃ " + lampOff + " waiting on human…"
	case pipeline.EventInterviewCompleted:
		return "┃ " + lampOn + " got it"
	case pipeline.EventCheckpointSaved:
		return "━━ " + lampOn + " saved " + lampOn + " ━━"
	default:
		return string(t)
	}
}

// isCompletionEvent returns true for events that represent successful completions.
func isCompletionEvent(eventType string) bool {
	switch pipeline.PipelineEventType(eventType) {
	case pipeline.EventStageCompleted, pipeline.EventPipelineCompleted, pipeline.EventParallelCompleted:
		return true
	}
	return false
}
