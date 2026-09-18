// ABOUTME: Tests for #652 — a tool node exiting non-zero carries a bounded
// ABOUTME: FailureReason (exit code + stderr tail) so stage_failed / TUI / diagnose show WHY.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/pipeline"
)

func runToolNode(t *testing.T, command string) pipeline.Outcome {
	t.Helper()
	requireSh(t)
	h := NewToolHandler(exec.NewLocalEnvironment(t.TempDir()))
	node := &pipeline.Node{
		ID:    "Step",
		Shape: "parallelogram",
		Attrs: map[string]string{"tool_command": command},
	}
	outcome, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("Execute returned handler error %v; want a routable outcome", err)
	}
	return outcome
}

// TestToolFailureReason_ExitCodeAndStderrTail: exit 1 with stderr yields
// "exit 1: <stderr tail>" without touching ctx.tool_stdout / tool_stderr.
func TestToolFailureReason_ExitCodeAndStderrTail(t *testing.T) {
	outcome := runToolNode(t, "echo out-line; echo 'line one' >&2; echo 'line two' >&2; exit 3")
	if outcome.Status != pipeline.OutcomeFail {
		t.Fatalf("Status = %q, want fail", outcome.Status)
	}
	if !strings.HasPrefix(outcome.FailureReason, "exit 3: ") {
		t.Errorf("FailureReason = %q, want prefix %q", outcome.FailureReason, "exit 3: ")
	}
	if !strings.Contains(outcome.FailureReason, "line one") || !strings.Contains(outcome.FailureReason, "line two") {
		t.Errorf("FailureReason = %q, want the stderr tail", outcome.FailureReason)
	}
	if strings.Contains(outcome.FailureReason, "out-line") {
		t.Errorf("FailureReason = %q, must prefer stderr over stdout when stderr is non-empty", outcome.FailureReason)
	}
	if got := outcome.ContextUpdates[pipeline.ContextKeyToolStdout]; got != "out-line" {
		t.Errorf("tool_stdout = %q, want unchanged capture %q", got, "out-line")
	}
	if got := outcome.ContextUpdates[pipeline.ContextKeyToolStderr]; got != "line one\nline two" {
		t.Errorf("tool_stderr = %q, want unchanged capture", got)
	}
}

// TestToolFailureReason_StdoutTailWhenStderrEmpty: with an empty stderr the
// reason falls back to the stdout tail; with neither it is just the exit code.
func TestToolFailureReason_StdoutTailWhenStderrEmpty(t *testing.T) {
	outcome := runToolNode(t, "echo 'BUILD ABORTED: nothing shipped'; exit 1")
	if want := "exit 1: BUILD ABORTED: nothing shipped"; outcome.FailureReason != want {
		t.Errorf("FailureReason = %q, want %q", outcome.FailureReason, want)
	}
	outcome = runToolNode(t, "exit 7")
	if want := "exit 7"; outcome.FailureReason != want {
		t.Errorf("FailureReason = %q, want %q", outcome.FailureReason, want)
	}
	outcome = runToolNode(t, "echo ok")
	if outcome.FailureReason != "" {
		t.Errorf("FailureReason = %q on success, want empty", outcome.FailureReason)
	}
}

// TestToolFailureReason_IsBounded: the tail is capped (last lines, ~512 bytes)
// so a 64KB stderr capture never lands whole in a stage_failed error.
func TestToolFailureReason_IsBounded(t *testing.T) {
	outcome := runToolNode(t, "i=0; while [ $i -lt 400 ]; do echo \"stderr line number $i padding padding padding\" >&2; i=$((i+1)); done; exit 1")
	if len(outcome.FailureReason) > failureReasonTailBytes+64 {
		t.Errorf("FailureReason is %d bytes, want <= ~%d", len(outcome.FailureReason), failureReasonTailBytes)
	}
	if !strings.Contains(outcome.FailureReason, "stderr line number 399") {
		t.Errorf("FailureReason = %q, want the LAST lines of stderr", outcome.FailureReason)
	}
	if strings.Contains(outcome.FailureReason, "stderr line number 0 ") {
		t.Errorf("FailureReason = %q, must drop the head of stderr", outcome.FailureReason)
	}
	if n := strings.Count(outcome.FailureReason, "\n"); n >= failureReasonTailLines {
		t.Errorf("FailureReason has %d newlines, want < %d lines", n, failureReasonTailLines)
	}
}

// TestToolFailureReason_ReachesStageFailedAndJSONL: the reason rides #642's
// failureReasonErr onto stage_failed.Err and the activity log's `error` field.
func TestToolFailureReason_ReachesStageFailedAndJSONL(t *testing.T) {
	requireSh(t)
	g := pipeline.NewGraph("tool-failure-reason")
	g.AddNode(&pipeline.Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&pipeline.Node{ID: "Build", Shape: "parallelogram", Attrs: map[string]string{
		"tool_command": "echo 'go: missing module' >&2; exit 2",
	}})
	g.AddNode(&pipeline.Node{ID: "done", Shape: "Msquare"})
	g.AddEdge(&pipeline.Edge{From: "start", To: "Build"})
	g.AddEdge(&pipeline.Edge{From: "Build", To: "done"})

	t.Setenv("TRACKER_AUDIT_DIR", t.TempDir())
	artifactDir := t.TempDir()
	jsonl := pipeline.NewJSONLEventHandler(artifactDir)
	var mu sync.Mutex
	var events []pipeline.PipelineEvent
	emitter := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		mu.Lock()
		events = append(events, evt)
		mu.Unlock()
		jsonl.HandlePipelineEvent(evt)
	})
	reg := NewDefaultRegistry(g, WithExecEnvironment(exec.NewLocalEnvironment(t.TempDir())))
	engine := pipeline.NewEngine(g, reg, pipeline.WithPipelineEventHandler(emitter))
	result, err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected the strict-failure halt error")
	}
	if err := jsonl.Close(); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(artifactDir, result.RunID, "activity.jsonl")

	mu.Lock()
	defer mu.Unlock()
	var failed []pipeline.PipelineEvent
	for _, evt := range events {
		if evt.Type == pipeline.EventStageFailed && evt.NodeID == "Build" {
			failed = append(failed, evt)
		}
	}
	if len(failed) == 0 {
		t.Fatal("no stage_failed for Build")
	}
	for _, evt := range failed {
		if evt.Err == nil {
			t.Fatalf("stage_failed %q has nil Err; want the tool failure reason", evt.Message)
		}
		if got := evt.Err.Error(); !strings.Contains(got, "exit 2") || !strings.Contains(got, "go: missing module") {
			t.Errorf("stage_failed.Err = %q, want exit code and stderr tail", got)
		}
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimPrefix(line, pipeline.ActivityLogSentinel)
		if line == "" {
			continue
		}
		var entry struct {
			Type   string `json:"type"`
			NodeID string `json:"node_id"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("bad jsonl line %q: %v", line, err)
		}
		if entry.Type == "stage_failed" && entry.NodeID == "Build" {
			found = true
			if !strings.Contains(entry.Error, "exit 2: go: missing module") {
				t.Errorf("jsonl error = %q, want %q", entry.Error, "exit 2: go: missing module")
			}
		}
	}
	if !found {
		t.Fatal("no stage_failed jsonl line for Build")
	}
}

// TestFailureReasonTail_Table pins the pure helper's bounding rules.
func TestFailureReasonTail_Table(t *testing.T) {
	long := strings.Repeat("x", failureReasonTailBytes*2)
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"one line", "boom", "boom"},
		{"last N lines", "a\nb\nc\nd\ne\nf", "d\ne\nf"},
		{"trailing whitespace trimmed", "a\nb\n\n", "a\nb"},
		{"long single line keeps the tail", long, long[len(long)-failureReasonTailBytes:]},
		// 3-byte runes: 512 is not a multiple of 3, so the byte cut lands
		// mid-rune and must advance to the next rune boundary (170 runes).
		{"byte cut lands on a rune boundary", strings.Repeat("€", 200), strings.Repeat("€", failureReasonTailBytes/3)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := failureReasonTail(tc.in)
			if got != tc.want {
				t.Errorf("failureReasonTail(%q) = %q, want %q", fmt.Sprintf("%.20s…", tc.in), got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("failureReasonTail produced invalid UTF-8: %q", got)
			}
		})
	}
}

// TestToolFailureReason_TimeoutWinsOverExitCode: a timed-out command also
// exits non-zero (killed); the timeout reason must be the one reported.
func TestToolFailureReason_TimeoutWinsOverExitCode(t *testing.T) {
	requireSh(t)
	h := NewToolHandler(exec.NewLocalEnvironment(t.TempDir()))
	node := &pipeline.Node{ID: "Slow", Shape: "parallelogram", Attrs: map[string]string{
		"tool_command": "echo boom >&2; sleep 5", "timeout": "200ms",
	}}
	outcome, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.HasPrefix(outcome.FailureReason, "command timed out") || strings.HasPrefix(outcome.FailureReason, "exit ") {
		t.Errorf("FailureReason = %q, want the timeout reason, not an exit code", outcome.FailureReason)
	}
}
