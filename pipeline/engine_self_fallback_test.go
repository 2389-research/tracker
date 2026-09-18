// ABOUTME: Tests for #650 — a fallback that resolves to the failing node itself is
// ABOUTME: a no-op (no re-entry, no latch, no event) and a fallback-reached node names its origin.
package pipeline

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// selfFallbackGraph builds start -> A -> AbortRun with the graph-level
// on_failure pointing at AbortRun — build_product's #640 A2 shape. withExit
// adds the `AbortRun -> Done` edge dippin's termination lint wants; without it
// AbortRun is a bare abort terminal (exit 1, no outgoing edges).
func selfFallbackGraph(withExit bool) *Graph {
	g := NewGraph("self-fallback")
	g.Attrs["fallback_target"] = "AbortRun"
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "A", Shape: "parallelogram"})
	g.AddNode(&Node{ID: "AbortRun", Shape: "parallelogram"})
	g.AddNode(&Node{ID: "Done", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "start", To: "A"})
	g.AddEdge(&Edge{From: "A", To: "AbortRun"})
	if withExit {
		g.AddEdge(&Edge{From: "AbortRun", To: "Done"})
	}
	return g
}

func selfFallbackRun(t *testing.T, g *Graph, opts ...EngineOption) (*EngineResult, error, []string, []PipelineEvent) {
	t.Helper()
	var visits []string
	reg := newTestRegistry()
	reg.Register(&testHandler{name: "tool", executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		visits = append(visits, node.ID)
		switch node.ID {
		case "A":
			return Outcome{Status: OutcomeFail, FailureReason: "exit 1: Setup.sh: SPEC.md not found"}, nil
		case "AbortRun":
			return Outcome{Status: OutcomeFail, FailureReason: "exit 1: BUILD ABORTED: nothing was shipped"}, nil
		}
		return Outcome{Status: OutcomeSuccess}, nil
	}})
	var events []PipelineEvent
	opts = append(opts, WithPipelineEventHandler(PipelineEventHandlerFunc(func(evt PipelineEvent) { events = append(events, evt) })))
	engine := NewEngine(g, reg, opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := engine.Run(ctx)
	if ctx.Err() != nil {
		t.Fatal("run did not terminate")
	}
	return result, err, visits, events
}

func stageFailedFor(events []PipelineEvent, nodeID string) []PipelineEvent {
	var out []PipelineEvent
	for _, evt := range events {
		if evt.Type == EventStageFailed && evt.NodeID == nodeID {
			out = append(out, evt)
		}
	}
	return out
}

// TestStrictFailureSelfFallbackIsNoop: when the resolved fallback IS the
// failing node, the engine must not re-enter it — no latch consumed, no
// fallback_latched event, a single strict-failure halt naming the origin.
func TestStrictFailureSelfFallbackIsNoop(t *testing.T) {
	for _, tc := range []struct {
		name     string
		withExit bool
	}{
		{"AbortRun -> Done (build_product shape)", true},
		{"bare abort terminal (no outgoing edges)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := selfFallbackGraph(tc.withExit)
			result, err, visits, events := selfFallbackRun(t, g)

			if got, want := strings.Join(visits, ","), "A,AbortRun"; got != want {
				t.Fatalf("visits = %s, want exactly %s (self-fallback must not re-enter AbortRun)", got, want)
			}
			if err == nil || result == nil || result.Status != OutcomeFail {
				t.Fatalf("run must end fail: err=%v status=%v", err, statusOf(result))
			}
			if n := countEvents(events, EventFallbackLatched, "AbortRun"); n != 0 {
				t.Errorf("fallback_latched for AbortRun = %d, want 0 (a self-target is no fallback)", n)
			}
			// A: `node failed` + `routing to fallback "AbortRun"`, both carrying the reason.
			aFailed := stageFailedFor(events, "A")
			if len(aFailed) != 2 {
				t.Fatalf("stage_failed for A = %d, want 2 (failed + routing): %+v", len(aFailed), aFailed)
			}
			for _, evt := range aFailed {
				if evt.Err == nil || !strings.Contains(evt.Err.Error(), "SPEC.md not found") {
					t.Errorf("stage_failed %q for A lacks the FailureReason (got %v)", evt.Message, evt.Err)
				}
			}
			if !strings.Contains(aFailed[1].Message, `routing to fallback "AbortRun"`) {
				t.Errorf("A's second stage_failed = %q, want the fallback routing message", aFailed[1].Message)
			}
			// AbortRun: `node failed` + ONE terminal halt — no self-routing line.
			abortFailed := stageFailedFor(events, "AbortRun")
			if len(abortFailed) != 2 {
				t.Fatalf("stage_failed for AbortRun = %d, want 2 (failed + halt): %+v", len(abortFailed), abortFailed)
			}
			for _, evt := range abortFailed {
				if strings.Contains(evt.Message, "routing to fallback") {
					t.Errorf("AbortRun routed to itself: %q", evt.Message)
				}
				if evt.Err == nil || !strings.Contains(evt.Err.Error(), "BUILD ABORTED") {
					t.Errorf("stage_failed %q for AbortRun lacks the FailureReason (got %v)", evt.Message, evt.Err)
				}
			}
			halt := abortFailed[1]
			if !strings.Contains(halt.Message, "stopping pipeline") {
				t.Errorf("terminal stage_failed = %q, want the strict-failure halt", halt.Message)
			}
			if !strings.Contains(halt.Message, `reached from "A" failure`) {
				t.Errorf("terminal stage_failed = %q, want it to name the originating node", halt.Message)
			}
			if !strings.Contains(err.Error(), `"AbortRun"`) || !strings.Contains(err.Error(), `reached from "A" failure`) {
				t.Errorf("terminal error = %q, want it to name AbortRun AND the originating node A", err)
			}
			if result.Trace.Entries[len(result.Trace.Entries)-1].NodeID != "AbortRun" {
				t.Errorf("last trace entry = %q, want AbortRun", result.Trace.Entries[len(result.Trace.Entries)-1].NodeID)
			}
		})
	}
}

// TestAbortTerminalPreservesWIP: a failing tool node with NO outgoing edges
// halts via the strict-failure path (WIP preservation runs, stage_failed
// carries the reason) instead of the pre-#650 "no outgoing edges from
// non-exit node" invariant error that skipped checkStrictFailure entirely.
func TestAbortTerminalPreservesWIP(t *testing.T) {
	g := NewGraph("abort-terminal")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "A", Shape: "parallelogram"})
	g.AddNode(&Node{ID: "AbortRun", Shape: "parallelogram"})
	g.AddNode(&Node{ID: "Done", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "start", To: "A"})
	g.AddEdge(&Edge{From: "A", To: "AbortRun", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "A", To: "Done", Condition: "ctx.outcome = success"})

	result, err, visits, events := selfFallbackRun(t, g)
	if got, want := strings.Join(visits, ","), "A,AbortRun"; got != want {
		t.Fatalf("visits = %s, want %s", got, want)
	}
	if err == nil || strings.Contains(err.Error(), "no outgoing edges") {
		t.Fatalf("err = %v, want the strict-failure halt, not the no-outgoing-edges invariant error", err)
	}
	if result == nil || result.Status != OutcomeFail {
		t.Fatalf("status = %v, want fail with a result (the halt is a recognized terminal)", statusOf(result))
	}
	abortFailed := stageFailedFor(events, "AbortRun")
	if len(abortFailed) != 2 || !strings.Contains(abortFailed[1].Message, "stopping pipeline") {
		t.Fatalf("stage_failed for AbortRun = %+v, want failed + halt", abortFailed)
	}
	if strings.Contains(abortFailed[1].Message, "reached from") {
		t.Errorf("AbortRun was reached via a conditional edge, not a fallback; halt = %q must not claim an origin", abortFailed[1].Message)
	}
	// A success outcome on a no-edge node is still the invariant error.
	g2 := NewGraph("no-edge-success")
	g2.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g2.AddNode(&Node{ID: "A", Shape: "parallelogram"})
	g2.AddNode(&Node{ID: "Done", Shape: "Msquare"})
	g2.AddEdge(&Edge{From: "start", To: "A"})
	_, err = NewEngine(g2, newTestRegistry()).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no outgoing edges") {
		t.Errorf("no-edge SUCCESS node err = %v, want the invariant error", err)
	}
}

// TestRetryExhaustedSelfFallbackIsNoop: fallback_retry_target pointing at the
// exhausted node itself halts cleanly with no re-entry and no latch.
func TestRetryExhaustedSelfFallbackIsNoop(t *testing.T) {
	g := NewGraph("retry-self-fallback")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "A", Shape: "box", Attrs: map[string]string{
		"max_retries":           "0",
		"fallback_retry_target": "A",
	}})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "start", To: "A"})
	g.AddEdge(&Edge{From: "A", To: "done"})
	var aCalls, rescueCalls int
	reg := retryFallbackRegistry(&aCalls, &rescueCalls)
	var events []PipelineEvent
	engine := NewEngine(g, reg, WithPipelineEventHandler(PipelineEventHandlerFunc(func(evt PipelineEvent) { events = append(events, evt) })))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, _ := engine.Run(ctx)
	if ctx.Err() != nil {
		t.Fatal("run did not terminate")
	}
	if result.Status != OutcomeFail {
		t.Fatalf("status = %q, want fail", result.Status)
	}
	if aCalls != 1 {
		t.Errorf("A ran %d times, want 1 (self fallback_retry_target must not re-enter)", aCalls)
	}
	if n := countEvents(events, EventFallbackLatched, "A"); n != 0 {
		t.Errorf("fallback_latched for A = %d, want 0", n)
	}
	failed := stageFailedFor(events, "A")
	if len(failed) != 1 || !strings.Contains(failed[0].Message, "retries exhausted") || strings.Contains(failed[0].Message, "already taken") {
		t.Errorf("stage_failed for A = %+v, want one plain retries-exhausted halt", failed)
	}
}

// TestGoalGateSelfFallbackIsNoop: a goal gate whose fallback_target is itself
// exhausts without a redirect or latch event.
func TestGoalGateSelfFallbackIsNoop(t *testing.T) {
	g := NewGraph("goal-gate-self-fallback")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "gate", Shape: "box", Attrs: map[string]string{"goal_gate": "true", "max_retries": "0", "fallback_target": "gate"}})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "start", To: "gate"})
	g.AddEdge(&Edge{From: "gate", To: "done", Condition: "ctx.outcome = success"})
	g.AddEdge(&Edge{From: "gate", To: "done", Condition: "ctx.outcome = fail"})
	gateCalls := 0
	reg := newTestRegistry()
	reg.Register(&testHandler{name: "codergen", executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		if node.ID == "gate" {
			gateCalls++
			return Outcome{Status: OutcomeFail}, nil
		}
		return Outcome{Status: OutcomeSuccess}, nil
	}})
	var events []PipelineEvent
	engine := NewEngine(g, reg, WithPipelineEventHandler(PipelineEventHandlerFunc(func(evt PipelineEvent) { events = append(events, evt) })))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, _ := engine.Run(ctx)
	if ctx.Err() != nil {
		t.Fatal("run did not terminate")
	}
	if result.Status != OutcomeFail {
		t.Fatalf("status = %q, want fail", result.Status)
	}
	if gateCalls != 1 {
		t.Errorf("gate ran %d times, want 1", gateCalls)
	}
	if n := countEvents(events, EventFallbackLatched, "gate"); n != 0 {
		t.Errorf("fallback_latched for gate = %d, want 0", n)
	}
	for _, evt := range stageFailedFor(events, "gate") {
		if strings.Contains(evt.Message, "routing to fallback") || strings.Contains(evt.Message, "already taken") {
			t.Errorf("gate stage_failed %q: self fallback must be silent", evt.Message)
		}
	}
}

// TestFallbackOriginPersistedInCheckpoint: the origin of a fallback-reached
// node survives in the checkpoint so `tracker diagnose` can print it.
func TestFallbackOriginPersistedInCheckpoint(t *testing.T) {
	g := selfFallbackGraph(true)
	cpPath := filepath.Join(t.TempDir(), "cp.json")
	selfFallbackRun(t, g, WithCheckpointPath(cpPath))
	cp, err := LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := cp.FallbackOrigin("AbortRun"); got != "A" {
		t.Errorf("FallbackOrigin(AbortRun) = %q, want A", got)
	}
	if got := cp.FallbackOrigin("A"); got != "" {
		t.Errorf("FallbackOrigin(A) = %q, want empty", got)
	}
}
