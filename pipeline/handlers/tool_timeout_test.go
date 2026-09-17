// ABOUTME: Tests for #644 — a tool node exceeding its timeout is a routable
// ABOUTME: OutcomeFail (stderr "command timed out"), not an unroutable handler error.
package handlers

import (
	"context"
	osexec "os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/pipeline"
)

func requireSh(t *testing.T) {
	t.Helper()
	if _, err := osexec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
}

// TestToolTimeout_IsRoutableFail: `timeout: 300ms` on `sleep 5` yields
// OutcomeFail with ctx.tool_stderr carrying "command timed out after 300ms",
// the captured stdout tail preserved, a ToolTimeout detail for the engine
// event, and the handler returns in ~timeout, not ~5 s (the process group is
// killed).
func TestToolTimeout_IsRoutableFail(t *testing.T) {
	requireSh(t)
	h := NewToolHandler(exec.NewLocalEnvironment(t.TempDir()))
	node := &pipeline.Node{
		ID:    "TestMilestone",
		Shape: "parallelogram",
		Attrs: map[string]string{
			"tool_command": "echo partial-stdout; echo partial-stderr >&2; sleep 5; echo never",
			"timeout":      "300ms",
		},
	}
	start := time.Now()
	outcome, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Execute returned handler error %v; want a routable OutcomeFail", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("Execute took %v; the subprocess was not killed at the 300ms timeout", elapsed)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Fatalf("Status = %q, want %q", outcome.Status, pipeline.OutcomeFail)
	}
	stderr := outcome.ContextUpdates[pipeline.ContextKeyToolStderr]
	if !strings.Contains(stderr, "command timed out after 300ms") {
		t.Errorf("tool_stderr = %q, want it to contain %q", stderr, "command timed out after 300ms")
	}
	if !strings.Contains(stderr, "partial-stderr") {
		t.Errorf("tool_stderr = %q, want the captured stderr tail preserved", stderr)
	}
	if got := outcome.ContextUpdates[pipeline.ContextKeyToolStdout]; got != "partial-stdout" {
		t.Errorf("tool_stdout = %q, want the captured stdout tail %q", got, "partial-stdout")
	}
	if outcome.Tool.Timeout == nil {
		t.Fatal("outcome.Tool.Timeout is nil; the engine cannot emit tool_timeout")
	}
	if outcome.Tool.Timeout.Timeout != 300*time.Millisecond {
		t.Errorf("Timeout detail = %v, want 300ms", outcome.Tool.Timeout.Timeout)
	}
	if outcome.Tool.Timeout.CapturedBytes == 0 {
		t.Error("Timeout.CapturedBytes = 0, want the captured output size")
	}
}

// TestToolTimeout_ParentCancellationStaysError: when the RUN is cancelled
// (Ctrl+C, --max-wall-time) while a tool is executing, that is not the node's
// timeout — it must still surface as a hard error so the engine's
// cancellation path runs, not as a routable fail that keeps the run going.
func TestToolTimeout_ParentCancellationStaysError(t *testing.T) {
	requireSh(t)
	h := NewToolHandler(exec.NewLocalEnvironment(t.TempDir()))
	node := &pipeline.Node{
		ID:    "slow",
		Shape: "parallelogram",
		Attrs: map[string]string{"tool_command": "sleep 5", "timeout": "10s"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	outcome, err := h.Execute(ctx, node, pipeline.NewPipelineContext())
	if err == nil {
		t.Fatalf("Execute returned outcome %+v with nil error; a cancelled run must be a hard error", outcome)
	}
}

// TestToolTimeout_RoutesOnFailEdge runs the #644 repro through the engine: a
// tool node with `timeout: 300ms` whose `when ctx.outcome = fail` edge leads to
// Fix. Pre-#644 the engine emitted pipeline_failed and Fix was never reached.
func TestToolTimeout_RoutesOnFailEdge(t *testing.T) {
	requireSh(t)
	g := pipeline.NewGraph("tool-timeout-routing")
	g.AddNode(&pipeline.Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&pipeline.Node{ID: "Test", Shape: "parallelogram", Attrs: map[string]string{
		"tool_command": "sleep 5",
		"timeout":      "300ms",
	}})
	g.AddNode(&pipeline.Node{ID: "Fix", Shape: "box"})
	g.AddNode(&pipeline.Node{ID: "done", Shape: "Msquare"})
	g.AddEdge(&pipeline.Edge{From: "start", To: "Test"})
	g.AddEdge(&pipeline.Edge{From: "Test", To: "done", Condition: "ctx.outcome = success"})
	g.AddEdge(&pipeline.Edge{From: "Test", To: "Fix", Condition: "ctx.outcome = fail"})
	g.AddEdge(&pipeline.Edge{From: "Fix", To: "done"})

	var mu sync.Mutex
	fixRan := false
	var seenStderr string
	codergen := func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
		mu.Lock()
		defer mu.Unlock()
		if node.ID == "Fix" {
			fixRan = true
			seenStderr, _ = pctx.Get(pipeline.ContextKeyToolStderr)
		}
		return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
	}
	var events []pipeline.PipelineEvent
	emitter := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, evt)
	})
	reg := NewDefaultRegistry(g, WithCodergenFunc(codergen), WithExecEnvironment(exec.NewLocalEnvironment(t.TempDir())), WithPipelineEventHandler(emitter))
	engine := pipeline.NewEngine(g, reg, pipeline.WithPipelineEventHandler(emitter))

	start := time.Now()
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine.Run: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("run took %v; want ~300ms (subprocess killed at timeout)", time.Since(start))
	}
	if result.Status != pipeline.OutcomeSuccess {
		t.Fatalf("status = %q, want success via the fail edge -> Fix -> done", result.Status)
	}
	mu.Lock()
	defer mu.Unlock()
	if !fixRan {
		t.Fatal("Fix never ran — the timeout did not route on `when ctx.outcome = fail`")
	}
	if !strings.Contains(seenStderr, "timed out") {
		t.Errorf("Fix saw tool_stderr %q, want the timeout message", seenStderr)
	}
	timeouts := gateEventsOfType(events, pipeline.EventToolTimeout)
	if len(timeouts) != 1 {
		t.Fatalf("tool_timeout events = %d, want 1", len(timeouts))
	}
	if timeouts[0].NodeID != "Test" || timeouts[0].ToolTimeout == nil || timeouts[0].ToolTimeout.Timeout != 300*time.Millisecond {
		t.Errorf("tool_timeout event = %+v, want NodeID=Test with a 300ms ToolTimeout payload", timeouts[0])
	}
}

// TestToolTimeout_StrictFailureStillStops: a timeout is an ordinary fail, so a
// tool node with only unconditional edges stops the pipeline (strict failure
// edges) exactly as a non-zero exit does.
func TestToolTimeout_StrictFailureStillStops(t *testing.T) {
	requireSh(t)
	g := pipeline.NewGraph("tool-timeout-strict")
	g.AddNode(&pipeline.Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&pipeline.Node{ID: "Setup", Shape: "parallelogram", Attrs: map[string]string{
		"tool_command": "sleep 5",
		"timeout":      "300ms",
	}})
	g.AddNode(&pipeline.Node{ID: "Build", Shape: "box"})
	g.AddNode(&pipeline.Node{ID: "done", Shape: "Msquare"})
	g.AddEdge(&pipeline.Edge{From: "start", To: "Setup"})
	g.AddEdge(&pipeline.Edge{From: "Setup", To: "Build"})
	g.AddEdge(&pipeline.Edge{From: "Build", To: "done"})

	buildRan := false
	codergen := func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
		buildRan = true
		return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
	}
	reg := NewDefaultRegistry(g, WithCodergenFunc(codergen), WithExecEnvironment(exec.NewLocalEnvironment(t.TempDir())))
	result, err := pipeline.NewEngine(g, reg).Run(context.Background())
	if err == nil {
		t.Fatal("expected the strict-failure halt error")
	}
	if result.Status != pipeline.OutcomeFail {
		t.Errorf("status = %q, want fail", result.Status)
	}
	if buildRan {
		t.Error("Build ran after Setup timed out — strict failure edges must stop the run")
	}
}
