# TUI Overhaul + Thinking Indicators Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Clean-room rewrite of the tracker TUI with robust internals and LLM thinking/progress indicators, preserving the same visual design and external interface.

**Architecture:** Single `tui/` package with message-driven state management. All pipeline/agent/LLM events flow through an adapter layer into typed Bubbletea messages. Components are self-contained models that read from a central StateStore. ThinkingTracker drives lamp animations and elapsed-time indicators.

**Tech Stack:** Go, Bubbletea (tea.Model), Lipgloss (styling), Bubbles (viewport/textinput)

**Spec:** `docs/superpowers/specs/2026-03-13-tui-overhaul-design.md`

---

## Pre-Work: Understanding the External Interface

Before writing any code, the implementer must understand what `cmd/tracker/main.go` expects from the TUI package. Read these files:

- `cmd/tracker/main.go:227-367` — `runTUI()` function that creates AppModel, tea.Program, wires events
- `cmd/tracker/main.go:100-222` — `run()` function that uses Mode 1 interviewer
- `tui/interviewer.go` — `BubbleteaInterviewer` implementing `handlers.Interviewer` and `handlers.FreeformInterviewer`
- `tui/events.go` — `TUIEventHandler` implementing `pipeline.PipelineEventHandler`
- `pipeline/handlers/interviewer.go` — `Interviewer` and `FreeformInterviewer` interfaces

The new TUI must export the same types and functions that `main.go` currently imports.

---

## Task 0: Clear Old Files and Prepare

Before creating any new files, move the old TUI code out of the way to avoid compilation conflicts (new `messages.go`, `interviewer.go`, `events.go` would conflict with existing files of the same name).

**Files:**
- Delete: `tui/messages.go`, `tui/events.go`, `tui/interviewer.go`
- Delete: `tui/dashboard/`, `tui/components/`, `tui/render/`

- [ ] **Step 1: Back up old files to a temporary branch**

```bash
git checkout -b tui-old-backup
git checkout main
```

- [ ] **Step 2: Delete old TUI files**

```bash
rm tui/messages.go tui/events.go tui/interviewer.go
rm -rf tui/dashboard/ tui/components/ tui/render/
```

- [ ] **Step 3: Temporarily comment out TUI references in main.go**

Comment out the `runTUI()` function body and any imports from the deleted packages. This keeps main.go parseable while we rebuild. We'll restore it in Task 14.

- [ ] **Step 4: Verify the project compiles (with TUI disabled)**

Run: `cd /Users/harper/Public/src/2389/tracker && go build ./cmd/tracker/`
Expected: SUCCESS (with runTUI commented out)

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor(tui): remove old TUI files for clean-room rewrite"
```

---

## Chunk 1: Foundation

### Task 1: Messages

All typed message constants used by every other component. Must be created first since everything depends on these types.

**Files:**
- Create: `tui/messages.go`

- [ ] **Step 1: Write the test for message types**

Create `tui/messages_test.go`:

```go
// ABOUTME: Tests that all TUI message types are properly defined and satisfy tea.Msg.
// ABOUTME: Validates message field access patterns used throughout the TUI.
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPipelineMessagesAreTeaMsgs(t *testing.T) {
	msgs := []tea.Msg{
		MsgNodeStarted{NodeID: "n1"},
		MsgNodeCompleted{NodeID: "n1", Outcome: "success"},
		MsgNodeFailed{NodeID: "n1", Error: "boom"},
		MsgPipelineCompleted{},
		MsgPipelineFailed{Error: "fatal"},
	}
	for i, msg := range msgs {
		if msg == nil {
			t.Errorf("message %d is nil", i)
		}
	}
}

func TestAgentMessagesAreTeaMsgs(t *testing.T) {
	msgs := []tea.Msg{
		MsgThinkingStarted{NodeID: "n1"},
		MsgThinkingStopped{NodeID: "n1"},
		MsgTextChunk{NodeID: "n1", Text: "hello"},
		MsgReasoningChunk{NodeID: "n1", Text: "thinking"},
		MsgToolCallStart{NodeID: "n1", ToolName: "exec"},
		MsgToolCallEnd{NodeID: "n1", ToolName: "exec", Output: "ok"},
		MsgAgentError{NodeID: "n1", Error: "fail"},
	}
	for i, msg := range msgs {
		if msg == nil {
			t.Errorf("message %d is nil", i)
		}
	}
}

func TestLLMMessagesAreTeaMsgs(t *testing.T) {
	msgs := []tea.Msg{
		MsgLLMRequestStart{NodeID: "n1", Provider: "anthropic", Model: "claude-sonnet-4-6"},
		MsgLLMFinish{NodeID: "n1"},
		MsgLLMProviderRaw{NodeID: "n1", Data: "raw"},
	}
	for i, msg := range msgs {
		if msg == nil {
			t.Errorf("message %d is nil", i)
		}
	}
}

func TestGateMessagesHaveReplyCh(t *testing.T) {
	ch := make(chan string, 1)
	choice := MsgGateChoice{
		NodeID:  "n1",
		Prompt:  "Pick one",
		Options: []string{"a", "b"},
		ReplyCh: ch,
	}
	if choice.ReplyCh == nil {
		t.Error("expected non-nil ReplyCh")
	}

	freeform := MsgGateFreeform{
		NodeID:  "n1",
		Prompt:  "Enter value",
		ReplyCh: ch,
	}
	if freeform.ReplyCh == nil {
		t.Error("expected non-nil ReplyCh")
	}
}

func TestUIMessagesAreTeaMsgs(t *testing.T) {
	msgs := []tea.Msg{
		MsgThinkingTick{},
		MsgHeaderTick{},
		MsgToggleExpand{},
	}
	for i, msg := range msgs {
		if msg == nil {
			t.Errorf("message %d is nil", i)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestPipelineMessages -v`
Expected: FAIL — `MsgNodeStarted` undefined

- [ ] **Step 3: Write messages.go**

Create `tui/messages.go`:

```go
// ABOUTME: All typed Bubbletea message constants for the TUI.
// ABOUTME: Components communicate exclusively through these messages — no string comparisons.
package tui

// Pipeline messages — mapped from pipeline.PipelineEvent by the adapter.
type MsgNodeStarted struct{ NodeID string }
type MsgNodeCompleted struct {
	NodeID  string
	Outcome string
}
type MsgNodeFailed struct {
	NodeID string
	Error  string
}
type MsgPipelineCompleted struct{}
type MsgPipelineFailed struct{ Error string }

// Agent messages — mapped from agent.Event by the adapter.
type MsgThinkingStarted struct{ NodeID string }
type MsgThinkingStopped struct{ NodeID string }
type MsgTextChunk struct {
	NodeID string
	Text   string
}
type MsgReasoningChunk struct {
	NodeID string
	Text   string
}
type MsgToolCallStart struct {
	NodeID   string
	ToolName string
}
type MsgToolCallEnd struct {
	NodeID   string
	ToolName string
	Output   string
	Error    string
}
type MsgAgentError struct {
	NodeID string
	Error  string
}

// LLM trace messages — mapped from llm.TraceEvent by the adapter.
type MsgLLMRequestStart struct {
	NodeID   string
	Provider string
	Model    string
}
type MsgLLMFinish struct{ NodeID string }
type MsgLLMProviderRaw struct {
	NodeID string
	Data   string
}

// Gate messages — sent by the Interviewer when the pipeline hits a gate node.
type MsgGateChoice struct {
	NodeID  string
	Prompt  string
	Options []string
	ReplyCh chan<- string
}
type MsgGateFreeform struct {
	NodeID  string
	Prompt  string
	ReplyCh chan<- string
}

// UI messages — internal TUI control flow.
type MsgThinkingTick struct{} // 150ms tick for lamp animation + thinking elapsed time
type MsgHeaderTick struct{}   // 1s tick for header elapsed time
type MsgToggleExpand struct{}  // ctrl+o expand/collapse agent log

// Pipeline done — sent by main.go's pipeline goroutine when engine finishes.
type MsgPipelineDone struct{ Err error }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run "TestPipelineMessages|TestAgentMessages|TestLLMMessages|TestGateMessages|TestUIMessages" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tui/messages.go tui/messages_test.go
git commit -m "feat(tui): add typed message constants for TUI rewrite"
```

---

### Task 2: Style Registry

Shared styles, colors, and signal lamp characters. Every visual component imports from here.

**Files:**
- Create: `tui/styles.go`

- [ ] **Step 1: Write the test**

Create `tui/styles_test.go`:

```go
// ABOUTME: Tests that the style registry exposes all required lamp characters and colors.
// ABOUTME: Ensures no empty strings for visual constants.
package tui

import "testing"

func TestLampCharacters(t *testing.T) {
	lamps := []struct {
		name string
		char string
	}{
		{"Running", LampRunning},
		{"Done", LampDone},
		{"Pending", LampPending},
		{"Failed", LampFailed},
	}
	for _, l := range lamps {
		if l.char == "" {
			t.Errorf("lamp %s is empty", l.name)
		}
	}
}

func TestThinkingFrames(t *testing.T) {
	if len(ThinkingFrames) != 4 {
		t.Errorf("expected 4 thinking frames, got %d", len(ThinkingFrames))
	}
	for i, f := range ThinkingFrames {
		if f == "" {
			t.Errorf("thinking frame %d is empty", i)
		}
	}
}

func TestStylesNotZero(t *testing.T) {
	// Verify key styles are initialized (lipgloss styles have non-empty renders)
	s := Styles.NodeName.Render("test")
	if s == "" {
		t.Error("NodeName style renders empty")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestLampCharacters -v`
Expected: FAIL — `LampRunning` undefined

- [ ] **Step 3: Write styles.go**

Read the existing `tui/dashboard/style.go` for the current color palette and lamp characters. Create `tui/styles.go` with all constants consolidated:

```go
// ABOUTME: Shared style registry for the TUI — all colors, borders, lamp characters, and lipgloss styles.
// ABOUTME: Every visual component imports from here instead of defining its own styles.
package tui

import "github.com/charmbracelet/lipgloss"

// Signal lamp characters.
const (
	LampRunning = "◉"
	LampDone    = "●"
	LampPending = "○"
	LampFailed  = "✖"
)

// ThinkingFrames cycles during LLM thinking state (150ms per frame).
var ThinkingFrames = [4]string{"◐", "◓", "◑", "◒"}

// Colors — read existing tui/dashboard/style.go and replicate the palette.
var (
	ColorRunning = lipgloss.Color("#FFCC00")
	ColorDone    = lipgloss.Color("#00CC66")
	ColorFailed  = lipgloss.Color("#FF3333")
	ColorPending = lipgloss.Color("#666666")
	ColorMuted   = lipgloss.Color("#888888")
	ColorBorder  = lipgloss.Color("#444444")
	ColorAccent  = lipgloss.Color("#00AAFF")
)

// StyleRegistry holds all lipgloss styles used across TUI components.
type StyleRegistry struct {
	NodeName    lipgloss.Style
	NodeStatus  lipgloss.Style
	Header      lipgloss.Style
	Border      lipgloss.Style
	Muted       lipgloss.Style
	Error       lipgloss.Style
	ToolName    lipgloss.Style
	Thinking    lipgloss.Style
	StatusBar   lipgloss.Style
}

// Styles is the global style registry.
var Styles = StyleRegistry{
	NodeName:   lipgloss.NewStyle().Bold(true),
	NodeStatus: lipgloss.NewStyle().Foreground(ColorMuted),
	Header:     lipgloss.NewStyle().Bold(true).Foreground(ColorAccent),
	Border:     lipgloss.NewStyle().Foreground(ColorBorder),
	Muted:      lipgloss.NewStyle().Foreground(ColorMuted),
	Error:      lipgloss.NewStyle().Foreground(ColorFailed),
	ToolName:   lipgloss.NewStyle().Foreground(ColorAccent),
	Thinking:   lipgloss.NewStyle().Foreground(ColorRunning).Italic(true),
	StatusBar:  lipgloss.NewStyle().Foreground(ColorMuted),
}
```

**Important:** Read the actual colors from `tui/dashboard/style.go` and match them exactly. The code above is a template — adjust colors to match the existing palette.

- [ ] **Step 4: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run "TestLamp|TestThinking|TestStyles" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tui/styles.go tui/styles_test.go
git commit -m "feat(tui): add shared style registry"
```

---

### Task 3: StateStore

Central state container. All pipeline and agent state lives here. Components read via getters, updates via `Apply(msg)`.

**Files:**
- Create: `tui/state.go`

- [ ] **Step 1: Write the test**

Create `tui/state_test.go`:

```go
// ABOUTME: Tests for the StateStore central state container.
// ABOUTME: Verifies state updates via Apply and reads via getters.
package tui

import "testing"

func TestStateStoreInitialState(t *testing.T) {
	s := NewStateStore(nil)
	if len(s.Nodes()) != 0 {
		t.Error("expected empty node list")
	}
	if s.IsThinking("n1") {
		t.Error("expected not thinking initially")
	}
	if s.PipelineDone() {
		t.Error("expected pipeline not done initially")
	}
}

func TestStateStoreNodeLifecycle(t *testing.T) {
	s := NewStateStore(nil)
	s.SetNodes([]NodeEntry{
		{ID: "n1", Label: "Step 1"},
		{ID: "n2", Label: "Step 2"},
		{ID: "n3", Label: "Step 3"},
	})

	if len(s.Nodes()) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(s.Nodes()))
	}

	s.Apply(MsgNodeStarted{NodeID: "n1"})
	if s.NodeStatus("n1") != NodeRunning {
		t.Errorf("expected running, got %v", s.NodeStatus("n1"))
	}

	s.Apply(MsgNodeCompleted{NodeID: "n1", Outcome: "success"})
	if s.NodeStatus("n1") != NodeDone {
		t.Errorf("expected done, got %v", s.NodeStatus("n1"))
	}

	s.Apply(MsgNodeFailed{NodeID: "n2", Error: "boom"})
	if s.NodeStatus("n2") != NodeFailed {
		t.Errorf("expected failed, got %v", s.NodeStatus("n2"))
	}
	if s.NodeError("n2") != "boom" {
		t.Errorf("expected error 'boom', got %q", s.NodeError("n2"))
	}
}

func TestStateStorePipelineDone(t *testing.T) {
	s := NewStateStore(nil)
	s.Apply(MsgPipelineCompleted{})
	if !s.PipelineDone() {
		t.Error("expected pipeline done")
	}
}

func TestStateStorePipelineFailed(t *testing.T) {
	s := NewStateStore(nil)
	s.Apply(MsgPipelineFailed{Error: "fatal"})
	if !s.PipelineDone() {
		t.Error("expected pipeline done on failure")
	}
	if s.PipelineError() != "fatal" {
		t.Errorf("expected error 'fatal', got %q", s.PipelineError())
	}
}

func TestStateStoreThinking(t *testing.T) {
	s := NewStateStore(nil)
	s.Apply(MsgThinkingStarted{NodeID: "n1"})
	if !s.IsThinking("n1") {
		t.Error("expected thinking after start")
	}

	s.Apply(MsgThinkingStopped{NodeID: "n1"})
	if s.IsThinking("n1") {
		t.Error("expected not thinking after stop")
	}
}

func TestStateStoreCompletedCount(t *testing.T) {
	s := NewStateStore(nil)
	s.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}, {ID: "n3"}})
	s.Apply(MsgNodeCompleted{NodeID: "n1"})
	s.Apply(MsgNodeCompleted{NodeID: "n2"})

	done, total := s.Progress()
	if done != 2 || total != 3 {
		t.Errorf("expected 2/3, got %d/%d", done, total)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestStateStore -v`
Expected: FAIL — `NewStateStore` undefined

- [ ] **Step 3: Write state.go**

```go
// ABOUTME: Central state container for the TUI — single source of truth for pipeline and agent state.
// ABOUTME: Updated via Apply(msg), read via getter methods. Components never write directly.
package tui

import "github.com/2389-research/tracker/llm"

// NodeState represents the status of a pipeline node.
type NodeState int

const (
	NodePending NodeState = iota
	NodeRunning
	NodeDone
	NodeFailed
)

type nodeInfo struct {
	status NodeState
	err    string
}

// NodeEntry holds the display information for a pipeline node.
type NodeEntry struct {
	ID    string
	Label string
}

// StateStore holds all TUI state. Updated via Apply(), read via getters.
type StateStore struct {
	nodes        []NodeEntry
	nodeMap      map[string]*nodeInfo
	thinking     map[string]bool
	pipelineDone bool
	pipelineErr  string
	activeNode   string
	TokenTracker *llm.TokenTracker
}

// NewStateStore creates a new state store with an optional token tracker.
func NewStateStore(tt *llm.TokenTracker) *StateStore {
	return &StateStore{
		nodeMap:      make(map[string]*nodeInfo),
		thinking:     make(map[string]bool),
		TokenTracker: tt,
	}
}

// SetNodes initializes the ordered node list.
func (s *StateStore) SetNodes(entries []NodeEntry) {
	s.nodes = entries
	for _, e := range entries {
		if _, ok := s.nodeMap[e.ID]; !ok {
			s.nodeMap[e.ID] = &nodeInfo{}
		}
	}
}

// Apply updates state based on a typed message.
func (s *StateStore) Apply(msg interface{}) {
	switch m := msg.(type) {
	case MsgNodeStarted:
		s.ensureNode(m.NodeID)
		s.nodeMap[m.NodeID].status = NodeRunning
		s.activeNode = m.NodeID
	case MsgNodeCompleted:
		s.ensureNode(m.NodeID)
		s.nodeMap[m.NodeID].status = NodeDone
	case MsgNodeFailed:
		s.ensureNode(m.NodeID)
		s.nodeMap[m.NodeID].status = NodeFailed
		s.nodeMap[m.NodeID].err = m.Error
	case MsgPipelineCompleted:
		s.pipelineDone = true
	case MsgPipelineFailed:
		s.pipelineDone = true
		s.pipelineErr = m.Error
	case MsgThinkingStarted:
		s.thinking[m.NodeID] = true
	case MsgThinkingStopped:
		delete(s.thinking, m.NodeID)
	}
}

func (s *StateStore) ensureNode(id string) {
	if _, ok := s.nodeMap[id]; !ok {
		s.nodeMap[id] = &nodeInfo{}
	}
}

// Nodes returns the ordered list of node entries.
func (s *StateStore) Nodes() []NodeEntry { return s.nodes }

// NodeStatus returns the current status of a node.
func (s *StateStore) NodeStatus(id string) NodeState {
	if info, ok := s.nodeMap[id]; ok {
		return info.status
	}
	return NodePending
}

// NodeError returns the error message for a failed node.
func (s *StateStore) NodeError(id string) string {
	if info, ok := s.nodeMap[id]; ok {
		return info.err
	}
	return ""
}

// IsThinking returns whether a node's LLM is in thinking state.
func (s *StateStore) IsThinking(id string) bool { return s.thinking[id] }

// PipelineDone returns whether the pipeline has completed (success or failure).
func (s *StateStore) PipelineDone() bool { return s.pipelineDone }

// PipelineError returns the pipeline error message, if any.
func (s *StateStore) PipelineError() string { return s.pipelineErr }

// ActiveNode returns the most recently started node ID.
func (s *StateStore) ActiveNode() string { return s.activeNode }

// Progress returns (completed, total) node counts.
func (s *StateStore) Progress() (int, int) {
	done := 0
	for _, info := range s.nodeMap {
		if info.status == NodeDone {
			done++
		}
	}
	return done, len(s.nodes)
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestStateStore -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tui/state.go tui/state_test.go
git commit -m "feat(tui): add StateStore central state container"
```

---

### Task 4: ScrollView

Reusable scroll container with auto-scroll and manual override. Used by both NodeList and AgentLog.

**Files:**
- Create: `tui/scrollview.go`

- [ ] **Step 1: Write the test**

Create `tui/scrollview_test.go`:

```go
// ABOUTME: Tests for the reusable ScrollView component.
// ABOUTME: Covers auto-scroll, manual override, and viewport calculations.
package tui

import "testing"

func TestScrollViewAutoScroll(t *testing.T) {
	sv := NewScrollView(5) // 5 visible lines
	for i := 0; i < 10; i++ {
		sv.Append("line")
	}
	// Auto-scroll should show last 5 lines
	start, end := sv.VisibleRange()
	if start != 5 || end != 10 {
		t.Errorf("expected 5-10, got %d-%d", start, end)
	}
}

func TestScrollViewManualScrollDisablesAuto(t *testing.T) {
	sv := NewScrollView(5)
	for i := 0; i < 10; i++ {
		sv.Append("line")
	}
	sv.ScrollUp(2)
	start, _ := sv.VisibleRange()
	if start != 3 {
		t.Errorf("expected start=3 after scrolling up 2, got %d", start)
	}
	if sv.autoScroll {
		t.Error("expected auto-scroll disabled after manual scroll")
	}
}

func TestScrollViewScrollToBottom(t *testing.T) {
	sv := NewScrollView(5)
	for i := 0; i < 10; i++ {
		sv.Append("line")
	}
	sv.ScrollUp(3)
	sv.ScrollToBottom()
	if !sv.autoScroll {
		t.Error("expected auto-scroll re-enabled")
	}
	start, _ := sv.VisibleRange()
	if start != 5 {
		t.Errorf("expected start=5, got %d", start)
	}
}

func TestScrollViewEmptyContent(t *testing.T) {
	sv := NewScrollView(5)
	start, end := sv.VisibleRange()
	if start != 0 || end != 0 {
		t.Errorf("expected 0-0 for empty, got %d-%d", start, end)
	}
}

func TestScrollViewFewerThanHeight(t *testing.T) {
	sv := NewScrollView(10)
	sv.Append("a")
	sv.Append("b")
	start, end := sv.VisibleRange()
	if start != 0 || end != 2 {
		t.Errorf("expected 0-2, got %d-%d", start, end)
	}
}

func TestScrollViewSetHeight(t *testing.T) {
	sv := NewScrollView(5)
	for i := 0; i < 20; i++ {
		sv.Append("line")
	}
	sv.SetHeight(10)
	start, end := sv.VisibleRange()
	if end-start != 10 {
		t.Errorf("expected window of 10, got %d", end-start)
	}
}

func TestScrollViewUpdateLastLine(t *testing.T) {
	sv := NewScrollView(5)
	sv.Append("first")
	sv.Append("second")
	sv.UpdateLast("updated")
	lines := sv.Lines()
	if lines[1] != "updated" {
		t.Errorf("expected 'updated', got %q", lines[1])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestScrollView -v`
Expected: FAIL — `NewScrollView` undefined

- [ ] **Step 3: Write scrollview.go**

```go
// ABOUTME: Reusable scroll container with auto-scroll-to-bottom and manual scroll override.
// ABOUTME: Used by both NodeList and AgentLog to eliminate duplicated scroll logic.
package tui

// ScrollView manages a list of lines with a scrollable viewport.
type ScrollView struct {
	lines      []string
	height     int
	offset     int
	autoScroll bool
}

// NewScrollView creates a scroll view with the given visible height.
func NewScrollView(height int) *ScrollView {
	return &ScrollView{height: height, autoScroll: true}
}

// Append adds a line and auto-scrolls if enabled.
func (sv *ScrollView) Append(line string) {
	sv.lines = append(sv.lines, line)
	if sv.autoScroll {
		sv.clampOffset()
	}
}

// UpdateLast replaces the last line (for coalescing streaming chunks).
func (sv *ScrollView) UpdateLast(line string) {
	if len(sv.lines) > 0 {
		sv.lines[len(sv.lines)-1] = line
	}
}

// ScrollUp moves the viewport up by n lines and disables auto-scroll.
func (sv *ScrollView) ScrollUp(n int) {
	sv.autoScroll = false
	sv.offset -= n
	if sv.offset < 0 {
		sv.offset = 0
	}
}

// ScrollDown moves the viewport down by n lines.
func (sv *ScrollView) ScrollDown(n int) {
	sv.offset += n
	sv.clampOffset()
	// Re-enable auto-scroll if at bottom
	if sv.offset >= len(sv.lines)-sv.height {
		sv.autoScroll = true
	}
}

// ScrollToBottom jumps to the bottom and re-enables auto-scroll.
func (sv *ScrollView) ScrollToBottom() {
	sv.autoScroll = true
	sv.clampOffset()
}

// SetHeight updates the visible height (on terminal resize).
func (sv *ScrollView) SetHeight(h int) {
	sv.height = h
	if sv.autoScroll {
		sv.clampOffset()
	}
}

// VisibleRange returns the [start, end) indices of visible lines.
func (sv *ScrollView) VisibleRange() (int, int) {
	if len(sv.lines) == 0 {
		return 0, 0
	}
	start := sv.offset
	end := start + sv.height
	if end > len(sv.lines) {
		end = len(sv.lines)
	}
	return start, end
}

// Lines returns all lines (for testing).
func (sv *ScrollView) Lines() []string { return sv.lines }

// Len returns the total number of lines.
func (sv *ScrollView) Len() int { return len(sv.lines) }

func (sv *ScrollView) clampOffset() {
	maxOffset := len(sv.lines) - sv.height
	if maxOffset < 0 {
		maxOffset = 0
	}
	if sv.autoScroll {
		sv.offset = maxOffset
	} else if sv.offset > maxOffset {
		sv.offset = maxOffset
	}
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestScrollView -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tui/scrollview.go tui/scrollview_test.go
git commit -m "feat(tui): add reusable ScrollView component"
```

---

### Task 5: ThinkingTracker

Tracks LLM thinking state per node. Drives lamp animation frames and elapsed time.

**Files:**
- Create: `tui/thinking.go`

- [ ] **Step 1: Write the test**

Create `tui/thinking_test.go`:

```go
// ABOUTME: Tests for the ThinkingTracker component.
// ABOUTME: Verifies state machine transitions, frame cycling, and elapsed time tracking.
package tui

import (
	"testing"
	"time"
)

func TestThinkingTrackerStartStop(t *testing.T) {
	tr := NewThinkingTracker()
	if tr.IsThinking("n1") {
		t.Error("should not be thinking initially")
	}

	tr.Start("n1")
	if !tr.IsThinking("n1") {
		t.Error("should be thinking after Start")
	}

	tr.Stop("n1")
	if tr.IsThinking("n1") {
		t.Error("should not be thinking after Stop")
	}
}

func TestThinkingTrackerFrameCycles(t *testing.T) {
	tr := NewThinkingTracker()
	tr.Start("n1")

	frames := make([]string, 5)
	for i := range frames {
		frames[i] = tr.Frame("n1")
		tr.Tick()
	}

	// Should cycle: frame 0, 1, 2, 3, 0
	if frames[0] != ThinkingFrames[0] {
		t.Errorf("frame 0: got %q, want %q", frames[0], ThinkingFrames[0])
	}
	if frames[4] != ThinkingFrames[0] {
		t.Errorf("frame 4 should wrap to frame 0: got %q", frames[4])
	}
}

func TestThinkingTrackerElapsed(t *testing.T) {
	tr := NewThinkingTracker()
	tr.StartAt("n1", time.Now().Add(-3*time.Second))

	elapsed := tr.Elapsed("n1")
	if elapsed < 2*time.Second || elapsed > 5*time.Second {
		t.Errorf("expected ~3s elapsed, got %v", elapsed)
	}
}

func TestThinkingTrackerMultipleNodes(t *testing.T) {
	tr := NewThinkingTracker()
	tr.Start("n1")
	tr.Start("n2")

	if !tr.IsThinking("n1") || !tr.IsThinking("n2") {
		t.Error("both nodes should be thinking")
	}

	tr.Stop("n1")
	if tr.IsThinking("n1") {
		t.Error("n1 should have stopped")
	}
	if !tr.IsThinking("n2") {
		t.Error("n2 should still be thinking")
	}
}

func TestThinkingTrackerNotThinkingFrame(t *testing.T) {
	tr := NewThinkingTracker()
	frame := tr.Frame("n1")
	if frame != "" {
		t.Errorf("expected empty frame for non-thinking node, got %q", frame)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestThinkingTracker -v`
Expected: FAIL — `NewThinkingTracker` undefined

- [ ] **Step 3: Write thinking.go**

```go
// ABOUTME: ThinkingTracker tracks LLM thinking state per node with animation frames and elapsed time.
// ABOUTME: Drives the spinning lamp indicator on active nodes and the "Thinking... (3.2s)" agent log line.
package tui

import "time"

type thinkingState struct {
	startedAt time.Time
	frame     int
}

// ThinkingTracker manages per-node LLM thinking state.
type ThinkingTracker struct {
	nodes map[string]*thinkingState
}

// NewThinkingTracker creates a new tracker.
func NewThinkingTracker() *ThinkingTracker {
	return &ThinkingTracker{nodes: make(map[string]*thinkingState)}
}

// Start marks a node as thinking (uses current time).
func (tr *ThinkingTracker) Start(nodeID string) {
	tr.StartAt(nodeID, time.Now())
}

// StartAt marks a node as thinking with a specific start time (for testing).
func (tr *ThinkingTracker) StartAt(nodeID string, at time.Time) {
	tr.nodes[nodeID] = &thinkingState{startedAt: at}
}

// Stop marks a node as no longer thinking.
func (tr *ThinkingTracker) Stop(nodeID string) {
	delete(tr.nodes, nodeID)
}

// IsThinking returns whether a node is currently in thinking state.
func (tr *ThinkingTracker) IsThinking(nodeID string) bool {
	_, ok := tr.nodes[nodeID]
	return ok
}

// Frame returns the current animation frame character for a thinking node.
// Returns empty string if the node is not thinking.
func (tr *ThinkingTracker) Frame(nodeID string) string {
	st, ok := tr.nodes[nodeID]
	if !ok {
		return ""
	}
	return ThinkingFrames[st.frame%len(ThinkingFrames)]
}

// Elapsed returns how long the node has been thinking.
func (tr *ThinkingTracker) Elapsed(nodeID string) time.Duration {
	st, ok := tr.nodes[nodeID]
	if !ok {
		return 0
	}
	return time.Since(st.startedAt)
}

// Tick advances the animation frame for all thinking nodes.
func (tr *ThinkingTracker) Tick() {
	for _, st := range tr.nodes {
		st.frame++
	}
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestThinkingTracker -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tui/thinking.go tui/thinking_test.go
git commit -m "feat(tui): add ThinkingTracker for LLM thinking state"
```

---

## Chunk 2: Components

### Task 6: Header

Top bar showing pipeline name, run ID, elapsed time, and token/cost readout.

**Files:**
- Create: `tui/header.go`

- [ ] **Step 1: Write the test**

Add to `tui/header_test.go`:

```go
// ABOUTME: Tests for the Header component.
// ABOUTME: Verifies rendering of pipeline info and elapsed time.
package tui

import (
	"strings"
	"testing"
)

func TestHeaderRender(t *testing.T) {
	store := NewStateStore(nil)
	h := NewHeader(store, "test-pipeline", "run-abc")
	view := h.View()

	if !strings.Contains(view, "test-pipeline") {
		t.Errorf("expected pipeline name in header, got: %s", view)
	}
	if !strings.Contains(view, "run-abc") {
		t.Errorf("expected run ID in header, got: %s", view)
	}
}

func TestHeaderElapsedTime(t *testing.T) {
	store := NewStateStore(nil)
	h := NewHeader(store, "p", "r")
	// Initial view should show 0s or 00:00
	view := h.View()
	if !strings.Contains(view, "0") {
		t.Errorf("expected zero elapsed time initially, got: %s", view)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestHeader -v`
Expected: FAIL — `NewHeader` undefined

- [ ] **Step 3: Write header.go**

Read existing `tui/dashboard/header.go` (174 lines) to understand the current rendering. Create a clean version:

```go
// ABOUTME: Header component — displays pipeline name, run ID, elapsed time, and token/cost readout.
// ABOUTME: Self-contained Bubbletea model with its own Update/View. Reads token data from StateStore.
package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Header displays pipeline metadata at the top of the TUI.
type Header struct {
	store        *StateStore
	pipelineName string
	runID        string
	startedAt    time.Time
	width        int
}

// NewHeader creates a header component.
func NewHeader(store *StateStore, pipelineName, runID string) *Header {
	return &Header{
		store:        store,
		pipelineName: pipelineName,
		runID:        runID,
		startedAt:    time.Now(),
	}
}

// Update handles messages (header tick for elapsed time).
func (h *Header) Update(msg tea.Msg) tea.Cmd {
	switch msg.(type) {
	case MsgHeaderTick:
		// No state change needed — View() computes elapsed from startedAt
		return nil
	}
	return nil
}

// SetWidth updates the header width on terminal resize.
func (h *Header) SetWidth(w int) { h.width = w }

// View renders the header.
func (h *Header) View() string {
	elapsed := time.Since(h.startedAt).Truncate(time.Second)

	left := Styles.Header.Render(h.pipelineName) + "  " +
		Styles.Muted.Render(h.runID)

	right := Styles.Muted.Render(fmt.Sprintf("%s", elapsed))

	// Add token info if available
	if h.store.TokenTracker != nil {
		usage := h.store.TokenTracker.TotalUsage()
		if usage.TotalTokens > 0 {
			right = Styles.Muted.Render(fmt.Sprintf("%dt  ", usage.TotalTokens)) + right
		}
	}

	gap := h.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}

	return left + fmt.Sprintf("%*s", gap, "") + right
}
```

**Important:** Read the existing `tui/dashboard/header.go` to match the exact layout and token display format. The code above is a template.

- [ ] **Step 4: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestHeader -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tui/header.go tui/header_test.go
git commit -m "feat(tui): add Header component"
```

---

### Task 7: StatusBar

Bottom bar showing track diagram, progress summary, and keybinding hints.

**Files:**
- Create: `tui/statusbar.go`

- [ ] **Step 1: Write the test**

Create `tui/statusbar_test.go`:

```go
// ABOUTME: Tests for the StatusBar component.
// ABOUTME: Verifies track diagram, progress summary, and keybinding hints.
package tui

import (
	"strings"
	"testing"
)

func TestStatusBarProgress(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}, {ID: "n3"}})
	store.Apply(MsgNodeCompleted{NodeID: "n1"})

	sb := NewStatusBar(store)
	view := sb.View()
	if !strings.Contains(view, "1/3") {
		t.Errorf("expected '1/3' progress, got: %s", view)
	}
}

func TestStatusBarTrackDiagram(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}, {ID: "n3"}})
	store.Apply(MsgNodeStarted{NodeID: "n1"})
	store.Apply(MsgNodeCompleted{NodeID: "n1"})
	store.Apply(MsgNodeFailed{NodeID: "n2"})

	sb := NewStatusBar(store)
	view := sb.View()
	// Should contain lamp glyphs for done, failed, pending
	if !strings.Contains(view, LampDone) {
		t.Errorf("expected done lamp in track diagram, got: %s", view)
	}
	if !strings.Contains(view, LampFailed) {
		t.Errorf("expected failed lamp in track diagram, got: %s", view)
	}
}

func TestStatusBarKeybindingHints(t *testing.T) {
	store := NewStateStore(nil)
	sb := NewStatusBar(store)
	view := sb.View()
	if !strings.Contains(view, "ctrl+o") {
		t.Errorf("expected keybinding hint, got: %s", view)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestStatusBar -v`
Expected: FAIL — `NewStatusBar` undefined

- [ ] **Step 3: Write statusbar.go**

Read existing `tui/dashboard/nodelist.go` for the `TrackDiagram()` and `ProgressSummary()` methods. Create a clean standalone version:

```go
// ABOUTME: StatusBar component — renders track diagram, progress summary, and keybinding hints.
// ABOUTME: Reads node statuses from StateStore to build the compact glyph view.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// StatusBar renders pipeline progress at the bottom of the TUI.
type StatusBar struct {
	store *StateStore
	width int
}

// NewStatusBar creates a status bar component.
func NewStatusBar(store *StateStore) *StatusBar {
	return &StatusBar{store: store}
}

// SetWidth updates the status bar width on terminal resize.
func (sb *StatusBar) SetWidth(w int) { sb.width = w }

// View renders the status bar.
func (sb *StatusBar) View() string {
	// Track diagram: compact glyph per node
	var track strings.Builder
	for _, n := range sb.store.Nodes() {
		switch sb.store.NodeStatus(n.ID) {
		case NodeRunning:
			track.WriteString(lipgloss.NewStyle().Foreground(ColorRunning).Render(LampRunning))
		case NodeDone:
			track.WriteString(lipgloss.NewStyle().Foreground(ColorDone).Render(LampDone))
		case NodeFailed:
			track.WriteString(lipgloss.NewStyle().Foreground(ColorFailed).Render(LampFailed))
		default:
			track.WriteString(lipgloss.NewStyle().Foreground(ColorPending).Render(LampPending))
		}
	}

	// Progress summary
	done, total := sb.store.Progress()
	progress := Styles.Muted.Render(fmt.Sprintf(" %d/%d ", done, total))

	// Keybinding hints
	hints := Styles.Muted.Render("ctrl+o expand/collapse  q quit")

	left := track.String() + progress
	right := hints

	gap := sb.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}

	return Styles.StatusBar.Render(left + fmt.Sprintf("%*s", gap, "") + right)
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestStatusBar -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tui/statusbar.go tui/statusbar_test.go
git commit -m "feat(tui): add StatusBar component"
```

---

### Task 8: NodeList

Signal lamp panel showing pipeline nodes with status indicators and thinking animation.

**Files:**
- Create: `tui/nodelist.go`

- [ ] **Step 1: Write the test**

Create `tui/nodelist_test.go`:

```go
// ABOUTME: Tests for the NodeList component.
// ABOUTME: Verifies signal lamp rendering, thinking animation, and scroll behavior.
package tui

import (
	"strings"
	"testing"
)

func TestNodeListRendersNodes(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "step1"}, {ID: "step2"}, {ID: "step3"}})
	tr := NewThinkingTracker()

	nl := NewNodeList(store, tr, 10)
	view := nl.View()
	if !strings.Contains(view, "step1") {
		t.Errorf("expected step1 in view, got: %s", view)
	}
	if !strings.Contains(view, LampPending) {
		t.Errorf("expected pending lamp, got: %s", view)
	}
}

func TestNodeListSignalLamps(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}, {ID: "n3"}})
	store.Apply(MsgNodeStarted{NodeID: "n1"})
	store.Apply(MsgNodeCompleted{NodeID: "n1"})
	store.Apply(MsgNodeFailed{NodeID: "n2"})
	tr := NewThinkingTracker()

	nl := NewNodeList(store, tr, 10)
	view := nl.View()

	if !strings.Contains(view, LampDone) {
		t.Errorf("expected done lamp for n1")
	}
	if !strings.Contains(view, LampFailed) {
		t.Errorf("expected failed lamp for n2")
	}
	if !strings.Contains(view, LampPending) {
		t.Errorf("expected pending lamp for n3")
	}
}

func TestNodeListThinkingAnimation(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}})
	store.Apply(MsgNodeStarted{NodeID: "n1"})
	tr := NewThinkingTracker()
	tr.Start("n1")

	nl := NewNodeList(store, tr, 10)
	view := nl.View()

	// Should show a thinking frame instead of the running lamp
	if !strings.Contains(view, ThinkingFrames[0]) {
		t.Errorf("expected thinking frame, got: %s", view)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestNodeList -v`
Expected: FAIL — `NewNodeList` undefined

- [ ] **Step 3: Write nodelist.go**

Read existing `tui/dashboard/nodelist.go` (326 lines) for the current rendering logic. Create a clean version using ScrollView and ThinkingTracker:

```go
// ABOUTME: NodeList component — renders pipeline nodes with signal lamp status indicators.
// ABOUTME: Shows thinking animation on nodes with active LLM requests. Uses ScrollView for viewport.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// NodeList renders the pipeline node panel with signal lamps.
type NodeList struct {
	store    *StateStore
	thinking *ThinkingTracker
	scroll   *ScrollView
	width    int
	height   int
}

// NewNodeList creates a node list component.
func NewNodeList(store *StateStore, thinking *ThinkingTracker, height int) *NodeList {
	return &NodeList{
		store:    store,
		thinking: thinking,
		scroll:   NewScrollView(height),
		height:   height,
	}
}

// Update handles messages.
func (nl *NodeList) Update(msg tea.Msg) tea.Cmd {
	switch msg.(type) {
	case MsgThinkingTick:
		// View re-renders with new animation frame — no state change needed
	}
	return nil
}

// SetSize updates dimensions on terminal resize.
func (nl *NodeList) SetSize(w, h int) {
	nl.width = w
	nl.height = h
	nl.scroll.SetHeight(h)
}

// View renders the node list.
func (nl *NodeList) View() string {
	nodes := nl.store.Nodes()
	if len(nodes) == 0 {
		return Styles.Muted.Render("No nodes")
	}

	var lines []string
	for _, n := range nodes {
		lamp := nl.lampFor(n.ID)
		label := n.Label
		if label == "" {
			label = n.ID
		}
		name := Styles.NodeName.Render(label)
		lines = append(lines, fmt.Sprintf(" %s %s", lamp, name))
	}

	// Build view from lines (simple for now — can use ScrollView for long lists)
	visible := lines
	if len(visible) > nl.height && nl.height > 0 {
		// Use scroll offset
		start := 0
		end := nl.height
		if end > len(visible) {
			end = len(visible)
		}
		visible = visible[start:end]
	}

	return strings.Join(visible, "\n")
}

func (nl *NodeList) lampFor(nodeID string) string {
	status := nl.store.NodeStatus(nodeID)

	// Thinking animation takes priority over running lamp
	if status == NodeRunning && nl.thinking.IsThinking(nodeID) {
		frame := nl.thinking.Frame(nodeID)
		return lipgloss.NewStyle().Foreground(ColorRunning).Render(frame)
	}

	switch status {
	case NodeRunning:
		return lipgloss.NewStyle().Foreground(ColorRunning).Render(LampRunning)
	case NodeDone:
		return lipgloss.NewStyle().Foreground(ColorDone).Render(LampDone)
	case NodeFailed:
		return lipgloss.NewStyle().Foreground(ColorFailed).Render(LampFailed)
	default:
		return lipgloss.NewStyle().Foreground(ColorPending).Render(LampPending)
	}
}
```

**Important:** Read the existing `tui/dashboard/nodelist.go` to replicate auto-scroll-to-running-node behavior and node attribute display (handler names, shapes, etc.).

- [ ] **Step 4: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestNodeList -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tui/nodelist.go tui/nodelist_test.go
git commit -m "feat(tui): add NodeList component with thinking animation"
```

---

### Task 9: AgentLog

Streaming agent activity log with text coalescing, expand/collapse, thinking indicator, and verbose trace mode.

**Files:**
- Create: `tui/agentlog.go`

- [ ] **Step 1: Write the test**

Create `tui/agentlog_test.go`:

```go
// ABOUTME: Tests for the AgentLog component.
// ABOUTME: Covers text coalescing, expand/collapse, thinking indicator, and tool formatting.
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAgentLogTextCoalescing(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 20)

	// Simulate streaming text chunks — should coalesce into one entry
	al.Update(MsgTextChunk{NodeID: "n1", Text: "Hello "})
	al.Update(MsgTextChunk{NodeID: "n1", Text: "world"})

	view := al.View()
	if !strings.Contains(view, "Hello world") {
		t.Errorf("expected coalesced text, got: %s", view)
	}
}

func TestAgentLogToolCallBreaksCoalescing(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 20)

	al.Update(MsgTextChunk{NodeID: "n1", Text: "before"})
	al.Update(MsgToolCallStart{NodeID: "n1", ToolName: "exec"})
	al.Update(MsgTextChunk{NodeID: "n1", Text: "after"})

	view := al.View()
	if !strings.Contains(view, "before") || !strings.Contains(view, "after") {
		t.Errorf("expected both text segments, got: %s", view)
	}
	if !strings.Contains(view, "exec") {
		t.Errorf("expected tool name, got: %s", view)
	}
}

func TestAgentLogThinkingIndicator(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}})
	store.Apply(MsgNodeStarted{NodeID: "n1"})
	tr := NewThinkingTracker()
	tr.Start("n1")

	al := NewAgentLog(store, tr, 20)
	al.SetFocusedNode("n1")
	view := al.View()

	if !strings.Contains(view, "Thinking") {
		t.Errorf("expected thinking indicator, got: %s", view)
	}
}

func TestAgentLogExpandCollapse(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 20)

	// Add multi-line content
	al.Update(MsgToolCallEnd{
		NodeID:   "n1",
		ToolName: "exec",
		Output:   "line1\nline2\nline3\nline4\nline5\nline6",
	})

	// Collapsed (default) — should truncate
	collapsed := al.View()

	// Expand
	al.Update(MsgToggleExpand{})
	expanded := al.View()

	// Expanded should be longer or equal
	if len(expanded) < len(collapsed) {
		t.Error("expected expanded view to be >= collapsed view")
	}
}

func TestAgentLogReasoningChunk(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 20)

	al.Update(MsgReasoningChunk{NodeID: "n1", Text: "I think that..."})
	view := al.View()
	if !strings.Contains(view, "I think that...") {
		t.Errorf("expected reasoning text, got: %s", view)
	}
}

func TestAgentLogVerboseTrace(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 20)

	al.Update(MsgLLMProviderRaw{NodeID: "n1", Data: "raw data"})

	// Non-verbose: should not show raw data
	view := al.View()
	if strings.Contains(view, "raw data") {
		t.Error("non-verbose mode should hide raw data")
	}

	// Enable verbose
	al.SetVerboseTrace(true)
	al.Update(MsgLLMProviderRaw{NodeID: "n1", Data: "raw data 2"})
	view = al.View()
	if !strings.Contains(view, "raw data 2") {
		t.Errorf("verbose mode should show raw data, got: %s", view)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestAgentLog -v`
Expected: FAIL — `NewAgentLog` undefined

- [ ] **Step 3: Write agentlog.go**

Read existing `tui/dashboard/agentlog.go` (416 lines) carefully to understand the coalescing state machine (`coalesceBuf`, `coalesceKind`, `coalesceActive`), the `collapseLines` function, model-header deduplication, and tool-color categorization. Create a clean version:

```go
// ABOUTME: AgentLog component — streaming activity log with text coalescing and expand/collapse.
// ABOUTME: Shows LLM text/reasoning, tool calls, errors, and a thinking indicator for the focused node.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const defaultCollapseLines = 4

// AgentLog displays streaming agent activity.
type AgentLog struct {
	store        *StateStore
	thinking     *ThinkingTracker
	scroll       *ScrollView
	focusedNode  string
	expanded     bool
	verboseTrace bool
	width        int
	height       int

	// Coalescing state machine
	coalesceBuf    *strings.Builder
	coalesceKind   string // "text" or "reasoning"
	coalesceActive bool
}

// NewAgentLog creates an agent log component.
func NewAgentLog(store *StateStore, thinking *ThinkingTracker, height int) *AgentLog {
	return &AgentLog{
		store:    store,
		thinking: thinking,
		scroll:   NewScrollView(height),
		height:   height,
	}
}

// SetVerboseTrace enables/disables verbose LLM trace output.
func (al *AgentLog) SetVerboseTrace(v bool) { al.verboseTrace = v }

// SetFocusedNode sets which node's log to display.
func (al *AgentLog) SetFocusedNode(id string) { al.focusedNode = id }

// SetSize updates dimensions on terminal resize.
func (al *AgentLog) SetSize(w, h int) {
	al.width = w
	al.height = h
	al.scroll.SetHeight(h)
}

// Update handles messages and updates the log content.
func (al *AgentLog) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case MsgTextChunk:
		al.handleTextChunk(m.Text, "text")
	case MsgReasoningChunk:
		al.handleTextChunk(m.Text, "reasoning")
	case MsgToolCallStart:
		al.flushCoalesce()
		al.scroll.Append(Styles.ToolName.Render("▶ " + m.ToolName))
	case MsgToolCallEnd:
		al.flushCoalesce()
		output := m.Output
		if !al.expanded {
			output = collapseLines(output, defaultCollapseLines)
		}
		if m.Error != "" {
			al.scroll.Append(Styles.Error.Render("✗ " + m.ToolName + ": " + m.Error))
		}
		if output != "" {
			al.scroll.Append(output)
		}
	case MsgAgentError:
		al.flushCoalesce()
		al.scroll.Append(Styles.Error.Render("error: " + m.Error))
	case MsgLLMRequestStart:
		al.flushCoalesce()
		header := Styles.Muted.Render(fmt.Sprintf("[%s/%s]", m.Provider, m.Model))
		al.scroll.Append(header)
	case MsgLLMProviderRaw:
		if al.verboseTrace {
			al.scroll.Append(Styles.Muted.Render(m.Data))
		}
	case MsgToggleExpand:
		al.expanded = !al.expanded
	}
	return nil
}

func (al *AgentLog) handleTextChunk(text, kind string) {
	if al.coalesceActive && al.coalesceKind == kind {
		// Extend current coalesced block
		al.coalesceBuf.WriteString(text)
		rendered := al.renderCoalesced()
		al.scroll.UpdateLast(rendered)
	} else {
		// Start new coalesced block
		al.flushCoalesce()
		al.coalesceBuf = &strings.Builder{}
		al.coalesceBuf.WriteString(text)
		al.coalesceKind = kind
		al.coalesceActive = true
		rendered := al.renderCoalesced()
		al.scroll.Append(rendered)
	}
}

func (al *AgentLog) renderCoalesced() string {
	text := al.coalesceBuf.String()
	if al.coalesceKind == "reasoning" {
		return Styles.Muted.Render(text)
	}
	return text
}

func (al *AgentLog) flushCoalesce() {
	al.coalesceActive = false
	al.coalesceBuf = nil
	al.coalesceKind = ""
}

// View renders the agent log.
func (al *AgentLog) View() string {
	var lines []string

	start, end := al.scroll.VisibleRange()
	allLines := al.scroll.Lines()
	if start < end {
		lines = allLines[start:end]
	}

	// Append thinking indicator if focused node is thinking
	if al.focusedNode != "" && al.thinking.IsThinking(al.focusedNode) {
		elapsed := al.thinking.Elapsed(al.focusedNode)
		indicator := Styles.Thinking.Render(
			fmt.Sprintf("⟳ Thinking... (%.1fs)", elapsed.Seconds()),
		)
		lines = append(lines, indicator)
	}

	if len(lines) == 0 {
		return Styles.Muted.Render("Waiting for activity...")
	}

	return strings.Join(lines, "\n")
}

// collapseLines truncates multi-line output to maxLines.
func collapseLines(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	truncated := lines[:maxLines]
	truncated = append(truncated, Styles.Muted.Render(
		fmt.Sprintf("... (%d more lines)", len(lines)-maxLines),
	))
	return strings.Join(truncated, "\n")
}

// thinkingTickCmd returns a command that sends a MsgThinkingTick after 150ms.
func thinkingTickCmd() tea.Cmd {
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg {
		return MsgThinkingTick{}
	})
}

// headerTickCmd returns a command that sends a MsgHeaderTick after 1s.
func headerTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return MsgHeaderTick{}
	})
}
```

**Important:** Read the existing `tui/dashboard/agentlog.go` carefully for:
- The full coalescing state machine (buffer pointer semantics)
- Model-header deduplication logic
- Tool-color categorization (different colors for different tool types)
- How `collapseLines` handles edge cases

The code above is a simplified version — the implementer must verify it handles the same edge cases as the existing code.

- [ ] **Step 4: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestAgentLog -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tui/agentlog.go tui/agentlog_test.go
git commit -m "feat(tui): add AgentLog component with coalescing and thinking indicator"
```

---

### Task 10: Modal + Choice/Freeform

Overlay container with choice and freeform gate content models.

**Files:**
- Create: `tui/modal.go`

- [ ] **Step 1: Write the test**

Create `tui/modal_test.go`:

```go
// ABOUTME: Tests for the Modal overlay and Choice/Freeform content models.
// ABOUTME: Verifies overlay rendering, choice selection, and freeform input.
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModalOverlayRendering(t *testing.T) {
	m := NewModal(80, 24)
	m.Show(NewChoiceContent("Pick one", []string{"a", "b", "c"}, nil))

	view := m.View("background content here")
	if !strings.Contains(view, "Pick one") {
		t.Errorf("expected prompt in modal, got: %s", view)
	}
}

func TestModalHideShow(t *testing.T) {
	m := NewModal(80, 24)
	if m.Visible() {
		t.Error("should not be visible initially")
	}

	m.Show(NewChoiceContent("test", []string{"a"}, nil))
	if !m.Visible() {
		t.Error("should be visible after Show")
	}

	m.Hide()
	if m.Visible() {
		t.Error("should not be visible after Hide")
	}
}

func TestChoiceContentSelection(t *testing.T) {
	ch := make(chan string, 1)
	c := NewChoiceContent("Pick", []string{"alpha", "beta", "gamma"}, ch)

	// Move down and select
	c.Update(tea.KeyMsg{Type: tea.KeyDown})
	c.Update(tea.KeyMsg{Type: tea.KeyEnter})

	select {
	case got := <-ch:
		if got != "beta" {
			t.Errorf("expected 'beta', got %q", got)
		}
	default:
		t.Error("expected value on reply channel")
	}
}

func TestFreeformContentSubmit(t *testing.T) {
	ch := make(chan string, 1)
	f := NewFreeformContent("Enter value", ch)

	// Type and submit
	for _, r := range "hello" {
		f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	f.Update(tea.KeyMsg{Type: tea.KeyEnter})

	select {
	case got := <-ch:
		if got != "hello" {
			t.Errorf("expected 'hello', got %q", got)
		}
	default:
		t.Error("expected value on reply channel")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run "TestModal|TestChoice|TestFreeform" -v`
Expected: FAIL — `NewModal` undefined

- [ ] **Step 3: Write modal.go**

Read existing `tui/components/modal.go`, `tui/components/choice.go`, and `tui/components/freeform.go` for the current implementations. Create a consolidated version:

```go
// ABOUTME: Modal overlay container with choice and freeform content models for gate interactions.
// ABOUTME: Uses lipgloss Place for proper centering instead of string manipulation.
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ModalContent is the interface for content displayed inside the modal.
type ModalContent interface {
	Update(msg tea.Msg) tea.Cmd
	View() string
}

// Modal is a centered overlay container.
type Modal struct {
	content ModalContent
	visible bool
	width   int
	height  int
}

// NewModal creates a modal overlay.
func NewModal(width, height int) *Modal {
	return &Modal{width: width, height: height}
}

// Show displays the modal with the given content.
func (m *Modal) Show(content ModalContent) {
	m.content = content
	m.visible = true
}

// Hide closes the modal.
func (m *Modal) Hide() {
	m.visible = false
	m.content = nil
}

// Visible returns whether the modal is currently shown.
func (m *Modal) Visible() bool { return m.visible }

// Update forwards messages to the modal content.
func (m *Modal) Update(msg tea.Msg) tea.Cmd {
	if !m.visible || m.content == nil {
		return nil
	}
	return m.content.Update(msg)
}

// View renders the modal overlaid on the background.
func (m *Modal) View(background string) string {
	if !m.visible || m.content == nil {
		return background
	}

	modalStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorAccent).
		Padding(1, 2).
		MaxWidth(m.width * 3 / 4)

	overlay := modalStyle.Render(m.content.View())

	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		overlay,
		lipgloss.WithWhitespaceChars(" "),
	)
}

// SetSize updates modal dimensions on terminal resize.
func (m *Modal) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// --- ChoiceContent ---

// ChoiceContent renders a multiple-choice gate prompt.
type ChoiceContent struct {
	prompt  string
	options []string
	cursor  int
	replyCh chan<- string
}

// NewChoiceContent creates a choice modal content.
func NewChoiceContent(prompt string, options []string, replyCh chan<- string) *ChoiceContent {
	return &ChoiceContent{
		prompt:  prompt,
		options: options,
		replyCh: replyCh,
	}
}

// Update handles key input for choice selection.
func (c *ChoiceContent) Update(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch km.Type {
	case tea.KeyUp:
		if c.cursor > 0 {
			c.cursor--
		}
	case tea.KeyDown:
		if c.cursor < len(c.options)-1 {
			c.cursor++
		}
	case tea.KeyEnter:
		if c.replyCh != nil {
			c.replyCh <- c.options[c.cursor]
		}
		return nil
	}
	return nil
}

// View renders the choice list.
func (c *ChoiceContent) View() string {
	var b strings.Builder
	b.WriteString(Styles.Header.Render(c.prompt))
	b.WriteString("\n\n")
	for i, opt := range c.options {
		cursor := "  "
		if i == c.cursor {
			cursor = "▸ "
		}
		b.WriteString(cursor + opt + "\n")
	}
	return b.String()
}

// --- FreeformContent ---

// FreeformContent renders a free-text gate prompt.
type FreeformContent struct {
	prompt  string
	input   []rune
	replyCh chan<- string
}

// NewFreeformContent creates a freeform modal content.
func NewFreeformContent(prompt string, replyCh chan<- string) *FreeformContent {
	return &FreeformContent{
		prompt:  prompt,
		replyCh: replyCh,
	}
}

// Update handles key input for freeform text entry.
func (f *FreeformContent) Update(msg tea.Msg) tea.Cmd {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch km.Type {
	case tea.KeyRunes:
		f.input = append(f.input, km.Runes...)
	case tea.KeyBackspace:
		if len(f.input) > 0 {
			f.input = f.input[:len(f.input)-1]
		}
	case tea.KeyEnter:
		if f.replyCh != nil {
			f.replyCh <- string(f.input)
		}
		return nil
	}
	return nil
}

// View renders the freeform input.
func (f *FreeformContent) View() string {
	var b strings.Builder
	b.WriteString(Styles.Header.Render(f.prompt))
	b.WriteString("\n\n")
	b.WriteString(string(f.input))
	b.WriteString("█") // cursor
	return b.String()
}
```

**Important:** Read the existing `tui/components/choice.go` (206 lines) and `tui/components/freeform.go` (186 lines) for scrollable prompt rendering, viewport management, and edge cases (empty options, long prompts). The code above is simplified — the implementer should port the relevant viewport logic.

- [ ] **Step 4: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run "TestModal|TestChoice|TestFreeform" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tui/modal.go tui/modal_test.go
git commit -m "feat(tui): add Modal overlay with Choice and Freeform content"
```

---

### Task 11: Interviewer

Bridges pipeline gate requests into the TUI. Implements `handlers.Interviewer` and `handlers.FreeformInterviewer`.

**Files:**
- Create: `tui/interviewer.go`

- [ ] **Step 1: Write the test**

Create `tui/interviewer_test.go`:

```go
// ABOUTME: Tests for the BubbleteaInterviewer gate bridge.
// ABOUTME: Verifies Mode 1 and Mode 2 gate request/reply flow.
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestInterviewerMode2SendsGateChoice(t *testing.T) {
	// Mode 2: interviewer sends messages through tea.Program
	msgCh := make(chan tea.Msg, 1)
	mockSend := func(msg tea.Msg) {
		msgCh <- msg
	}

	iv := NewBubbleteaInterviewer(mockSend)

	// Run Ask in a goroutine (it blocks until reply)
	done := make(chan string, 1)
	go func() {
		result, err := iv.Ask("Pick one", []string{"a", "b"}, "a")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		done <- result
	}()

	// Wait for the message to be sent (via channel — no race)
	sentMsg := <-msgCh
	msg, ok := sentMsg.(MsgGateChoice)
	if !ok {
		t.Fatalf("expected MsgGateChoice, got %T", sentMsg)
	}
	msg.ReplyCh <- "b"

	result := <-done
	if result != "b" {
		t.Errorf("expected 'b', got %q", result)
	}
}
```

**Note:** This test is tricky because of the goroutine/channel interplay. The implementer should read the existing `tui/interviewer.go` (186 lines) carefully for Mode 1 vs Mode 2 logic and the reply channel pattern. The test above is a starting point — adjust based on the actual implementation's concurrency model.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestInterviewer -v`
Expected: FAIL — `NewBubbleteaInterviewer` undefined

- [ ] **Step 3: Write interviewer.go**

Read existing `tui/interviewer.go` (186 lines) and `tui/messages.go` (25 lines) carefully. The new version must:
- Implement `handlers.Interviewer` (Ask method)
- Implement `handlers.FreeformInterviewer` (AskFreeform method)
- Support Mode 1 (inline tea.Program per gate) and Mode 2 (send through existing program)
- Use the new typed messages (`MsgGateChoice`, `MsgGateFreeform`)

```go
// ABOUTME: BubbleteaInterviewer bridges pipeline gate requests into the TUI.
// ABOUTME: Mode 1: spins up inline tea.Program per gate. Mode 2: sends through existing program.
package tui

import tea "github.com/charmbracelet/bubbletea"

// SendFunc sends a message to the tea.Program (Mode 2).
type SendFunc func(msg tea.Msg)

// BubbleteaInterviewer implements handlers.Interviewer and handlers.FreeformInterviewer.
type BubbleteaInterviewer struct {
	send SendFunc // nil for Mode 1
}

// NewBubbleteaInterviewer creates a Mode 2 interviewer that sends messages through the program.
func NewBubbleteaInterviewer(send SendFunc) *BubbleteaInterviewer {
	return &BubbleteaInterviewer{send: send}
}

// NewMode1Interviewer creates a Mode 1 interviewer that runs inline programs.
func NewMode1Interviewer() *BubbleteaInterviewer {
	return &BubbleteaInterviewer{}
}

// Ask presents a multiple-choice gate prompt and blocks until the user responds.
func (iv *BubbleteaInterviewer) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	replyCh := make(chan string, 1)

	if iv.send != nil {
		// Mode 2: send through existing program
		iv.send(MsgGateChoice{
			Prompt:  prompt,
			Options: choices,
			ReplyCh: replyCh,
		})
		return <-replyCh, nil
	}

	// Mode 1: inline program
	// Read existing tui/interviewer.go for the Mode 1 implementation
	// that spins up a tea.Program with a ChoiceContent model
	return iv.askInline(prompt, choices, defaultChoice)
}

// AskFreeform presents a free-text gate prompt and blocks until the user responds.
func (iv *BubbleteaInterviewer) AskFreeform(prompt string) (string, error) {
	replyCh := make(chan string, 1)

	if iv.send != nil {
		iv.send(MsgGateFreeform{
			Prompt:  prompt,
			ReplyCh: replyCh,
		})
		return <-replyCh, nil
	}

	return iv.askFreeformInline(prompt)
}

// askInline runs a Mode 1 inline choice program.
// Implementer: port from existing tui/interviewer.go Mode 1 logic.
func (iv *BubbleteaInterviewer) askInline(prompt string, choices []string, defaultChoice string) (string, error) {
	replyCh := make(chan string, 1)
	content := NewChoiceContent(prompt, choices, replyCh)
	// Create and run inline tea.Program with this content
	// See existing implementation for details
	_ = content
	return defaultChoice, nil // placeholder
}

// askFreeformInline runs a Mode 1 inline freeform program.
func (iv *BubbleteaInterviewer) askFreeformInline(prompt string) (string, error) {
	replyCh := make(chan string, 1)
	content := NewFreeformContent(prompt, replyCh)
	_ = content
	return "", nil // placeholder
}
```

**Critical:** The Mode 1 inline program logic is non-trivial. The implementer MUST read `tui/interviewer.go` lines 80-186 in the existing code and port the inline `tea.Program` creation, the wrapper model, and the terminal restore logic.

- [ ] **Step 4: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestInterviewer -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tui/interviewer.go tui/interviewer_test.go
git commit -m "feat(tui): add BubbleteaInterviewer gate bridge"
```

---

## Chunk 3: Integration

### Task 12: Adapter

Converts raw pipeline/agent/LLM events into typed TUI messages. The only file that imports engine types.

**Files:**
- Create: `tui/adapter.go`

- [ ] **Step 1: Write the test**

Create `tui/adapter_test.go`:

```go
// ABOUTME: Tests for the event adapter layer.
// ABOUTME: Verifies correct mapping from pipeline/agent/LLM events to typed TUI messages.
package tui

import (
	"testing"
	"time"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

func TestAdaptPipelineEvent(t *testing.T) {
	tests := []struct {
		name     string
		evt      pipeline.PipelineEvent
		wantType string
	}{
		{
			name: "stage started",
			evt: pipeline.PipelineEvent{
				Type:   pipeline.EventStageStarted,
				NodeID: "n1",
			},
			wantType: "MsgNodeStarted",
		},
		{
			name: "stage completed",
			evt: pipeline.PipelineEvent{
				Type:   pipeline.EventStageCompleted,
				NodeID: "n1",
			},
			wantType: "MsgNodeCompleted",
		},
		{
			name: "pipeline failed",
			evt: pipeline.PipelineEvent{
				Type:    pipeline.EventPipelineFailed,
				Message: "fatal",
			},
			wantType: "MsgPipelineFailed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := AdaptPipelineEvent(tt.evt)
			if msg == nil {
				t.Fatal("expected non-nil message")
			}
			// Type check based on expected
			switch tt.wantType {
			case "MsgNodeStarted":
				if _, ok := msg.(MsgNodeStarted); !ok {
					t.Errorf("expected MsgNodeStarted, got %T", msg)
				}
			case "MsgNodeCompleted":
				if _, ok := msg.(MsgNodeCompleted); !ok {
					t.Errorf("expected MsgNodeCompleted, got %T", msg)
				}
			case "MsgPipelineFailed":
				if _, ok := msg.(MsgPipelineFailed); !ok {
					t.Errorf("expected MsgPipelineFailed, got %T", msg)
				}
			}
		})
	}
}

func TestAdaptLLMTraceEvent(t *testing.T) {
	evt := llm.TraceEvent{
		Kind:     llm.TraceRequestStart,
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
	}
	msgs := AdaptLLMTraceEvent(evt, "n1", false)
	if len(msgs) == 0 {
		t.Fatal("expected at least one message")
	}

	// Should produce MsgLLMRequestStart and MsgThinkingStarted
	var hasRequest, hasThinking bool
	for _, m := range msgs {
		switch m.(type) {
		case MsgLLMRequestStart:
			hasRequest = true
		case MsgThinkingStarted:
			hasThinking = true
		}
	}
	if !hasRequest {
		t.Error("expected MsgLLMRequestStart")
	}
	if !hasThinking {
		t.Error("expected MsgThinkingStarted")
	}
}

func TestAdaptLLMTraceEventVerboseFilter(t *testing.T) {
	evt := llm.TraceEvent{Kind: llm.TraceProviderRaw, Preview: "raw"}

	// Non-verbose: should be empty
	msgs := AdaptLLMTraceEvent(evt, "n1", false)
	if len(msgs) != 0 {
		t.Errorf("expected no messages in non-verbose mode, got %d", len(msgs))
	}

	// Verbose: should produce message
	msgs = AdaptLLMTraceEvent(evt, "n1", true)
	if len(msgs) != 1 {
		t.Errorf("expected 1 message in verbose mode, got %d", len(msgs))
	}
}
```

**Note:** The exact pipeline event types and agent event types need to match the actual types in the codebase. The implementer should read `pipeline/events.go` and `agent/events.go` to get the correct type names.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestAdapt -v`
Expected: FAIL — `AdaptPipelineEvent` undefined

- [ ] **Step 3: Write adapter.go**

```go
// ABOUTME: Event adapter layer — converts raw pipeline/agent/LLM events into typed TUI messages.
// ABOUTME: The only file in the TUI package that imports engine types (pipeline, agent, llm).
package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// AdaptPipelineEvent converts a pipeline event into a typed TUI message.
func AdaptPipelineEvent(evt pipeline.PipelineEvent) tea.Msg {
	switch evt.Type {
	case pipeline.EventStageStarted:
		return MsgNodeStarted{NodeID: evt.NodeID}
	case pipeline.EventStageCompleted:
		return MsgNodeCompleted{NodeID: evt.NodeID, Outcome: "success"}
	case pipeline.EventStageFailed:
		errMsg := evt.Message
		if evt.Err != nil {
			errMsg = evt.Err.Error()
		}
		return MsgNodeFailed{NodeID: evt.NodeID, Error: errMsg}
	case pipeline.EventPipelineCompleted:
		return MsgPipelineCompleted{}
	case pipeline.EventPipelineFailed:
		errMsg := evt.Message
		if evt.Err != nil {
			errMsg = evt.Err.Error()
		}
		return MsgPipelineFailed{Error: errMsg}
	case pipeline.EventPipelineStarted:
		// No specific TUI message for pipeline start — handled at init
		return nil
	default:
		return nil
	}
}

// AdaptAgentEvent converts an agent event into a typed TUI message.
func AdaptAgentEvent(evt agent.Event, nodeID string) tea.Msg {
	switch evt.Type {
	case agent.EventTextDelta:
		return MsgTextChunk{NodeID: nodeID, Text: evt.Text}
	case agent.EventToolCallEnd:
		return MsgToolCallEnd{
			NodeID:   nodeID,
			ToolName: evt.ToolName,
			Output:   evt.ToolOutput,
			Error:    evt.ToolError,
		}
	case agent.EventToolCallStart:
		return MsgToolCallStart{NodeID: nodeID, ToolName: evt.ToolName}
	case agent.EventError:
		errMsg := ""
		if evt.Err != nil {
			errMsg = evt.Err.Error()
		}
		return MsgAgentError{NodeID: nodeID, Error: errMsg}
	default:
		return nil
	}
}

// AdaptLLMTraceEvent converts an LLM trace event into one or more typed TUI messages.
// Returns multiple messages when an event triggers both a display message and a state change
// (e.g., TraceRequestStart triggers both MsgLLMRequestStart and MsgThinkingStarted).
func AdaptLLMTraceEvent(evt llm.TraceEvent, nodeID string, verbose bool) []tea.Msg {
	switch evt.Kind {
	case llm.TraceRequestStart:
		return []tea.Msg{
			MsgLLMRequestStart{NodeID: nodeID, Provider: evt.Provider, Model: evt.Model},
			MsgThinkingStarted{NodeID: nodeID},
		}
	case llm.TraceText:
		return []tea.Msg{
			MsgTextChunk{NodeID: nodeID, Text: evt.Preview},
			MsgThinkingStopped{NodeID: nodeID},
		}
	case llm.TraceReasoning:
		return []tea.Msg{
			MsgReasoningChunk{NodeID: nodeID, Text: evt.Preview},
			MsgThinkingStopped{NodeID: nodeID},
		}
	case llm.TraceFinish:
		return []tea.Msg{
			MsgLLMFinish{NodeID: nodeID},
			MsgThinkingStopped{NodeID: nodeID},
		}
	case llm.TraceToolPrepare:
		return []tea.Msg{
			MsgToolCallStart{NodeID: nodeID, ToolName: evt.ToolName},
			MsgThinkingStopped{NodeID: nodeID},
		}
	case llm.TraceProviderRaw:
		if verbose {
			return []tea.Msg{MsgLLMProviderRaw{NodeID: nodeID, Data: evt.Preview}}
		}
		return nil
	default:
		return nil
	}
}

// TUIEventHandler implements pipeline.PipelineEventHandler by forwarding to a tea.Program.
type TUIEventHandler struct {
	send func(tea.Msg)
}

// NewTUIEventHandler creates a handler that sends adapted messages to the program.
func NewTUIEventHandler(send func(tea.Msg)) *TUIEventHandler {
	return &TUIEventHandler{send: send}
}

// HandlePipelineEvent converts and sends the event.
func (h *TUIEventHandler) HandlePipelineEvent(evt pipeline.PipelineEvent) {
	msg := AdaptPipelineEvent(evt)
	if msg != nil {
		h.send(msg)
	}
}
```

**Important:** Read `pipeline/events.go` and `agent/events.go` to verify the exact event type constants (`EventStageStarted` vs `EventNodeStarted`, etc.). Also check `llm/trace.go` for `TraceKind` values. The code above uses names from the explorer report — verify they match.

- [ ] **Step 4: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestAdapt -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tui/adapter.go tui/adapter_test.go
git commit -m "feat(tui): add event adapter layer"
```

---

### Task 13: App (Orchestrator)

Main `tea.Model` that owns layout, message routing, and tick commands. Under 200 lines.

**Files:**
- Create: `tui/app.go`

- [ ] **Step 1: Write the test**

Create `tui/app_test.go`:

```go
// ABOUTME: Tests for the App orchestrator model.
// ABOUTME: Verifies layout, message routing, and tick command scheduling.
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAppInitReturnsTicks(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}})
	app := NewAppModel(store, "test", "run1")

	cmd := app.Init()
	if cmd == nil {
		t.Error("expected Init to return tick commands")
	}
}

func TestAppRoutesNodeStarted(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}})
	app := NewAppModel(store, "test", "run1")
	app.Init()

	app.Update(MsgNodeStarted{NodeID: "n1"})

	if store.NodeStatus("n1") != NodeRunning {
		t.Errorf("expected node running after MsgNodeStarted")
	}
}

func TestAppViewContainsAllSections(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}})
	app := NewAppModel(store, "my-pipeline", "run-xyz")
	app.Init()
	// Set terminal size
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	view := app.View()

	if !strings.Contains(view, "my-pipeline") {
		t.Error("expected pipeline name in view")
	}
}

func TestAppModalRouting(t *testing.T) {
	store := NewStateStore(nil)
	app := NewAppModel(store, "test", "run1")
	app.Init()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	ch := make(chan string, 1)
	app.Update(MsgGateChoice{
		Prompt:  "Pick",
		Options: []string{"a", "b"},
		ReplyCh: ch,
	})

	if !app.modal.Visible() {
		t.Error("expected modal visible after gate choice")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestApp -v`
Expected: FAIL — `NewAppModel` undefined

- [ ] **Step 3: Write app.go**

Read existing `tui/dashboard/app.go` (492 lines) to understand:
- Layout proportions (header height, node list width, agent log width)
- Window resize handling
- How it composes components
- How it handles gate messages

Create a clean version:

```go
// ABOUTME: App is the root Bubbletea model — layout and message routing only.
// ABOUTME: Composes Header, StatusBar, NodeList, AgentLog, Modal, and ThinkingTracker.
package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// AppModel is the root TUI model.
type AppModel struct {
	store    *StateStore
	header   *Header
	status   *StatusBar
	nodeList *NodeList
	agentLog *AgentLog
	modal    *Modal
	thinking *ThinkingTracker

	width  int
	height int
	ready  bool
}

// NewAppModel creates the root TUI model.
func NewAppModel(store *StateStore, pipelineName, runID string) *AppModel {
	thinking := NewThinkingTracker()
	return &AppModel{
		store:    store,
		header:   NewHeader(store, pipelineName, runID),
		status:   NewStatusBar(store),
		nodeList: NewNodeList(store, thinking, 0),
		agentLog: NewAgentLog(store, thinking, 0),
		modal:    NewModal(80, 24),
		thinking: thinking,
	}
}

// SetVerboseTrace enables verbose LLM trace output.
func (a *AppModel) SetVerboseTrace(v bool) {
	a.agentLog.SetVerboseTrace(v)
}

// SetInitialNodes sets the node list from main.go's buildNodeList.
func (a *AppModel) SetInitialNodes(nodes []NodeEntry) {
	a.store.SetNodes(nodes)
}

// Init starts tick commands. Value receiver required by tea.Model.
func (a AppModel) Init() tea.Cmd {
	return tea.Batch(thinkingTickCmd(), headerTickCmd())
}

// Update routes messages to the store and child components. Value receiver for tea.Model.
func (a AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// Handle global keys first
	switch m := msg.(type) {
	case tea.KeyMsg:
		switch m.String() {
		case "q", "ctrl+c":
			return a, tea.Quit
		case "ctrl+o":
			msg = MsgToggleExpand{}
		}
		// If modal is visible, route keys to modal
		if a.modal.Visible() {
			cmd := a.modal.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return a, tea.Batch(cmds...)
		}

	case tea.WindowSizeMsg:
		a.width = m.Width
		a.height = m.Height
		a.ready = true
		a.relayout()
		return a, nil

	case MsgThinkingTick:
		a.thinking.Tick()
		cmds = append(cmds, thinkingTickCmd())

	case MsgHeaderTick:
		cmds = append(cmds, headerTickCmd())

	case MsgGateChoice:
		a.modal.Show(NewChoiceContent(m.Prompt, m.Options, m.ReplyCh))
		return a, nil

	case MsgGateFreeform:
		a.modal.Show(NewFreeformContent(m.Prompt, m.ReplyCh))
		return a, nil

	case MsgPipelineDone:
		// Pipeline finished — could show summary or quit
		return a, nil
	}

	// Update state store
	a.store.Apply(msg)

	// Update focused node for agent log
	if active := a.store.ActiveNode(); active != "" {
		a.agentLog.SetFocusedNode(active)
	}

	// Route to child components
	cmds = append(cmds, a.header.Update(msg))
	cmds = append(cmds, a.nodeList.Update(msg))
	cmds = append(cmds, a.agentLog.Update(msg))

	return a, tea.Batch(cmds...)
}

// View composes the full TUI layout. Value receiver for tea.Model.
func (a AppModel) View() string {
	if !a.ready {
		return "Initializing..."
	}

	headerView := a.header.View()
	statusView := a.status.View()
	nodeView := a.nodeList.View()
	logView := a.agentLog.View()

	// Layout: header on top, status on bottom, node list left, agent log right
	nodeWidth := a.width / 4
	logWidth := a.width - nodeWidth - 1 // -1 for border

	nodePanel := lipgloss.NewStyle().
		Width(nodeWidth).
		Height(a.height - 3). // -1 header, -1 status, -1 border
		Render(nodeView)

	logPanel := lipgloss.NewStyle().
		Width(logWidth).
		Height(a.height - 3).
		BorderLeft(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(ColorBorder).
		Render(logView)

	body := lipgloss.JoinHorizontal(lipgloss.Top, nodePanel, logPanel)

	full := lipgloss.JoinVertical(lipgloss.Left,
		headerView,
		body,
		statusView,
	)

	// Modal overlay
	if a.modal.Visible() {
		return a.modal.View(full)
	}

	return full
}

func (a *AppModel) relayout() {
	a.header.SetWidth(a.width)
	a.status.SetWidth(a.width)

	nodeWidth := a.width / 4
	logWidth := a.width - nodeWidth - 1
	bodyHeight := a.height - 3

	a.nodeList.SetSize(nodeWidth, bodyHeight)
	a.agentLog.SetSize(logWidth, bodyHeight)
	a.modal.SetSize(a.width, a.height)
}
```

**Important:** Read the existing `tui/dashboard/app.go` for:
- Exact layout proportions (the 1/4 vs 3/4 split may differ)
- How it handles `PipelineDoneMsg` (quitting behavior)
- Edge case handling for very small terminals
- How it passes node list to the store on init

The code above is a template. The implementer must verify layout proportions match the existing TUI.

- [ ] **Step 4: Run tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./tui/ -run TestApp -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tui/app.go tui/app_test.go
git commit -m "feat(tui): add App orchestrator model"
```

---

### Task 14: Integration — Wire Up main.go

Restore `runTUI()` in `cmd/tracker/main.go` using the new TUI package. Old files were already deleted in Task 0.

**Files:**
- Modify: `cmd/tracker/main.go`

- [ ] **Step 1: Verify the new TUI compiles**

Run: `cd /Users/harper/Public/src/2389/tracker && go build ./tui/`
Expected: SUCCESS

- [ ] **Step 2: Read main.go and identify every TUI callsite**

Read `cmd/tracker/main.go` fully. The following callsites need updating (reference the `tui-old-backup` branch for the original code):

| Old code | New code |
|----------|----------|
| `import "tui/dashboard"` | Remove — everything is in `tui` package |
| `dashboard.NewAppModel(name, tokenTracker)` | `tui.NewAppModel(tui.NewStateStore(tokenTracker), name, "")` |
| `appModel.SetInitialNodes(nodeList)` | `appModel.SetInitialNodes(nodeList)` — convert `[]dashboard.NodeEntry` → `[]tui.NodeEntry` |
| `dashboard.NodeEntry{ID, Label, Status}` | `tui.NodeEntry{ID, Label}` — status is managed by StateStore |
| `dashboard.NodeDone / NodePending / NodeRunning` | `tui.NodeDone / NodePending / NodeRunning` |
| `tui.NewBubbleteaInterviewerMode2(prog)` | `tui.NewBubbleteaInterviewer(prog.Send)` |
| `tui.NewBubbleteaInterviewer()` | `tui.NewMode1Interviewer()` |
| `prog.Send(dashboard.AgentEventMsg{Event: evt})` | `msg := tui.AdaptAgentEvent(evt, nodeID); if msg != nil { prog.Send(msg) }` |
| `prog.Send(dashboard.LLMTraceMsg{Event: evt})` | `for _, m := range tui.AdaptLLMTraceEvent(evt, nodeID, verbose) { prog.Send(m) }` |
| `prog.Send(dashboard.PipelineEventMsg{Event: evt})` | Use `tui.NewTUIEventHandler(prog.Send)` as pipeline event handler |
| `prog.Send(dashboard.PipelineDoneMsg{Err: err})` | `prog.Send(tui.MsgPipelineDone{Err: err})` |
| `chooseInterviewer(isTerminal)` | Update to use `NewMode1Interviewer()` for non-TUI mode |

- [ ] **Step 3: Update main.go imports and function calls**

Apply all changes from the table above. The `buildNodeList` function should return `[]tui.NodeEntry` instead of `[]dashboard.NodeEntry`. Remove all `tui/dashboard` imports.

- [ ] **Step 4: Update `chooseInterviewer` function**

The Mode 1 constructor changes from `tui.NewBubbleteaInterviewer()` to `tui.NewMode1Interviewer()`. Update `chooseInterviewer` accordingly.

- [ ] **Step 4: Run full test suite**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./... -v`
Expected: PASS (both new TUI tests and all existing tests)

- [ ] **Step 5: Verify clean build**

Run: `cd /Users/harper/Public/src/2389/tracker && go build ./... && go test ./...`
Expected: SUCCESS — no references to deleted packages remain

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat(tui): complete TUI rewrite with thinking indicators

Clean-room rewrite replacing ~4,590 lines across 12 files with ~2,090
lines in a flat tui/ package. Same visual design, robust internals.

New features:
- Thinking spinner on active node lamps (◐ ◓ ◑ ◒)
- 'Thinking... (3.2s)' indicator in agent log
- Typed message constants (no string comparisons)
- Central StateStore with Apply(msg) pattern
- Reusable ScrollView (shared by NodeList + AgentLog)
- Proper modal overlay using lipgloss Place
- Shared StyleRegistry (no duplicated styles)"
```

---

## Post-Implementation Checklist

After all tasks are complete:

- [ ] Run `go test ./... -race` to check for data races
- [ ] Run the TUI manually with a real pipeline to verify visual output matches
- [ ] Test expand/collapse (`ctrl+o`) with real tool output
- [ ] Test gate modals (both choice and freeform) if a pipeline has gates
- [ ] Test with `--verbose` flag to verify trace output
- [ ] Verify token/cost display in header with a real LLM run
- [ ] Test thinking indicator appears during LLM calls
