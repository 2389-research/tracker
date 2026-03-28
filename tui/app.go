// ABOUTME: Root Bubbletea App model — "Signal Cabin" dashboard orchestrator.
// ABOUTME: Owns layout, message routing to state store and child components, and keyboard handling.
package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

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

// layout holds mutable terminal dimensions and mode flags shared via pointer.
// All fields that are value types on AppModel must live here to survive
// bubbletea's value-receiver copy semantics.
type layout struct {
	width       int
	height      int
	zenMode     bool   // hide sidebar, agent log gets full width
	focusedNode string // node ID for drill-down (empty = no focus)
}

// AppModel is the root Bubbletea model composing all TUI components.
// All fields are pointers so mutations through value receivers propagate
// correctly (required by tea.Model's value-receiver interface).
type AppModel struct {
	store    *StateStore
	header   *Header
	statusB  *StatusBar
	nodeList *NodeList
	history  *HistoryTrail
	agentLog *AgentLog
	modal    *Modal
	thinking *ThinkingTracker
	lay      *layout
}

// NewAppModel creates a fully-wired App with all child components.
func NewAppModel(store *StateStore, pipelineName, runID string) *AppModel {
	thinking := NewThinkingTracker()
	al := NewAgentLog(store, thinking, 10)
	return &AppModel{
		store:    store,
		header:   NewHeader(store, pipelineName, runID),
		statusB:  NewStatusBar(store, al),
		nodeList: NewNodeList(store, thinking, 10),
		history:  NewHistoryTrail(store),
		agentLog: al,
		modal:    NewModal(80, 24),
		thinking: thinking,
		lay:      &layout{},
	}
}

// Header returns the header component for configuration.
func (a *AppModel) Header() *Header { return a.header }

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

	// Handle flash messages.
	switch msg.(type) {
	case MsgStatusFlash:
		a.statusB.SetFlash(msg.(MsgStatusFlash).Text)
		return a, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
			return MsgStatusFlashClear{}
		})
	case MsgStatusFlashClear:
		a.statusB.ClearFlash()
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
		a.modal.CancelAndHide()
		return a, tea.Quit
	}
	if a.modal.Visible() {
		cmd := a.modal.Update(km)
		return a, cmd
	}

	// Search bar intercepts keys when active.
	if a.agentLog.Search().Active() {
		return a.handleSearchKey(km)
	}

	return a.handleDashboardKey(km)
}

// handleSearchKey routes keys while the search bar is active.
func (a AppModel) handleSearchKey(km tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmd, consumed := a.agentLog.Search().Update(km)
	if consumed {
		return a, cmd
	}
	return a, nil
}

// handleDashboardKey handles all dashboard-level keyboard shortcuts.
func (a AppModel) handleDashboardKey(km tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Rune-based shortcuts.
	if km.Type == tea.KeyRunes {
		return a.handleDashboardRune(string(km.Runes))
	}
	// Special keys.
	switch km.Type {
	case tea.KeyCtrlO:
		a.agentLog.Update(MsgToggleExpand{})
		return a, nil
	case tea.KeyUp:
		a.nodeList.MoveUp()
		return a, nil
	case tea.KeyDown:
		a.nodeList.MoveDown()
		return a, nil
	case tea.KeyEnter:
		if nodeID := a.nodeList.SelectedNodeID(); nodeID != "" {
			a.lay.focusedNode = nodeID
			a.agentLog.Update(MsgFocusNode{NodeID: nodeID})
		}
		return a, nil
	case tea.KeyEscape:
		if a.lay.focusedNode != "" {
			a.lay.focusedNode = ""
			a.agentLog.Update(MsgClearFocus{})
		}
		return a, nil
	}
	return a, nil
}

// handleDashboardRune handles single-character shortcuts.
func (a AppModel) handleDashboardRune(r string) (tea.Model, tea.Cmd) {
	switch r {
	case "q":
		return a, tea.Quit
	case "v":
		a.agentLog.Update(MsgCycleVerbosity{})
		return a, nil
	case "z":
		a.lay.zenMode = !a.lay.zenMode
		a.relayout()
		return a, nil
	case "?":
		a.modal.Show(NewHelpContent())
		return a, nil
	case "/":
		a.agentLog.Search().Activate()
		return a, nil
	case "n":
		a.agentLog.Search().NextMatch()
		return a, nil
	case "N":
		a.agentLog.Search().PrevMatch()
		return a, nil
	case "y":
		return a, a.copyToClipboard()
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
		content := buildFreeformContent(m, a.lay.width, a.lay.height)
		a.modal.Show(content)
		return a, nil, true
	case MsgGateAutopilot:
		a.modal.Show(NewAutopilotContent(m.Prompt, m.Decision, m.ReplyCh))
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

	var content string

	if a.lay.zenMode {
		// Zen mode: agent log gets full width, no sidebar.
		logWidth := a.lay.width
		a.agentLog.SetSize(logWidth, contentHeight)
		logView := a.agentLog.View()
		content = lipgloss.NewStyle().
			Width(logWidth).
			MaxWidth(logWidth).
			Height(contentHeight).
			MaxHeight(contentHeight).
			Render(logView)
	} else {
		nodeWidth := a.lay.width / nodeListWidthFrac
		if nodeWidth < 1 {
			nodeWidth = 1
		}
		logWidth := a.lay.width - nodeWidth
		if logWidth < 1 {
			logWidth = 1
		}

		// Render sidebar: node list (top) + history trail (bottom).
		nodeHeight := contentHeight * 3 / 5
		histHeight := contentHeight - nodeHeight
		a.nodeList.SetSize(nodeWidth, nodeHeight-1)
		a.history.SetSize(nodeWidth, histHeight)

		nodeView := a.nodeList.View()
		histView := a.history.View()
		sidebar := lipgloss.JoinVertical(lipgloss.Left, nodeView, histView)
		nodePanel := lipgloss.NewStyle().
			Width(nodeWidth).
			MaxWidth(nodeWidth).
			Height(contentHeight).
			MaxHeight(contentHeight).
			PaddingRight(1).
			Render(sidebar)

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
		content = lipgloss.JoinHorizontal(lipgloss.Top, nodePanel, separator, logPanel)
	}

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

	if a.lay.zenMode {
		a.agentLog.SetSize(a.lay.width, contentHeight)
	} else {
		nodeWidth := a.lay.width / nodeListWidthFrac
		logWidth := a.lay.width - nodeWidth - 1 // -1 for separator column
		a.nodeList.SetSize(nodeWidth, contentHeight)
		a.agentLog.SetSize(logWidth, contentHeight)
	}
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

// buildFreeformContent selects the best content type for a freeform gate:
// - Long prompt → ReviewContent (scrollable split-pane), labels shown in hint
// - Labels + short prompt → HybridContent (radio + freeform)
// - Short prompt, no labels → FreeformContent (simple modal)
func buildFreeformContent(m MsgGateFreeform, width, height int) ModalContent {
	// Labels always get the hybrid radio view — the user needs to see their options.
	// The plan content (after ---) stays in the activity log where it's scrollable.
	if len(m.Labels) > 0 {
		label := m.Prompt
		context := ""
		if idx := strings.Index(label, "\n\n---\n"); idx >= 0 {
			context = label[idx+6:]
			label = label[:idx]
		}
		// If there's substantial context (agent output, error info),
		// show it in a scrollable review pane with radio labels below.
		if len(context) > 200 || strings.Count(context, "\n") > 5 {
			return NewReviewHybridContent(label, context, m.Labels, m.Default, m.ReplyCh, width, height)
		}
		// Short or no context — simple radio modal.
		if context != "" {
			label = label + "\n\n" + Styles.DimText.Render(truncateContext(context, 5))
		}
		return NewHybridContent(label, m.Labels, m.Default, m.ReplyCh)
	}

	// Long prompts without labels get the review pane.
	isLong := strings.Count(m.Prompt, "\n") > longPromptThreshold || len(m.Prompt) > 2000
	if isLong {
		return NewReviewContent(m.Prompt, m.ReplyCh, width, height)
	}

	return NewFreeformContent(m.Prompt, m.ReplyCh)
}

// truncateContext returns the first N lines of context text.
func truncateContext(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	return strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n... (%d more lines in activity log)", len(lines)-maxLines)
}

// copyToClipboard copies the visible agent log text to the system clipboard.
func (a AppModel) copyToClipboard() tea.Cmd {
	text := a.agentLog.VisibleText()
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("pbcopy")
		case "linux":
			cmd = exec.Command("xclip", "-selection", "clipboard")
		default:
			return MsgStatusFlash{Text: "Clipboard not supported"}
		}
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			return MsgStatusFlash{Text: fmt.Sprintf("Copy failed: %s", err)}
		}
		return MsgStatusFlash{Text: "Copied!"}
	}
}

// String implements fmt.Stringer for debug purposes.
func (a AppModel) String() string {
	done, total := a.store.Progress()
	return fmt.Sprintf("AppModel{%d/%d nodes, %dx%d}", done, total, a.lay.width, a.lay.height)
}
