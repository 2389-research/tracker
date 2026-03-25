// ABOUTME: Root Bubbletea App model — "Signal Cabin" dashboard orchestrator.
// ABOUTME: Owns layout, message routing to state store and child components, and keyboard handling.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Layout constants for the dashboard grid.
const (
	nodeListWidthFrac = 4 // node list gets 1/nodeListWidthFrac of terminal width
	headerRows        = 1 // header occupies one row
	statusBarRows     = 1 // status bar occupies one row
	minContentHeight  = 4 // minimum rows for the main content area
)

// layout holds mutable terminal dimensions shared via pointer.
type layout struct {
	width  int
	height int
}

// AppModel is the root Bubbletea model composing all TUI components.
// All fields are pointers so mutations through value receivers propagate
// correctly (required by tea.Model's value-receiver interface).
type AppModel struct {
	store    *StateStore
	header   *Header
	statusB  *StatusBar
	nodeList *NodeList
	agentLog *AgentLog
	modal    *Modal
	thinking *ThinkingTracker
	lay      *layout
}

// NewAppModel creates a fully-wired App with all child components.
func NewAppModel(store *StateStore, pipelineName, runID string) *AppModel {
	thinking := NewThinkingTracker()
	return &AppModel{
		store:    store,
		header:   NewHeader(store, pipelineName, runID),
		statusB:  NewStatusBar(store),
		nodeList: NewNodeList(store, thinking, 10),
		agentLog: NewAgentLog(store, thinking, 10),
		modal:    NewModal(80, 24),
		thinking: thinking,
		lay:      &layout{},
	}
}

// Init returns the initial batch of tick commands.
func (a AppModel) Init() tea.Cmd {
	return tea.Batch(thinkingTickCmd(), headerTickCmd())
}

// Update routes messages through global keys, modal, state store, and child components.
func (a AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Handle global keys first.
	if km, ok := msg.(tea.KeyMsg); ok {
		return a.handleKeyMsg(km)
	}

	// Handle window resize.
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		a.lay.width = ws.Width
		a.lay.height = ws.Height
		a.relayout()
		return a, nil
	}

	// Handle gate/modal messages.
	if model, cmd, handled := a.handleModalMsg(msg); handled {
		return model, cmd
	}

	// Handle tick messages.
	if model, cmd, handled := a.handleTickMsg(msg); handled {
		return model, cmd
	}

	// Apply message to state store and route to thinking tracker and children.
	a.store.Apply(msg)
	a.routeThinkingMsg(msg)

	var cmds []tea.Cmd
	if cmd := a.nodeList.Update(msg); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := a.agentLog.Update(msg); cmd != nil {
		cmds = append(cmds, cmd)
	}

	return a, tea.Batch(cmds...)
}

// handleKeyMsg processes keyboard input, returning early for quit and modal keys.
func (a AppModel) handleKeyMsg(km tea.KeyMsg) (tea.Model, tea.Cmd) {
	if km.Type == tea.KeyCtrlC {
		return a, tea.Quit
	}
	if a.modal.Visible() {
		cmd := a.modal.Update(km)
		return a, cmd
	}
	switch {
	case km.Type == tea.KeyRunes && string(km.Runes) == "q":
		return a, tea.Quit
	case km.Type == tea.KeyCtrlO:
		a.agentLog.Update(MsgToggleExpand{})
		return a, nil
	}
	return a, nil
}

// handleModalMsg handles gate choice/freeform, dismiss, and pipeline done messages.
// Returns (model, cmd, true) if the message was handled.
func (a AppModel) handleModalMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch m := msg.(type) {
	case MsgGateChoice:
		a.modal.Show(NewChoiceContent(m.Prompt, m.Options, m.ReplyCh))
		return a, nil, true
	case MsgGateFreeform:
		// Use split-pane review for long prompts (plan approval, etc.)
		if strings.Count(m.Prompt, "\n") > longPromptThreshold || len(m.Prompt) > 2000 {
			a.modal.Show(NewReviewContent(m.Prompt, m.ReplyCh, a.lay.width, a.lay.height))
		} else {
			a.modal.Show(NewFreeformContent(m.Prompt, m.ReplyCh))
		}
		return a, nil, true
	case MsgModalDismiss:
		a.modal.Hide()
		return a, nil, true
	case MsgPipelineDone:
		return a, tea.Quit, true
	}
	return a, nil, false
}

// handleTickMsg handles thinking and header tick messages.
// Returns (model, cmd, true) if the message was handled.
func (a AppModel) handleTickMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg.(type) {
	case MsgThinkingTick:
		a.thinking.Tick()
		return a, thinkingTickCmd(), true
	case MsgHeaderTick:
		a.header.Update(msg)
		return a, headerTickCmd(), true
	}
	return a, nil, false
}

// routeThinkingMsg routes thinking and tool state changes to the tracker.
func (a *AppModel) routeThinkingMsg(msg tea.Msg) {
	switch m := msg.(type) {
	case MsgThinkingStarted:
		if nodeID := a.resolveNodeID(m.NodeID); nodeID != "" {
			a.thinking.Start(nodeID)
			a.agentLog.SetFocusedNode(nodeID)
		}
	case MsgThinkingStopped:
		if nodeID := a.resolveNodeID(m.NodeID); nodeID != "" {
			a.thinking.Stop(nodeID)
		}
	case MsgToolCallStart:
		if nodeID := a.resolveNodeID(m.NodeID); nodeID != "" {
			a.thinking.StartTool(nodeID, m.ToolName)
		}
	case MsgToolCallEnd:
		if nodeID := a.resolveNodeID(m.NodeID); nodeID != "" {
			a.thinking.StopTool(nodeID)
		}
	}
}

// View composes the dashboard layout: header, content (node list + agent log), status bar.
func (a AppModel) View() string {
	if a.lay.width == 0 || a.lay.height == 0 {
		return "initializing..."
	}

	// Render header.
	headerView := a.header.View()

	// Calculate content area dimensions.
	contentHeight := a.lay.height - headerRows - statusBarRows
	if contentHeight < minContentHeight {
		contentHeight = minContentHeight
	}

	nodeWidth := a.lay.width / nodeListWidthFrac
	if nodeWidth < 1 {
		nodeWidth = 1
	}
	logWidth := a.lay.width - nodeWidth
	if logWidth < 1 {
		logWidth = 1
	}

	// Render node list panel.
	nodeView := a.nodeList.View()
	nodePanel := lipgloss.NewStyle().
		Width(nodeWidth).
		MaxWidth(nodeWidth).
		Height(contentHeight).
		MaxHeight(contentHeight).
		PaddingRight(1).
		Render(nodeView)

	// Thin vertical separator between panels.
	sepStyle := lipgloss.NewStyle().Foreground(ColorBezel)
	var sepLines []string
	for i := 0; i < contentHeight; i++ {
		sepLines = append(sepLines, sepStyle.Render("│"))
	}
	separator := strings.Join(sepLines, "\n")

	// Render agent log panel.
	logView := a.agentLog.View()
	logPanel := lipgloss.NewStyle().
		Width(logWidth - 1).
		MaxWidth(logWidth - 1).
		Height(contentHeight).
		MaxHeight(contentHeight).
		PaddingLeft(1).
		Render(logView)

	// Join content panels horizontally.
	content := lipgloss.JoinHorizontal(lipgloss.Top, nodePanel, separator, logPanel)

	// Render status bar.
	statusView := a.statusB.View()

	// Stack vertically: header, content, status bar.
	dashboard := lipgloss.JoinVertical(lipgloss.Left, headerView, content, statusView)

	// Overlay modal if visible.
	if a.modal.Visible() {
		return a.modal.View(dashboard)
	}

	return dashboard
}

// relayout recalculates sizes for all child components after a terminal resize.
func (a *AppModel) relayout() {
	a.header.SetWidth(a.lay.width)
	a.statusB.SetWidth(a.lay.width)
	a.modal.SetSize(a.lay.width, a.lay.height)

	contentHeight := a.lay.height - headerRows - statusBarRows
	if contentHeight < minContentHeight {
		contentHeight = minContentHeight
	}

	nodeWidth := a.lay.width / nodeListWidthFrac
	logWidth := a.lay.width - nodeWidth - 1 // -1 for separator column

	a.nodeList.SetSize(nodeWidth, contentHeight)
	a.agentLog.SetSize(logWidth, contentHeight)
}

// ActiveNode returns the ID of the first running node, for focusing the log.
func (a *AppModel) ActiveNode() string {
	for _, n := range a.store.Nodes() {
		if a.store.NodeStatus(n.ID) == NodeRunning {
			return n.ID
		}
	}
	return ""
}

// resolveNodeID returns the given nodeID if non-empty, otherwise falls back to
// the currently focused node or the first running node.
func (a *AppModel) resolveNodeID(nodeID string) string {
	if nodeID != "" {
		return nodeID
	}
	return a.ActiveNode()
}

// SetVerboseTrace enables or disables verbose LLM trace output in the agent log.
func (a *AppModel) SetVerboseTrace(v bool) {
	a.agentLog.SetVerboseTrace(v)
}

// SetInitialNodes configures the ordered node list via the state store.
func (a *AppModel) SetInitialNodes(entries []NodeEntry) {
	a.store.SetNodes(entries)
}

// String implements fmt.Stringer for debug purposes.
func (a AppModel) String() string {
	done, total := a.store.Progress()
	return fmt.Sprintf("AppModel{%d/%d nodes, %dx%d}", done, total, a.lay.width, a.lay.height)
}
