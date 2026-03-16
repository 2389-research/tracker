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
		t.Errorf("expected node running, got %d", store.NodeStatus("n1"))
	}
}

func TestAppRoutesNodeCompleted(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}})
	app := NewAppModel(store, "test", "run1")
	app.Init()
	app.Update(MsgNodeCompleted{NodeID: "n1", Outcome: "success"})
	if store.NodeStatus("n1") != NodeDone {
		t.Errorf("expected node done, got %d", store.NodeStatus("n1"))
	}
}

func TestAppRoutesPipelineFailed(t *testing.T) {
	store := NewStateStore(nil)
	app := NewAppModel(store, "test", "run1")
	app.Init()
	app.Update(MsgPipelineFailed{Error: "fatal"})
	if !store.PipelineDone() {
		t.Error("expected pipeline done after failure")
	}
	if store.PipelineError() != "fatal" {
		t.Errorf("expected error 'fatal', got %s", store.PipelineError())
	}
}

func TestAppViewContainsAllSections(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}})
	app := NewAppModel(store, "my-pipeline", "run-xyz")
	app.Init()
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
	app.Update(MsgGateChoice{Prompt: "Pick", Options: []string{"a", "b"}, ReplyCh: ch})
	if !app.modal.Visible() {
		t.Error("expected modal visible after gate choice")
	}
}

func TestAppFreeformModalRouting(t *testing.T) {
	store := NewStateStore(nil)
	app := NewAppModel(store, "test", "run1")
	app.Init()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	ch := make(chan string, 1)
	app.Update(MsgGateFreeform{Prompt: "Enter value", ReplyCh: ch})
	if !app.modal.Visible() {
		t.Error("expected modal visible after gate freeform")
	}
}

func TestAppModalChoiceEnterDismisses(t *testing.T) {
	store := NewStateStore(nil)
	app := NewAppModel(store, "test", "run1")
	app.Init()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	ch := make(chan string, 1)
	app.Update(MsgGateChoice{Prompt: "Pick", Options: []string{"a", "b"}, ReplyCh: ch})
	if !app.modal.Visible() {
		t.Fatal("expected modal visible after gate choice")
	}
	// Press Enter to select
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	// Verify reply was sent
	select {
	case val := <-ch:
		if val != "a" {
			t.Errorf("expected selection 'a', got %q", val)
		}
	default:
		t.Fatal("expected reply on channel after Enter")
	}
	// The cmd should produce MsgModalDismiss
	if cmd == nil {
		t.Fatal("expected dismiss command after Enter")
	}
	// Simulate bubbletea processing the command
	app.Update(MsgModalDismiss{})
	if app.modal.Visible() {
		t.Error("expected modal hidden after dismiss")
	}
}

func TestAppModalFreeformEnterDismisses(t *testing.T) {
	store := NewStateStore(nil)
	app := NewAppModel(store, "test", "run1")
	app.Init()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	ch := make(chan string, 1)
	app.Update(MsgGateFreeform{Prompt: "Enter value", ReplyCh: ch})
	if !app.modal.Visible() {
		t.Fatal("expected modal visible after gate freeform")
	}
	// Type some text then press Enter
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case val := <-ch:
		if val != "hello" {
			t.Errorf("expected 'hello', got %q", val)
		}
	default:
		t.Fatal("expected reply on channel after Enter")
	}
	if cmd == nil {
		t.Fatal("expected dismiss command after Enter")
	}
	app.Update(MsgModalDismiss{})
	if app.modal.Visible() {
		t.Error("expected modal hidden after dismiss")
	}
}

func TestAppQuitKey(t *testing.T) {
	store := NewStateStore(nil)
	app := NewAppModel(store, "test", "run1")
	app.Init()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	_ = model
	// tea.Quit returns a special command; verify it's non-nil
	if cmd == nil {
		t.Error("expected quit command from 'q' key")
	}
}

func TestAppCtrlCKey(t *testing.T) {
	store := NewStateStore(nil)
	app := NewAppModel(store, "test", "run1")
	app.Init()
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Error("expected quit command from ctrl+c")
	}
}

func TestAppThinkingTickRouting(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}})
	app := NewAppModel(store, "test", "run1")
	app.Init()
	_, cmd := app.Update(MsgThinkingTick{})
	// Should return another thinking tick command
	if cmd == nil {
		t.Error("expected tick command after MsgThinkingTick")
	}
}

func TestAppHeaderTickRouting(t *testing.T) {
	store := NewStateStore(nil)
	app := NewAppModel(store, "test", "run1")
	app.Init()
	_, cmd := app.Update(MsgHeaderTick{})
	if cmd == nil {
		t.Error("expected tick command after MsgHeaderTick")
	}
}

func TestAppToggleExpand(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}})
	app := NewAppModel(store, "test", "run1")
	app.Init()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	// ctrl+o should toggle expand
	app.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	// No crash is a pass — the toggle routes to AgentLog
}

func TestAppWindowResize(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}})
	app := NewAppModel(store, "test", "run1")
	app.Init()
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	// Verify view renders without panic at new size
	view := app.View()
	if view == "" {
		t.Error("expected non-empty view after resize")
	}
}
