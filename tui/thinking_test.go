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
	if frames[0] != ThinkingFrames[0] {
		t.Errorf("frame 0: got %q, want %q", frames[0], ThinkingFrames[0])
	}
	if frames[4] != ThinkingFrames[0] {
		t.Errorf("frame 4 should wrap: got %q", frames[4])
	}
}

func TestThinkingTrackerElapsed(t *testing.T) {
	tr := NewThinkingTracker()
	tr.StartAt("n1", time.Now().Add(-3*time.Second))
	elapsed := tr.Elapsed("n1")
	if elapsed < 2*time.Second || elapsed > 5*time.Second {
		t.Errorf("expected ~3s, got %v", elapsed)
	}
}

func TestThinkingTrackerMultipleNodes(t *testing.T) {
	tr := NewThinkingTracker()
	tr.Start("n1")
	tr.Start("n2")
	if !tr.IsThinking("n1") || !tr.IsThinking("n2") {
		t.Error("both should be thinking")
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
	if tr.Frame("n1") != "" {
		t.Errorf("expected empty frame for non-thinking node")
	}
}

func TestThinkingTrackerToolRunning(t *testing.T) {
	tr := NewThinkingTracker()
	if tr.IsToolRunning("n1") {
		t.Error("should not be running tool initially")
	}
	tr.StartTool("n1", "bash")
	if !tr.IsToolRunning("n1") {
		t.Error("should be running tool after StartTool")
	}
	if tr.ToolName("n1") != "bash" {
		t.Errorf("expected tool name 'bash', got %q", tr.ToolName("n1"))
	}
	tr.StopTool("n1")
	if tr.IsToolRunning("n1") {
		t.Error("should not be running tool after StopTool")
	}
}
