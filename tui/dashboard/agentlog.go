// ABOUTME: Dashboard agent log component — scrolling viewport of agent actions and pipeline events.
// ABOUTME: Buffers log lines and renders a fixed-height scrollable view of the most recent entries.
package dashboard

import (
	"fmt"
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

	agentLogEntryStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("15"))

	agentLogTimestampStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")).
				Faint(true)

	agentLogEventTypeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("14"))

	agentLogErrorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("9"))
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
		return title + "\n" + agentLogEntryStyle.Render("(initializing…)")
	}
	return title + "\n" + a.viewport.View()
}

// Len returns the number of log entries.
func (a AgentLogModel) Len() int { return len(a.entries) }

// refreshViewport rebuilds the viewport content from entries and scrolls to bottom.
func (a *AgentLogModel) refreshViewport() {
	var sb strings.Builder
	for _, entry := range a.entries {
		sb.WriteString(formatLogEntry(entry))
		sb.WriteString("\n")
	}
	a.viewport.SetContent(sb.String())
	a.viewport.GotoBottom()
}

func formatLogEntry(e LogEntry) string {
	var parts []string

	ts := e.Time.Format("15:04:05")
	parts = append(parts, agentLogTimestampStyle.Render(ts))

	if e.EventType != "" {
		parts = append(parts, agentLogEventTypeStyle.Render("["+e.EventType+"]"))
	}

	if e.NodeID != "" {
		parts = append(parts, agentLogEntryStyle.Render(fmt.Sprintf("(%s)", e.NodeID)))
	}

	if e.Message != "" {
		msgStyle := agentLogEntryStyle
		if e.IsError {
			msgStyle = agentLogErrorStyle
		}
		parts = append(parts, msgStyle.Render(e.Message))
	}

	return strings.Join(parts, " ")
}
