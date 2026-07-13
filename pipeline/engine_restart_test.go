// ABOUTME: Tests for restart loop detection and handling in the pipeline engine.
// ABOUTME: Covers restart detection, max_restarts enforcement, downstream clearing, restart_target, and event emission.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestEngineRestartLoopDetection(t *testing.T) {
	// Graph: Start -> A -> B -> Check --(outcome=fail)--> A (loop back)
	//                              Check --(outcome=success)--> End
	// A is re-entered after completing B and Check, triggering a restart.
	// On the second pass through, Check succeeds.
	g := NewGraph("restart_loop")
	g.Attrs["max_restarts"] = "3"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "a", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "b", Shape: "box", Label: "B"})
	g.AddNode(&Node{ID: "check", Shape: "diamond", Label: "Check"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "a"})
	g.AddEdge(&Edge{From: "a", To: "b"})
	g.AddEdge(&Edge{From: "b", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "end", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "check", To: "a", Condition: "outcome=fail"})

	reg := newTestRegistry()

	var mu sync.Mutex
	checkAttempts := 0
	aExecutions := 0

	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			aExecutions++
			mu.Unlock()
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			checkAttempts++
			attempt := checkAttempts
			mu.Unlock()
			if attempt == 1 {
				return Outcome{
					Status:         OutcomeFail,
					ContextUpdates: map[string]string{"outcome": "fail"},
				}, nil
			}
			return Outcome{
				Status:         OutcomeSuccess,
				ContextUpdates: map[string]string{"outcome": "success"},
			}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("expected success, got %q", result.Status)
	}
	// A should have executed at least twice (once initially, once after restart).
	mu.Lock()
	defer mu.Unlock()
	if aExecutions < 2 {
		t.Errorf("expected A to execute at least 2 times, got %d", aExecutions)
	}
	if checkAttempts != 2 {
		t.Errorf("expected check to run 2 times, got %d", checkAttempts)
	}
}

func TestEngineRestartMaxRestartsExceeded(t *testing.T) {
	// Graph loops back every time. With max_restarts=2, it should fail after 2 restarts.
	g := NewGraph("restart_exceed")
	g.Attrs["max_restarts"] = "2"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "a", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "check", Shape: "diamond", Label: "Check"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "a"})
	g.AddEdge(&Edge{From: "a", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "end", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "check", To: "a", Condition: "outcome=fail"})

	reg := newTestRegistry()

	// Check always fails, forcing infinite restarts.
	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:         OutcomeFail,
				ContextUpdates: map[string]string{"outcome": "fail"},
			}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when max restarts exceeded")
	}
	if result != nil && result.Status != OutcomeFail {
		t.Errorf("expected fail status, got %q", result.Status)
	}
}

func TestEngineRestartDownstreamClearing(t *testing.T) {
	// Graph: Start -> A -> B -> C -> Check --(fail)--> A
	//                                Check --(success)--> End
	// When restart occurs at A, B and C should be cleared from completed
	// and re-executed.
	g := NewGraph("restart_downstream")
	g.Attrs["max_restarts"] = "3"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "a", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "b", Shape: "box", Label: "B"})
	g.AddNode(&Node{ID: "c", Shape: "box", Label: "C"})
	g.AddNode(&Node{ID: "check", Shape: "diamond", Label: "Check"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "a"})
	g.AddEdge(&Edge{From: "a", To: "b"})
	g.AddEdge(&Edge{From: "b", To: "c"})
	g.AddEdge(&Edge{From: "c", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "end", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "check", To: "a", Condition: "outcome=fail"})

	reg := newTestRegistry()

	var mu sync.Mutex
	execCounts := map[string]int{}
	checkAttempts := 0

	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			execCounts[node.ID]++
			mu.Unlock()
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			checkAttempts++
			attempt := checkAttempts
			mu.Unlock()
			if attempt == 1 {
				return Outcome{
					Status:         OutcomeFail,
					ContextUpdates: map[string]string{"outcome": "fail"},
				}, nil
			}
			return Outcome{
				Status:         OutcomeSuccess,
				ContextUpdates: map[string]string{"outcome": "success"},
			}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("expected success, got %q", result.Status)
	}

	mu.Lock()
	defer mu.Unlock()

	// All downstream nodes (a, b, c) should have been executed twice.
	for _, nodeID := range []string{"a", "b", "c"} {
		if execCounts[nodeID] < 2 {
			t.Errorf("expected node %q to execute at least 2 times, got %d", nodeID, execCounts[nodeID])
		}
	}
}

func TestEngineRestartTargetAttribute(t *testing.T) {
	// Graph: Start -> A -> B -> Check --(fail)--> B (loops to B, not A)
	//                             Check --(success)--> End
	// restart_target=B means instead of restarting from the re-entered node (B),
	// the engine restarts from B (same in this case, but if the edge points to A,
	// restart_target redirects to B).
	g := NewGraph("restart_target")
	g.Attrs["max_restarts"] = "3"
	g.Attrs["restart_target"] = "b"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "a", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "b", Shape: "box", Label: "B"})
	g.AddNode(&Node{ID: "check", Shape: "diamond", Label: "Check"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "a"})
	g.AddEdge(&Edge{From: "a", To: "b"})
	g.AddEdge(&Edge{From: "b", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "end", Condition: "outcome=success"})
	// The edge goes back to A, but restart_target is B.
	g.AddEdge(&Edge{From: "check", To: "a", Condition: "outcome=fail"})

	reg := newTestRegistry()

	var mu sync.Mutex
	execCounts := map[string]int{}
	checkAttempts := 0

	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			execCounts[node.ID]++
			mu.Unlock()
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			checkAttempts++
			attempt := checkAttempts
			mu.Unlock()
			if attempt == 1 {
				return Outcome{
					Status:         OutcomeFail,
					ContextUpdates: map[string]string{"outcome": "fail"},
				}, nil
			}
			return Outcome{
				Status:         OutcomeSuccess,
				ContextUpdates: map[string]string{"outcome": "success"},
			}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("expected success, got %q", result.Status)
	}

	mu.Lock()
	defer mu.Unlock()

	// A should only execute once because restart_target is B, not A.
	if execCounts["a"] != 1 {
		t.Errorf("expected node 'a' to execute 1 time (restart_target=b skips it), got %d", execCounts["a"])
	}
	// B should execute twice (initial + restart).
	if execCounts["b"] < 2 {
		t.Errorf("expected node 'b' to execute at least 2 times, got %d", execCounts["b"])
	}
}

func TestEngineRestartEmitsLoopRestartEvent(t *testing.T) {
	g := NewGraph("restart_event")
	g.Attrs["max_restarts"] = "3"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "a", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "check", Shape: "diamond", Label: "Check"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "a"})
	g.AddEdge(&Edge{From: "a", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "end", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "check", To: "a", Condition: "outcome=fail"})

	reg := newTestRegistry()

	var mu sync.Mutex
	checkAttempts := 0
	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			checkAttempts++
			attempt := checkAttempts
			mu.Unlock()
			if attempt == 1 {
				return Outcome{
					Status:         OutcomeFail,
					ContextUpdates: map[string]string{"outcome": "fail"},
				}, nil
			}
			return Outcome{
				Status:         OutcomeSuccess,
				ContextUpdates: map[string]string{"outcome": "success"},
			}, nil
		},
	})

	var eventMu sync.Mutex
	var events []PipelineEvent
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		eventMu.Lock()
		events = append(events, evt)
		eventMu.Unlock()
	})

	engine := NewEngine(g, reg, WithPipelineEventHandler(handler))
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("expected success, got %q", result.Status)
	}

	eventMu.Lock()
	defer eventMu.Unlock()

	foundRestart := false
	for _, evt := range events {
		if evt.Type == EventLoopRestart {
			foundRestart = true
			if evt.NodeID != "a" {
				t.Errorf("expected restart event for node 'a', got %q", evt.NodeID)
			}
		}
	}
	if !foundRestart {
		t.Error("expected EventLoopRestart to be emitted")
	}
}

func TestEngineRestartCheckpointPreservesRestartCount(t *testing.T) {
	g := NewGraph("restart_cp")
	g.Attrs["max_restarts"] = "5"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "a", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "check", Shape: "diamond", Label: "Check"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "a"})
	g.AddEdge(&Edge{From: "a", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "end", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "check", To: "a", Condition: "outcome=fail"})

	dir := t.TempDir()
	cpPath := filepath.Join(dir, "cp.json")

	reg := newTestRegistry()

	var mu sync.Mutex
	checkAttempts := 0
	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			checkAttempts++
			attempt := checkAttempts
			mu.Unlock()
			if attempt <= 2 {
				return Outcome{
					Status:         OutcomeFail,
					ContextUpdates: map[string]string{"outcome": "fail"},
				}, nil
			}
			return Outcome{
				Status:         OutcomeSuccess,
				ContextUpdates: map[string]string{"outcome": "success"},
			}, nil
		},
	})

	engine := NewEngine(g, reg, WithCheckpointPath(cpPath))
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("expected success, got %q", result.Status)
	}

	// Load the checkpoint and verify restart_count was saved.
	cp, err := LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	if cp.RestartCount != 2 {
		t.Errorf("expected restart_count=2 in checkpoint, got %d", cp.RestartCount)
	}
}

func TestEngineRestartDefaultMaxRestarts(t *testing.T) {
	// When max_restarts is not set, default is 5.
	g := NewGraph("restart_default")
	// No max_restarts attribute set.
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "a", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "check", Shape: "diamond", Label: "Check"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "a"})
	g.AddEdge(&Edge{From: "a", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "end", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "check", To: "a", Condition: "outcome=fail"})

	reg := newTestRegistry()

	// Always fail — should hit default max_restarts of 5.
	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:         OutcomeFail,
				ContextUpdates: map[string]string{"outcome": "fail"},
			}, nil
		},
	})

	engine := NewEngine(g, reg)
	_, err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when default max restarts exceeded")
	}
	// Error message should mention max restarts.
	expected := "max restarts (5) exceeded"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestEngineRestartResetsRetryCountsForClearedNodes(t *testing.T) {
	// When a restart clears downstream nodes, their retry counts should also be reset.
	g := NewGraph("restart_retry_reset")
	g.Attrs["max_restarts"] = "3"
	g.Attrs["default_max_retry"] = "2"
	g.Attrs["default_retry_policy"] = "none"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "a", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "flaky", Shape: "box", Label: "Flaky"})
	g.AddNode(&Node{ID: "check", Shape: "diamond", Label: "Check"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "a"})
	g.AddEdge(&Edge{From: "a", To: "flaky"})
	g.AddEdge(&Edge{From: "flaky", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "end", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "check", To: "a", Condition: "outcome=fail"})

	reg := newTestRegistry()

	var mu sync.Mutex
	checkAttempts := 0
	flakyRetries := 0

	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "flaky" {
				mu.Lock()
				flakyRetries++
				retries := flakyRetries
				mu.Unlock()
				// Retry once on each pass.
				if retries%2 == 1 {
					return Outcome{Status: OutcomeRetry}, nil
				}
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			checkAttempts++
			attempt := checkAttempts
			mu.Unlock()
			if attempt == 1 {
				return Outcome{
					Status:         OutcomeFail,
					ContextUpdates: map[string]string{"outcome": "fail"},
				}, nil
			}
			return Outcome{
				Status:         OutcomeSuccess,
				ContextUpdates: map[string]string{"outcome": "success"},
			}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("expected success, got %q", result.Status)
	}
}

func TestDownstreamNodes(t *testing.T) {
	g := NewGraph("downstream_test")
	g.AddNode(&Node{ID: "a", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "b", Shape: "box", Label: "B"})
	g.AddNode(&Node{ID: "c", Shape: "box", Label: "C"})
	g.AddNode(&Node{ID: "d", Shape: "box", Label: "D"})
	g.AddNode(&Node{ID: "e", Shape: "box", Label: "E"})

	// a -> b -> c -> d
	//           c -> e
	g.AddEdge(&Edge{From: "a", To: "b"})
	g.AddEdge(&Edge{From: "b", To: "c"})
	g.AddEdge(&Edge{From: "c", To: "d"})
	g.AddEdge(&Edge{From: "c", To: "e"})

	result := downstreamNodes(g, "b")

	expected := map[string]bool{"c": true, "d": true, "e": true}
	got := make(map[string]bool)
	for _, id := range result {
		got[id] = true
	}

	if len(got) != len(expected) {
		t.Errorf("expected %d downstream nodes, got %d: %v", len(expected), len(got), result)
	}
	for id := range expected {
		if !got[id] {
			t.Errorf("expected downstream node %q not found", id)
		}
	}

	// b itself should NOT be in the result.
	if got["b"] {
		t.Error("start node 'b' should not be in downstream result")
	}
	// a should NOT be downstream of b.
	if got["a"] {
		t.Error("node 'a' should not be downstream of 'b'")
	}
}

func TestCheckpointRestartCountSerialization(t *testing.T) {
	cp := &Checkpoint{
		RunID:          "test-run",
		CurrentNode:    "a",
		CompletedNodes: []string{"s"},
		RetryCounts:    map[string]int{},
		Context:        map[string]string{},
		RestartCount:   3,
	}

	data, err := json.Marshal(cp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var loaded Checkpoint
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if loaded.RestartCount != 3 {
		t.Errorf("expected restart_count=3, got %d", loaded.RestartCount)
	}
}

func TestEngineRestartMaxRestartsErrorMessage(t *testing.T) {
	g := NewGraph("restart_msg")
	g.Attrs["max_restarts"] = "3"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "a", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "check", Shape: "diamond", Label: "Check"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "a"})
	g.AddEdge(&Edge{From: "a", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "end", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "check", To: "a", Condition: "outcome=fail"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:         OutcomeFail,
				ContextUpdates: map[string]string{"outcome": "fail"},
			}, nil
		},
	})

	engine := NewEngine(g, reg)
	_, err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	expected := fmt.Sprintf("max restarts (%d) exceeded", 3)
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}
