// ABOUTME: Dashboard activity log — scrolling data recorder showing LLM calls and pipeline events.
// ABOUTME: "Signal Cabin" aesthetic: timestamped entries in [NodeID] format, color-coded by event type.
package dashboard

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/2389-research/tracker/pipeline"
)

// LogEntry represents a single line in the activity log.
type LogEntry struct {
	Time      time.Time
	EventType string
	NodeID    string
	Message   string
	IsError   bool
}

// AgentLogModel is a scrollable data recorder of pipeline events and LLM activity.
type AgentLogModel struct {
	entries  []LogEntry
	viewport viewport.Model
	width    int
	height   int
	ready    bool
}

// NewAgentLogModel creates an activity log model with the given viewport dimensions.
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

// formatLogEntry formats a log entry in the control panel data recorder style.
// Format: HH:MM:SS [NodeID] message
func formatLogEntry(e LogEntry, maxWidth int) string {
	var sb strings.Builder

	// Timestamp in dim readout style
	ts := e.Time.Format("15:04:05")
	sb.WriteString(dimTextStyle.Render(ts))
	sb.WriteString(" ")

	// [NodeID] as a signal label
	if e.NodeID != "" {
		nodeStyle := lipgloss.NewStyle().Foreground(colorReadout).Bold(true)
		sb.WriteString(nodeStyle.Render("[" + e.NodeID + "]"))
		sb.WriteString(" ")
	}

	// Message with status-appropriate styling
	msg := e.Message
	if maxWidth > 0 {
		// Truncate to prevent wrapping
		prefixLen := 9 // timestamp
		if e.NodeID != "" {
			prefixLen += len(e.NodeID) + 3
		}
		maxMsg := maxWidth - prefixLen - 2
		if maxMsg > 0 && len(msg) > maxMsg {
			msg = msg[:maxMsg-1] + "…"
		}
	}

	if e.IsError {
		sb.WriteString(lipgloss.NewStyle().Foreground(colorRed).Render(msg))
	} else if isCompletionEvent(e.EventType) {
		sb.WriteString(lipgloss.NewStyle().Foreground(colorGreen).Render(msg))
	} else {
		sb.WriteString(primaryTextStyle.Render(msg))
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
