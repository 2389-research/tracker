// ABOUTME: Dashboard agent log component — scrolling viewport of agent actions and pipeline events.
// ABOUTME: Buffers log lines and renders a fixed-height scrollable view in [NodeID] message format.
package dashboard

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/2389-research/tracker/pipeline"
)

var (
	agentLogTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("12"))

	agentLogNodeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("14")).
				Bold(true)

	agentLogMsgStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("15"))

	agentLogTimestampStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")).
				Faint(true)

	agentLogEventStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")).
				Faint(true)

	agentLogErrorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("9"))

	agentLogSuccessStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("10"))
)

// LogEntry represents a single line in the agent log.
type LogEntry struct {
	Time      time.Time
	EventType string
	NodeID    string
	Message   string
	IsError   bool
}

// AgentLogModel is a scrollable log of pipeline events and agent actions.
type AgentLogModel struct {
	entries  []LogEntry
	viewport viewport.Model
	width    int
	height   int
	ready    bool
}

// NewAgentLogModel creates an agent log model with the given viewport dimensions.
func NewAgentLogModel(width, height int) AgentLogModel {
	vp := viewport.New(width, height)
	vp.SetContent("")
	return AgentLogModel{
		viewport: vp,
		width:    width,
		height:   height,
		ready:    width > 0 && height > 0,
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
	isError := evt.Err != nil
	msg := evt.Message
	if isError && evt.Err != nil {
		if msg != "" {
			msg = msg + ": " + evt.Err.Error()
		} else {
			msg = evt.Err.Error()
		}
	}

	// Generate a readable message from event type if no message provided
	if msg == "" {
		msg = eventTypeToMessage(evt.Type)
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

// SetSize updates the viewport dimensions.
func (a *AgentLogModel) SetSize(width, height int) {
	a.width = width
	a.height = height
	a.viewport.Width = width
	a.viewport.Height = height
	a.ready = true
	a.refreshViewport()
}

// View renders the agent log viewport.
func (a AgentLogModel) View() string {
	title := agentLogTitleStyle.Render("Agent Log")
	if !a.ready {
		return title + "\n" + agentLogMsgStyle.Render("(initializing…)")
	}
	return title + "\n" + a.viewport.View()
}

// Len returns the number of log entries.
func (a AgentLogModel) Len() int { return len(a.entries) }

// refreshViewport rebuilds the viewport content from entries and scrolls to bottom.
func (a *AgentLogModel) refreshViewport() {
	var sb strings.Builder
	for _, entry := range a.entries {
		sb.WriteString(formatLogEntry(entry, a.width))
		sb.WriteString("\n")
	}
	a.viewport.SetContent(sb.String())
	a.viewport.GotoBottom()
}

// formatLogEntry formats a log entry in the spec's [NodeID] message style.
func formatLogEntry(e LogEntry, maxWidth int) string {
	var sb strings.Builder

	// Timestamp (compact)
	ts := e.Time.Format("15:04:05")
	sb.WriteString(agentLogTimestampStyle.Render(ts))
	sb.WriteString(" ")

	// [NodeID] prefix if present — this is the spec format
	if e.NodeID != "" {
		sb.WriteString(agentLogNodeStyle.Render("[" + e.NodeID + "]"))
		sb.WriteString(" ")
	}

	// Message with appropriate styling
	msg := e.Message
	if maxWidth > 0 {
		// Rough truncation to prevent wrapping
		prefixLen := 9 // timestamp
		if e.NodeID != "" {
			prefixLen += len(e.NodeID) + 3 // brackets + space
		}
		maxMsg := maxWidth - prefixLen - 2
		if maxMsg > 0 && len(msg) > maxMsg {
			msg = msg[:maxMsg-1] + "…"
		}
	}

	if e.IsError {
		sb.WriteString(agentLogErrorStyle.Render(msg))
	} else if isCompletionEvent(e.EventType) {
		sb.WriteString(agentLogSuccessStyle.Render(msg))
	} else {
		sb.WriteString(agentLogMsgStyle.Render(msg))
	}

	return sb.String()
}

// eventTypeToMessage converts a pipeline event type to a human-readable message.
func eventTypeToMessage(t pipeline.PipelineEventType) string {
	switch t {
	case pipeline.EventPipelineStarted:
		return "Pipeline started"
	case pipeline.EventPipelineCompleted:
		return "Pipeline completed"
	case pipeline.EventPipelineFailed:
		return "Pipeline failed"
	case pipeline.EventStageStarted:
		return "Starting…"
	case pipeline.EventStageCompleted:
		return "Done"
	case pipeline.EventStageFailed:
		return "Failed"
	case pipeline.EventStageRetrying:
		return "Retrying…"
	case pipeline.EventParallelStarted:
		return "Parallel group started"
	case pipeline.EventParallelCompleted:
		return "Parallel group completed"
	case pipeline.EventInterviewStarted:
		return "Awaiting input…"
	case pipeline.EventInterviewCompleted:
		return "Input received"
	case pipeline.EventCheckpointSaved:
		return "Checkpoint saved"
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
