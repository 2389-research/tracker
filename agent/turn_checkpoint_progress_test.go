// ABOUTME: Tests for #596 — turn snapshots persist cumulative accounting and
// ABOUTME: control-flow state so a resumed node continues rather than resets.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// TestSnapshot_CarriesProgress pins that a snapshot taken mid-node captures the
// cumulative accounting (usage, tool counts) and control-flow guards, not just
// messages.
func TestSnapshot_CarriesProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turns", "Build.json")
	ctx, cancel := context.WithCancel(context.Background())
	interruptTool := &cancelOnNthTool{name: "work", n: 2, cancel: cancel}
	client := &mockCompleter{responses: []*llm.Response{
		toolCallResponse("c1", "work"),
		toolCallResponse("c2", "work"),
		toolCallResponse("c3", "work"),
	}}
	cfg := DefaultConfig()
	cfg.MaxTurns = 10
	cfg.TurnCheckpointPath = path
	sess := mustNewSession(t, client, cfg, WithTools(interruptTool))
	if _, err := sess.Run(ctx, "build it"); err == nil {
		t.Fatal("expected interruption error")
	}

	snap, err := LoadTurnSnapshot(path)
	if err != nil || snap == nil {
		t.Fatalf("expected persisted snapshot, got snap=%v err=%v", snap, err)
	}
	if snap.Progress == nil {
		t.Fatal("snapshot carried no Progress (#596)")
	}
	if snap.Progress.Version != sessionProgressVersion {
		t.Errorf("Progress.Version = %d, want %d", snap.Progress.Version, sessionProgressVersion)
	}
	// Two tool turns completed before the interrupt.
	if got := snap.Progress.ToolCalls["work"]; got != 2 {
		t.Errorf("Progress.ToolCalls[work] = %d, want 2", got)
	}
	// Cumulative input tokens = 10 per completed turn * 2 turns.
	if got := snap.Progress.Usage.InputTokens; got != 20 {
		t.Errorf("Progress.Usage.InputTokens = %d, want 20", got)
	}
	if snap.Progress.Turns != 2 {
		t.Errorf("Progress.Turns = %d, want 2", snap.Progress.Turns)
	}
}

// TestResume_ContinuesAccounting is the #596 acceptance test: after an interrupt
// mid-node, a fresh session resumes and its final stats INCLUDE the
// pre-interruption cost/usage/tool counts rather than starting from zero.
func TestResume_ContinuesAccounting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turns", "Build.json")
	ctx, cancel := context.WithCancel(context.Background())
	interruptTool := &cancelOnNthTool{name: "work", n: 2, cancel: cancel}
	client1 := &mockCompleter{responses: []*llm.Response{
		toolCallResponse("c1", "work"),
		toolCallResponse("c2", "work"),
		toolCallResponse("c3", "work"),
	}}
	cfg := DefaultConfig()
	cfg.MaxTurns = 10
	cfg.TurnCheckpointPath = path
	sess1 := mustNewSession(t, client1, cfg, WithTools(interruptTool))
	if _, err := sess1.Run(ctx, "build it"); err == nil {
		t.Fatal("expected interruption error")
	}

	// Resume: the final turn completes cleanly with no additional token usage, so
	// the resumed result must equal the pre-interruption accounting.
	client2 := &mockCompleter{responses: []*llm.Response{
		{Message: llm.AssistantMessage("all done"), FinishReason: llm.FinishReason{Reason: "stop"}, Usage: llm.Usage{OutputTokens: 1}},
	}}
	sess2 := mustNewSession(t, client2, cfg, WithTools(&stubTool{name: "work", output: "ok"}))
	result, err := sess2.Run(context.Background(), "build it")
	if err != nil {
		t.Fatalf("resume Run: %v", err)
	}
	if got := result.ToolCalls["work"]; got != 2 {
		t.Errorf("resumed ToolCalls[work] = %d, want 2 (pre-interruption work must survive)", got)
	}
	if got := result.Usage.InputTokens; got != 20 {
		t.Errorf("resumed Usage.InputTokens = %d, want 20 (accounting must continue, not reset)", got)
	}
	// The one resumed turn's output token adds on top of the 10 carried.
	if got := result.Usage.OutputTokens; got != 11 {
		t.Errorf("resumed Usage.OutputTokens = %d, want 11 (10 carried + 1 new)", got)
	}
}

// TestSnapshotProgress_ControlFlowRoundTrip pins that the loop/no-progress/retry
// counters serialize and reload through a snapshot and re-seed a fresh turnState.
func TestSnapshotProgress_ControlFlowRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	sess := mustNewSession(t, &mockCompleter{}, DefaultConfig())
	result := &SessionResult{ToolCalls: map[string]int{"edit": 3}, Usage: llm.Usage{InputTokens: 7}, CompactionsApplied: 1}
	ts := &turnState{
		lastToolSignature:      "edit:{}",
		consecutiveLoopCount:   2,
		consecutiveNoToolTurns: 1,
		emptyResponseRetries:   1,
		consecutiveReflected:   2,
		sawEdit:                true,
	}
	tracker := NewContextWindowTracker(1000, 0.8)
	tracker.MarkWarned()

	prog := sess.captureProgress(result, ts, tracker)
	if err := sess.Snapshot(5, "", prog).Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	snap, err := LoadTurnSnapshot(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Re-seed a fresh session's live state from the reloaded progress.
	fresh := mustNewSession(t, &mockCompleter{}, DefaultConfig())
	fresh.resumeProgress = snap.Progress
	gotResult := &SessionResult{ToolCalls: map[string]int{}}
	gotTS := &turnState{}
	gotTracker := NewContextWindowTracker(1000, 0.8)
	fresh.applyResumeProgress(gotResult, gotTracker, gotTS)

	if gotTS.consecutiveLoopCount != 2 || gotTS.lastToolSignature != "edit:{}" {
		t.Errorf("loop guard not restored: %+v", gotTS)
	}
	if gotTS.consecutiveNoToolTurns != 1 || gotTS.emptyResponseRetries != 1 {
		t.Errorf("no-progress/empty-retry counters not restored: %+v", gotTS)
	}
	if !gotTS.sawEdit {
		t.Error("sawEdit not restored")
	}
	if !gotTracker.WarningEmitted {
		t.Error("context-window warned flag not restored")
	}
	if gotResult.ToolCalls["edit"] != 3 || gotResult.CompactionsApplied != 1 {
		t.Errorf("accounting not restored: %+v", gotResult)
	}
}

// TestLoadTurnSnapshot_V1Accepted pins the #596 back-compat policy: a pre-#596
// (schema 1) snapshot with no Progress still loads, and a resume from it zeroes
// progress rather than erroring.
func TestLoadTurnSnapshot_V1Accepted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.json")
	v1 := []byte(`{"schema":1,"session_id":"old","turn":2,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	if err := os.WriteFile(path, v1, 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := LoadTurnSnapshot(path)
	if err != nil {
		t.Fatalf("v1 snapshot must still load, got: %v", err)
	}
	if snap == nil || snap.Progress != nil {
		t.Fatalf("v1 snapshot must carry no Progress, got snap=%+v", snap)
	}
	// Resume from it: applyResumeProgress is a no-op, guards start fresh.
	sess := mustNewSession(t, &mockCompleter{}, DefaultConfig())
	sess.RestoreFrom(snap)
	if sess.resumeProgress != nil {
		t.Error("v1 restore must leave resumeProgress nil (progress zeroed)")
	}
	r := &SessionResult{ToolCalls: map[string]int{}}
	ts := &turnState{}
	sess.applyResumeProgress(r, NewContextWindowTracker(1, 0.8), ts)
	if r.Usage.InputTokens != 0 || ts.consecutiveLoopCount != 0 {
		t.Error("v1 resume must zero progress")
	}
}
