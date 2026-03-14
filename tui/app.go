// ABOUTME: Root Bubbletea App model — "Signal Cabin" dashboard orchestrator.
// ABOUTME: Owns layout, message routing to state store and child components, and keyboard handling.
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Layout constants for the dashboard grid.
const (
	nodeListWidthFrac = 4  // node list gets 1/nodeListWidthFrac of terminal width
	headerRows        = 1  // header occupies one row
	statusBarRows     = 1  // status bar occupies one row
	minContentHeight  = 4  // minimum rows for the main content area
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
	var cmds []tea.Cmd

	// Handle global keys first.
	if km, ok := msg.(tea.KeyMsg); ok {
		// If modal is visible, route keys to it instead of global handling.
		if a.modal.Visible() {
			cmd := a.modal.Update(km)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return a, tea.Batch(cmds...)
		}

		switch {
		case km.Type == tea.KeyCtrlC:
			return a, tea.Quit
		case km.Type == tea.KeyRunes && string(km.Runes) == "q":
			return a, tea.Quit
		case km.Type == tea.KeyCtrlO:
			// Toggle expand in agent log.
			a.agentLog.Update(MsgToggleExpand{})
			return a, nil
		}
	}

	// Handle window resize.
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		a.lay.width = ws.Width
		a.lay.height = ws.Height
		a.relayout()
		return a, nil
	}

	// Handle gate messages — show modal.
	switch m := msg.(type) {
	case MsgGateChoice:
		content := NewChoiceContent(m.Prompt, m.Options, m.ReplyCh)
		a.modal.Show(content)
		return a, nil
	case MsgGateFreeform:
		content := NewFreeformContent(m.Prompt, m.ReplyCh)
		a.modal.Show(content)
		return a, nil
	}

	// Handle tick messages — re-schedule ticks.
	switch msg.(type) {
	case MsgThinkingTick:
		a.thinking.Tick()
		cmds = append(cmds, thinkingTickCmd())
	case MsgHeaderTick:
		a.header.Update(msg)
		cmds = append(cmds, headerTickCmd())
		return a, tea.Batch(cmds...)
	}

	// Apply message to state store.
	a.store.Apply(msg)

	// Route thinking state changes to the tracker.
	switch m := msg.(type) {
	case MsgThinkingStarted:
		a.thinking.Start(m.NodeID)
		a.agentLog.SetFocusedNode(m.NodeID)
	case MsgThinkingStopped:
		a.thinking.Stop(m.NodeID)
	}

	// Forward to child components.
	if cmd := a.nodeList.Update(msg); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := a.agentLog.Update(msg); cmd != nil {
		cmds = append(cmds, cmd)
	}

	return a, tea.Batch(cmds...)
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
		Height(contentHeight).
		BorderRight(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(ColorBezel).
		Render(nodeView)

	// Render agent log panel.
	logView := a.agentLog.View()
	logPanel := lipgloss.NewStyle().
		Width(logWidth).
		Height(contentHeight).
		PaddingLeft(1).
		Render(logView)

	// Join content panels horizontally.
	content := lipgloss.JoinHorizontal(lipgloss.Top, nodePanel, logPanel)

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
	logWidth := a.lay.width - nodeWidth

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

// String implements fmt.Stringer for debug purposes.
func (a AppModel) String() string {
	done, total := a.store.Progress()
	return fmt.Sprintf("AppModel{%d/%d nodes, %dx%d}", done, total, a.lay.width, a.lay.height)
}
