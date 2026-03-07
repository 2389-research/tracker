// ABOUTME: Main TUI dashboard app — composes header, node list, agent log, and modal overlay.
// ABOUTME: Implements tea.Model; runs in mode 2 (--tui flag) with pipeline in a goroutine.
package dashboard

import (
	"fmt"
	"strings"
	"time"

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
	headerHeight     = 3 // two content lines + bottom border
	statusBarHeight  = 1
	tickInterval     = time.Second
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

// tickMsg is sent periodically to update the elapsed time display.
type tickMsg time.Time

// ─── Modal state ──────────────────────────────────────────────────────────────

// modalKind distinguishes the two gate types.
type modalKind int

const (
	modalNone     modalKind = iota
	modalChoice             // showing a ChoiceModel in a modal
	modalFreeform           // showing a FreeformModel in a modal
)

// ─── Styles ───────────────────────────────────────────────────────────────────

var (
	paneVerticalBorder = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), false, true, false, false).
				BorderForeground(lipgloss.Color("8"))

	headerBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), false, false, true, false).
				BorderForeground(lipgloss.Color("8"))

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("235")).
			Padding(0, 1)

	statusBarDimStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("8"))

	statusBarErrorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("9")).
				Bold(true)

	statusBarSuccessStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("10"))

	statusBarProgressStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("11"))
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

// NewAppModel constructs the dashboard AppModel.
// pipelineName is displayed in the header; tracker provides live token counts.
func NewAppModel(pipelineName string, tracker *llm.TokenTracker) AppModel {
	return AppModel{
		header:   NewHeaderModel(pipelineName, tracker),
		nodeList: NewNodeListModel(nil),
		agentLog: NewAgentLogModel(0, 0),
	}
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

	// ── Tick for elapsed time updates ────────────────────────────────────────
	case tickMsg:
		// Just re-render (header reads time.Since on each View call)
		return a, tickCmd()

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
		if msg.Err != nil {
			a.header.SetStatus(StatusFailed)
		} else {
			a.header.SetStatus(StatusCompleted)
		}
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

	// Reserve rows for header (2 lines + border) and status bar
	contentHeight := a.height - headerHeight - statusBarHeight
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

	// Header with bottom border separator
	headerContent := a.header.View()
	sb.WriteString(headerBorderStyle.Width(a.width).Render(headerContent))
	sb.WriteString("\n")

	// Split panes: node list (left) | agent log (right)
	nodePane := a.nodeList.View()
	logPane := a.agentLog.View()

	// Render side by side with vertical border between them
	panes := lipgloss.JoinHorizontal(
		lipgloss.Top,
		paneVerticalBorder.Render(nodePane),
		logPane,
	)
	sb.WriteString(panes)
	sb.WriteString("\n")

	// Status bar
	sb.WriteString(a.statusBar())

	return sb.String()
}

// statusBar renders a one-line footer with pipeline status and node progress.
func (a AppModel) statusBar() string {
	var parts []string

	// Pipeline status indicator
	if a.pipelineErr != nil {
		parts = append(parts, statusBarErrorStyle.Render(fmt.Sprintf("Pipeline failed: %v", a.pipelineErr)))
	} else if a.pipelineDone {
		parts = append(parts, statusBarSuccessStyle.Render("Pipeline completed"))
	} else {
		parts = append(parts, statusBarProgressStyle.Render("Pipeline running…"))
	}

	// Node progress
	pending, running, done, failed := a.nodeList.Counts()
	total := pending + running + done + failed
	if total > 0 {
		progress := fmt.Sprintf("%d/%d nodes complete", done, total)
		if running > 0 {
			progress += fmt.Sprintf("  %d running", running)
		}
		if failed > 0 {
			progress += fmt.Sprintf("  %d failed", failed)
		}
		parts = append(parts, statusBarDimStyle.Render(progress))
	}

	// Quit hint
	parts = append(parts, statusBarDimStyle.Render("q to quit"))

	content := strings.Join(parts, statusBarDimStyle.Render("  │  "))
	if a.width > 0 {
		return statusBarStyle.Width(a.width).Render(content)
	}
	return statusBarStyle.Render(content)
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
