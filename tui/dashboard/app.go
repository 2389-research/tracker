// ABOUTME: Main TUI dashboard app — "Signal Cabin" control panel layout with instrument cluster zones.
// ABOUTME: Composes header gauge cluster, node signal panel, activity log, and track diagram status bar.
package dashboard

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/tui/components"
)

// ─── Layout constants ─────────────────────────────────────────────────────────

const (
	nodeListWidthPct = 28 // percent of terminal width for the signal panel
	minNodeListWidth = 20
	minAgentLogWidth = 30
	headerHeight     = 4 // two content lines + border lines
	statusBarHeight  = 1
	tickInterval     = time.Second
)

// ─── Messages sent into the TUI loop ─────────────────────────────────────────

// PipelineEventMsg wraps a pipeline.PipelineEvent for delivery to the TUI.
type PipelineEventMsg struct{ Event pipeline.PipelineEvent }

// GateChoiceMsg requests a choice gate modal from the TUI.
type GateChoiceMsg struct {
	Prompt        string
	Choices       []string
	DefaultChoice string
	ReplyCh       chan<- string
}

// GateFreeformMsg requests a freeform gate modal from the TUI.
type GateFreeformMsg struct {
	Prompt  string
	ReplyCh chan<- string
}

// PipelineDoneMsg signals that the pipeline goroutine has finished.
type PipelineDoneMsg struct{ Err error }

// LLMActivityMsg wraps an LLM activity event for display in the activity log.
type LLMActivityMsg struct{ Summary string }

// LLMTraceMsg wraps a structured LLM trace event for display in the activity log.
type LLMTraceMsg struct{ Event llm.TraceEvent }

// AgentEventMsg wraps a live agent event for display in the activity log.
type AgentEventMsg struct{ Event agent.Event }

// tickMsg is sent periodically to update the elapsed time display.
type tickMsg time.Time

// ─── Modal state ──────────────────────────────────────────────────────────────

type modalKind int

const (
	modalNone     modalKind = iota
	modalChoice             // showing a ChoiceModel in a modal
	modalFreeform           // showing a FreeformModel in a modal
)

// ─── Instrument panel frame styles ───────────────────────────────────────────

var (
	// Outer frame — double-line bezel border around entire dashboard
	outerFrameStyle = lipgloss.NewStyle().
			Border(panelBorder).
			BorderForeground(colorBezel)

	// Vertical divider between node panel and activity log
	paneDividerStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), false, true, false, false).
				BorderForeground(colorBezel)

	// Status bar at the bottom
	statusBarBaseStyle = lipgloss.NewStyle().
				Background(colorPanel).
				Foreground(colorBrightText).
				Padding(0, 1)
)

// ─── App model ────────────────────────────────────────────────────────────────

// AppModel is the root bubbletea model for mode 2 (full TUI dashboard).
type AppModel struct {
	// Layout
	width  int
	height int

	// Dashboard components
	header   HeaderModel
	nodeList NodeListModel
	agentLog AgentLogModel

	// Modal state
	modalKind     modalKind
	choiceModal   components.ChoiceModel
	freeformModal components.FreeformModel
	modalTitle    string
	activeMsgCh   chan<- string

	// Lifecycle
	pipelineDone bool
	pipelineErr  error
	quitting     bool
	verboseTrace bool

	// Background viewport (rendered behind modal)
	bgViewport viewport.Model
}

// NewAppModel constructs the dashboard AppModel.
func NewAppModel(pipelineName string, tracker *llm.TokenTracker) AppModel {
	return AppModel{
		header:   NewHeaderModel(pipelineName, tracker),
		nodeList: NewNodeListModel(nil),
		agentLog: NewAgentLogModel(0, 0),
	}
}

// SetInitialNodes pre-populates the node list with all pipeline nodes as pending.
func (a *AppModel) SetInitialNodes(nodes []NodeEntry) {
	a.nodeList = NewNodeListModel(nodes)
}

// Init implements tea.Model. Starts the tick timer for elapsed time updates.
func (a AppModel) Init() tea.Cmd {
	return tickCmd()
}

// tickCmd returns a command that sends a tickMsg after the tick interval.
func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Update implements tea.Model and handles all messages.
func (a AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tickMsg:
		return a, tickCmd()

	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.relayout()
		return a, nil

	case tea.KeyMsg:
		if a.modalKind != modalNone {
			return a.updateModal(msg)
		}
		switch msg.String() {
		case "q", "ctrl+c":
			a.quitting = true
			return a, tea.Quit
		}
		return a, nil

	case PipelineEventMsg:
		a.agentLog.AppendEvent(msg.Event)
		a.applyPipelineEvent(msg.Event)
		return a, nil

	case LLMActivityMsg:
		a.agentLog.AppendLine(msg.Summary)
		return a, nil

	case LLMTraceMsg:
		a.agentLog.AppendTrace(msg.Event, a.verboseTrace)
		return a, nil

	case AgentEventMsg:
		a.agentLog.AppendAgentEvent(msg.Event)
		return a, nil

	case GateChoiceMsg:
		a.modalKind = modalChoice
		a.modalTitle = msg.Prompt
		a.choiceModal = components.NewChoiceModel(msg.Prompt, msg.Choices, msg.DefaultChoice)
		a.choiceModal.SetWidth(a.modalContentWidth())
		a.activeMsgCh = msg.ReplyCh
		return a, nil

	case GateFreeformMsg:
		a.modalKind = modalFreeform
		a.modalTitle = msg.Prompt
		a.freeformModal = components.NewFreeformModel(msg.Prompt)
		a.freeformModal.SetWidth(a.modalContentWidth())
		a.activeMsgCh = msg.ReplyCh
		return a, a.freeformModal.Init()

	case PipelineDoneMsg:
		a.pipelineDone = true
		a.pipelineErr = msg.Err
		if msg.Err != nil {
			a.header.SetStatus(StatusFailed)
		} else {
			a.header.SetStatus(StatusCompleted)
		}
		return a, nil
	}

	return a, nil
}

// SetVerboseTrace controls whether raw provider trace events are rendered.
func (a *AppModel) SetVerboseTrace(verbose bool) {
	a.verboseTrace = verbose
}

// updateModal routes keyboard input to the active gate component.
func (a AppModel) updateModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch a.modalKind {
	case modalChoice:
		m, cmd := a.choiceModal.Update(msg)
		a.choiceModal = m.(components.ChoiceModel)
		if a.choiceModal.IsDone() {
			a.closeModal(a.choiceModal.Selected())
			if cmd != nil {
				cmd()
			}
			return a, nil
		}
		if a.choiceModal.IsCancelled() {
			a.closeModal("")
			return a, nil
		}
		return a, cmd

	case modalFreeform:
		m, cmd := a.freeformModal.Update(msg)
		a.freeformModal = m.(components.FreeformModel)
		if a.freeformModal.IsDone() {
			a.closeModal(a.freeformModal.Value())
			if cmd != nil {
				cmd()
			}
			return a, nil
		}
		if a.freeformModal.IsCancelled() {
			a.closeModal("")
			return a, nil
		}
		return a, cmd
	}
	return a, nil
}

// closeModal sends the reply back through the gate's channel and clears modal state.
func (a *AppModel) closeModal(value string) {
	if a.activeMsgCh != nil {
		a.activeMsgCh <- value
		a.activeMsgCh = nil
	}
	a.modalKind = modalNone
}

// applyPipelineEvent maps pipeline events onto node list status updates.
func (a *AppModel) applyPipelineEvent(evt pipeline.PipelineEvent) {
	switch evt.Type {
	case pipeline.EventPipelineStarted:
		// No node-level action needed.

	case pipeline.EventStageStarted:
		if evt.NodeID != "" {
			found := false
			for _, n := range a.nodeList.nodes {
				if n.ID == evt.NodeID {
					found = true
					break
				}
			}
			if !found {
				a.nodeList.AddNode(NodeEntry{ID: evt.NodeID, Label: evt.NodeID, Status: NodeRunning})
			} else {
				a.nodeList.SetNodeStatus(evt.NodeID, NodeRunning)
			}
		}

	case pipeline.EventStageCompleted:
		if evt.NodeID != "" {
			a.nodeList.SetNodeStatus(evt.NodeID, NodeDone)
		}

	case pipeline.EventStageFailed:
		if evt.NodeID != "" {
			a.nodeList.SetNodeStatus(evt.NodeID, NodeFailed)
		}
	}
}

// relayout recalculates component sizes after a terminal resize.
func (a *AppModel) relayout() {
	if a.width <= 0 || a.height <= 0 {
		return
	}

	// Account for outer frame borders (2 chars horizontal)
	innerWidth := a.width - 2
	if innerWidth < 1 {
		innerWidth = 1
	}

	a.header.SetWidth(innerWidth)

	// Reserve rows for header + border + status bar + outer frame
	contentHeight := a.height - headerHeight - statusBarHeight - 2
	if contentHeight < 1 {
		contentHeight = 1
	}

	nodeListW := innerWidth * nodeListWidthPct / 100
	if nodeListW < minNodeListWidth {
		nodeListW = minNodeListWidth
	}
	agentLogW := innerWidth - nodeListW - 1 // -1 for divider
	if agentLogW < minAgentLogWidth {
		agentLogW = minAgentLogWidth
	}

	a.nodeList.SetWidth(nodeListW)
	a.nodeList.SetHeight(contentHeight)
	a.agentLog.SetSize(agentLogW, contentHeight)
}

// View implements tea.Model.
func (a AppModel) View() string {
	if a.quitting {
		return ""
	}

	background := a.renderBackground()

	if a.modalKind == modalNone {
		return background
	}

	var innerView string
	switch a.modalKind {
	case modalChoice:
		innerView = a.choiceModal.View()
	case modalFreeform:
		innerView = a.freeformModal.View()
	}
	modal := components.NewModal("Human Gate", a.width, a.height)
	modal.SetContent(innerView)
	return modal.View(background)
}

// renderBackground builds the full control panel layout.
func (a AppModel) renderBackground() string {
	var sb strings.Builder

	// ── Header gauge cluster ──
	sb.WriteString(a.header.View())
	sb.WriteString("\n")

	// ── Horizontal separator between header and panes ──
	innerWidth := a.width - 2
	if innerWidth < 1 {
		innerWidth = a.width
	}
	separator := lipgloss.NewStyle().Foreground(colorBezel).Render(
		"╠" + strings.Repeat("═", innerWidth) + "╣",
	)
	sb.WriteString(separator)
	sb.WriteString("\n")

	// ── Split panes: signal panel (left) | activity log (right) ──
	nodePane := a.nodeList.View()
	logPane := a.agentLog.View()

	panes := lipgloss.JoinHorizontal(
		lipgloss.Top,
		paneDividerStyle.Render(nodePane),
		logPane,
	)
	sb.WriteString(panes)
	sb.WriteString("\n")

	// ── Status bar with track diagram ──
	sb.WriteString(a.statusBar())

	// Wrap everything in the outer instrument panel frame
	content := sb.String()
	return outerFrameStyle.Width(a.width - 2).Render(content)
}

// statusBar renders the bottom instrument panel strip with track diagram and progress.
func (a AppModel) statusBar() string {
	var parts []string

	// Track diagram: ●━●━◉━○━○
	diagram := a.nodeList.TrackDiagram()
	if diagram != "" {
		parts = append(parts, diagram)
	}

	// Progress summary: 6/10 ●6 ◉2 ○2
	progress := a.nodeList.ProgressSummary()
	if progress != "" {
		parts = append(parts, progress)
	}

	// Pipeline status
	if a.pipelineErr != nil {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorRed).Render("FAULT"))
	} else if a.pipelineDone {
		parts = append(parts, lipgloss.NewStyle().Foreground(colorGreen).Render("CLEAR"))
	}

	// Quit hint
	parts = append(parts, dimTextStyle.Render("q to exit"))

	content := strings.Join(parts, dimTextStyle.Render("  "))
	if a.width > 0 {
		return statusBarBaseStyle.Width(a.width - 4).Render(content)
	}
	return statusBarBaseStyle.Render(content)
}

// PipelineErr returns the error from the pipeline goroutine, if any.
func (a AppModel) PipelineErr() error {
	return a.pipelineErr
}

// IsPipelineDone reports whether the pipeline goroutine has finished.
func (a AppModel) IsPipelineDone() bool {
	return a.pipelineDone
}

// modalContentWidth returns the width available inside the modal chrome.
// DoubleBorder = 2 chars + Padding(1,2) = 4 chars = 6 total horizontal.
func (a AppModel) modalContentWidth() int {
	w := a.width - 6
	if w < 20 {
		w = 20
	}
	return w
}

// ─── Compile-time interface assertion ─────────────────────────────────────────

var _ tea.Model = AppModel{}
