// ABOUTME: EventBackendResolved reaches the TUI as a neutral info line (not a
// ABOUTME: warning) carrying the backend: auto announcement.
package tui

import (
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func TestAdaptPipelineEvent_BackendResolved(t *testing.T) {
	evt := pipeline.PipelineEvent{
		Type:            pipeline.EventBackendResolved,
		NodeID:          "Answer",
		Message:         "backend: auto → claude-code (claude 2.1.280 at /usr/bin/claude; no API key needed)",
		BackendResolved: &pipeline.BackendResolvedDetail{Backend: "claude-code"},
	}
	msg, ok := AdaptPipelineEvent(evt).(MsgNodeInfo)
	if !ok {
		t.Fatalf("got %T, want MsgNodeInfo", AdaptPipelineEvent(evt))
	}
	if msg.NodeID != "Answer" || msg.Message != evt.Message {
		t.Fatalf("msg = %+v", msg)
	}
}

func TestAgentLog_NodeInfoLine(t *testing.T) {
	al := NewAgentLog(NewStateStore(nil), NewThinkingTracker(), 20)
	al.Update(MsgNodeInfo{NodeID: "Answer", Message: "backend: auto → native (provider anthropic)"})
	plain := stripANSI(al.View())
	if !strings.Contains(plain, "backend: auto → native (provider anthropic)") {
		t.Fatalf("expected the info line, got: %s", plain)
	}
	if strings.Contains(plain, "WARNING") {
		t.Fatalf("an info line must not render as a warning: %s", plain)
	}
}
