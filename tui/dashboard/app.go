// ABOUTME: Main TUI dashboard app — composes header, node list, agent log, and modal overlay.
// ABOUTME: Implements tea.Model; runs in mode 2 (--tui flag) with pipeline in a goroutine.
package dashboard

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/tui/components"
)

// ─── Layout constants ─────────────────────────────────────────────────────────

const (
	nodeListWidthPct = 30 // percent of terminal width used for the node list pane
	minNodeListWidth = 22
	minAgentLogWidth = 30
)

// ─── Messages sent into the TUI loop ─────────────────────────────────────────

// PipelineEventMsg wraps a pipeline.PipelineEvent for delivery to the TUI.
type PipelineEventMsg struct{ Event pipeline.PipelineEvent }

// GateChoiceMsg requests a choice gate modal from the TUI.
// Sent by BubbleteaInterviewer (mode 2) via tea.Program.Send().
type GateChoiceMsg struct {
	Prompt        string
	Choices       []string
	DefaultChoice string
	ReplyCh       chan<- string
}

// GateFreeformMsg requests a freeform gate modal from the TUI.
// Sent by BubbleteaInterviewer (mode 2) via tea.Program.Send().
type GateFreeformMsg struct {
	Prompt  string
	ReplyCh chan<- string
}

// PipelineDoneMsg signals that the pipeline goroutine has finished.
type PipelineDoneMsg struct{ Err error }

// ─── Modal state ──────────────────────────────────────────────────────────────

// modalKind distinguishes the two gate types.
type modalKind int

const (
	modalNone     modalKind = iota
	modalChoice             // showing a ChoiceModel in a modal
	modalFreeform           // showing a FreeformModel in a modal
)

// ─── App model ────────────────────────────────────────────────────────────────

// AppModel is the root bubbletea model for mode 2 (full TUI dashboard).
// It composes a header, a node list pane, an agent log pane, and an optional
// modal overlay for human gate prompts.
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
	activeMsgCh   chan<- string // reply channel for the current gate

	// Lifecycle
	pipelineDone bool
	pipelineErr  error
	quitting     bool

	// Background viewport (rendered behind modal)
	bgViewport viewport.Model
}

var (
	appBorderStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, true, false, false).
			BorderForeground(lipgloss.Color("8"))

	appStatusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Faint(true)

	appErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")).
			Bold(true)
)

// NewAppModel constructs the dashboard AppModel.
// pipelineName is displayed in the header; tracker provides live token counts.
func NewAppModel(pipelineName string, tracker *llm.TokenTracker) AppModel {
	return AppModel{
		header:   NewHeaderModel(pipelineName, tracker),
		nodeList: NewNodeListModel(nil),
		agentLog: NewAgentLogModel(0, 0),
	}
}

// Init implements tea.Model. No initial commands needed.
func (a AppModel) Init() tea.Cmd { return nil }

// Update implements tea.Model and handles all messages.
func (a AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	// ── Terminal resize ──────────────────────────────────────────────────────
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.relayout()
		return a, nil

	// ── Keyboard input ───────────────────────────────────────────────────────
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

	// ── Pipeline event forwarded by TUIEventHandler ──────────────────────────
	case PipelineEventMsg:
		a.agentLog.AppendEvent(msg.Event)
		a.applyPipelineEvent(msg.Event)
		return a, nil

	// ── Human gate: choice ───────────────────────────────────────────────────
	case GateChoiceMsg:
		a.modalKind = modalChoice
		a.modalTitle = msg.Prompt
		a.choiceModal = components.NewChoiceModel(msg.Prompt, msg.Choices, msg.DefaultChoice)
		a.activeMsgCh = msg.ReplyCh
		return a, nil

	// ── Human gate: freeform ─────────────────────────────────────────────────
	case GateFreeformMsg:
		a.modalKind = modalFreeform
		a.modalTitle = msg.Prompt
		a.freeformModal = components.NewFreeformModel(msg.Prompt)
		a.activeMsgCh = msg.ReplyCh
		return a, a.freeformModal.Init()

	// ── Pipeline done ─────────────────────────────────────────────────────────
	case PipelineDoneMsg:
		a.pipelineDone = true
		a.pipelineErr = msg.Err
		return a, nil
	}

	return a, nil
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
				// drain the ChoiceDoneMsg command so it doesn't confuse the root loop
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
		// No node-level action needed — header already shows elapsed time.

	case pipeline.EventStageStarted:
		if evt.NodeID != "" {
			// Ensure node exists; add it if not already present.
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

	a.header.SetWidth(a.width)

	// Reserve 1 row for header, 1 for status bar.
	contentHeight := a.height - 2
	if contentHeight < 1 {
		contentHeight = 1
	}

	nodeListW := a.width * nodeListWidthPct / 100
	if nodeListW < minNodeListWidth {
		nodeListW = minNodeListWidth
	}
	agentLogW := a.width - nodeListW - 1 // -1 for border
	if agentLogW < minAgentLogWidth {
		agentLogW = minAgentLogWidth
	}

	a.nodeList.SetWidth(nodeListW)
	a.agentLog.SetSize(agentLogW, contentHeight)
}

// View implements tea.Model. Renders all components, with modal overlay if active.
func (a AppModel) View() string {
	if a.quitting {
		return ""
	}

	// Build the background (header + split panes + status bar)
	background := a.renderBackground()

	if a.modalKind == modalNone {
		return background
	}

	// Render modal overlay centered over the background
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

// renderBackground builds the dashboard layout without any modal overlay.
func (a AppModel) renderBackground() string {
	var sb strings.Builder

	// Header row
	sb.WriteString(a.header.View())
	sb.WriteString("\n")

	// Split panes: node list (left) | agent log (right)
	nodePane := a.nodeList.View()
	logPane := a.agentLog.View()

	// Render side by side; use lipgloss Join for clean alignment
	panes := lipgloss.JoinHorizontal(
		lipgloss.Top,
		appBorderStyle.Render(nodePane),
		logPane,
	)
	sb.WriteString(panes)
	sb.WriteString("\n")

	// Status bar
	sb.WriteString(a.statusBar())

	return sb.String()
}

// statusBar renders a one-line footer with pipeline status.
func (a AppModel) statusBar() string {
	if a.pipelineErr != nil {
		return appErrorStyle.Render(fmt.Sprintf("Pipeline failed: %v  (q to quit)", a.pipelineErr))
	}
	if a.pipelineDone {
		return appStatusStyle.Render("Pipeline completed successfully  (q to quit)")
	}
	return appStatusStyle.Render("Pipeline running…  (q to quit)")
}

// PipelineErr returns the error from the pipeline goroutine, if any.
// Used by cmd/tracker/main.go after the TUI program exits to surface failures.
func (a AppModel) PipelineErr() error {
	return a.pipelineErr
}

// IsPipelineDone reports whether the pipeline goroutine has finished.
func (a AppModel) IsPipelineDone() bool {
	return a.pipelineDone
}

// ─── Compile-time interface assertion ─────────────────────────────────────────

// Ensure AppModel satisfies tea.Model at compile time.
var _ tea.Model = AppModel{}
