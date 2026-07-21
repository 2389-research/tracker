// ABOUTME: Tests for the StatusBar component.
// ABOUTME: Verifies track diagram, progress summary, and keybinding hints.
package tui

import (
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func TestStatusBarProgress(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}, {ID: "n3"}})
	store.Apply(MsgNodeCompleted{NodeID: "n1"})
	sb := NewStatusBar(store, nil)
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
	sb := NewStatusBar(store, nil)
	view := sb.View()
	if !strings.Contains(view, LampDone) {
		t.Errorf("expected done lamp in track diagram, got: %s", view)
	}
	if !strings.Contains(view, LampFailed) {
		t.Errorf("expected failed lamp in track diagram, got: %s", view)
	}
}

func TestStatusBarKeybindingHints(t *testing.T) {
	store := NewStateStore(nil)
	sb := NewStatusBar(store, nil)
	view := sb.View()
	if !strings.Contains(view, "quit") {
		t.Errorf("expected keybinding hint, got: %s", view)
	}
}

// TestCompletionRow_Success: green check + "Completed".
func TestCompletionRow_Success(t *testing.T) {
	row := CompletionRow(pipeline.OutcomeSuccess, nil, "")
	if !strings.Contains(row, "Completed") {
		t.Errorf("expected 'Completed' text, got: %q", row)
	}
	if !strings.Contains(row, LampDone) {
		t.Errorf("expected done lamp glyph, got: %q", row)
	}
	if strings.Contains(row, "validation override") {
		t.Errorf("success row must not mention override, got: %q", row)
	}
}

// TestCompletionRow_OverrideRendersAmber covers the headline copy for the
// validation_overridden terminal state (Gap 5.2 D17 + D18). The bullet glyph
// and textual gate/label/actor signal must be present so the row remains
// distinguishable in NO_COLOR / monochrome terminals — that's the only
// guarantee the renderer can make in a non-TTY test environment (lipgloss
// strips ANSI when stdout isn't a terminal). The amber-hex color signal is
// asserted separately in TestColorOverride_MatchesBrandingHex against the
// ColorOverride constant itself.
func TestCompletionRow_OverrideRendersAmber(t *testing.T) {
	override := &pipeline.OverrideDetail{
		GateNodeID: "ApproveGate",
		Label:      "force-merge",
		Actor:      pipeline.ActorAutopilot,
	}
	row := CompletionRow(pipeline.OutcomeValidationOverridden, override, "")
	// Textual signal — survives NO_COLOR.
	if !strings.Contains(row, "validation override at ApproveGate") {
		t.Errorf("expected gate in row, got: %q", row)
	}
	if !strings.Contains(row, `label "force-merge"`) {
		t.Errorf("expected quoted label, got: %q", row)
	}
	if !strings.Contains(row, "by autopilot") {
		t.Errorf("expected actor by autopilot, got: %q", row)
	}
	if !strings.Contains(row, LampDone) {
		t.Errorf("expected done lamp glyph (signal-preserving), got: %q", row)
	}
}

// TestColorOverride_MatchesBrandingHex pins the amber color constant to
// Tailwind amber-600 (#D97706), matching cmd/tracker/branding.go's
// colorOverride. Since tui can't import the cmd/tracker package (would create
// an import cycle: cmd → tui, not tui → cmd), the constant is duplicated;
// this test is the canary that catches drift.
func TestColorOverride_MatchesBrandingHex(t *testing.T) {
	want := "#D97706"
	if got := string(ColorOverride); got != want {
		t.Errorf("ColorOverride drifted from branding.go: got %q, want %q (Gap 5.2 D18)", got, want)
	}
}

// TestCompletionRow_OverrideNilFallback ensures the renderer doesn't crash on
// a defensive nil-override input for OutcomeValidationOverridden (shouldn't
// happen via normal flow but the path exists).
func TestCompletionRow_OverrideNilFallback(t *testing.T) {
	row := CompletionRow(pipeline.OutcomeValidationOverridden, nil, "")
	if !strings.Contains(row, "validation override") {
		t.Errorf("expected fallback text, got: %q", row)
	}
}

// TestCompletionRow_BudgetExceeded: red ✗ + "Budget exceeded".
func TestCompletionRow_BudgetExceeded(t *testing.T) {
	row := CompletionRow(pipeline.OutcomeBudgetExceeded, nil, "")
	if !strings.Contains(row, "Budget exceeded") {
		t.Errorf("expected 'Budget exceeded' text, got: %q", row)
	}
	if !strings.Contains(row, LampFailed) {
		t.Errorf("expected failed lamp glyph, got: %q", row)
	}
}

// TestCompletionRow_PausedBilling: a recoverable pause, not a failure.
func TestCompletionRow_PausedBilling(t *testing.T) {
	row := CompletionRow(pipeline.OutcomePausedBilling, nil, "")
	if !strings.Contains(row, "Paused") {
		t.Errorf("expected 'Paused' text, got: %q", row)
	}
	if strings.Contains(row, LampFailed) {
		t.Errorf("a pause should not use the failed lamp, got: %q", row)
	}
}

// TestCompletionRow_Fail: red ✗ + "Failed" (+ optional error).
func TestCompletionRow_Fail(t *testing.T) {
	row := CompletionRow(pipeline.OutcomeFail, nil, "node X errored")
	if !strings.Contains(row, "Failed") {
		t.Errorf("expected 'Failed' text, got: %q", row)
	}
	if !strings.Contains(row, "node X errored") {
		t.Errorf("expected error detail, got: %q", row)
	}
	if !strings.Contains(row, LampFailed) {
		t.Errorf("expected failed lamp glyph, got: %q", row)
	}
}

// TestStatusBar_CompletionRowReplacesProgress verifies that once the pipeline
// has reached a terminal state the status bar swaps the progress region for
// the completion row, so operators see the final classification at a glance.
func TestStatusBar_CompletionRowReplacesProgress(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}})
	store.Apply(MsgNodeCompleted{NodeID: "n1"})
	store.Apply(MsgValidationOverridden{
		NodeID: "Gate",
		Detail: pipeline.OverrideDetail{
			GateNodeID: "Gate",
			Label:      "approve",
			Actor:      pipeline.ActorHuman,
		},
	})
	store.Apply(MsgPipelineCompleted{})
	sb := NewStatusBar(store, nil)
	view := sb.View()
	if !strings.Contains(view, "validation override at Gate") {
		t.Errorf("expected completion-row override copy in status bar, got: %q", view)
	}
}
