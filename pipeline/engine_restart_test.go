// ABOUTME: Tests for restart loop detection and handling in the pipeline engine.
// ABOUTME: Covers restart detection, max_restarts enforcement, downstream clearing, restart_target, and event emission.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

// twoIndependentLoopsGraph builds two strictly-sequential, independent loops:
//
//	s -> a -> checkA --(success)--> b -> checkB --(success)--> end
//	          checkA --(fail)-----> a (loop 1, target a)
//	                                     checkB --(fail)-----> b (loop 2, target b)
func twoIndependentLoopsGraph(maxRestarts string) *Graph {
	g := NewGraph("two_loops")
	g.Attrs["max_restarts"] = maxRestarts
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "a", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "checkA", Shape: "diamond", Label: "CheckA"})
	g.AddNode(&Node{ID: "b", Shape: "box", Label: "B"})
	g.AddNode(&Node{ID: "checkB", Shape: "diamond", Label: "CheckB"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})

	g.AddEdge(&Edge{From: "s", To: "a"})
	g.AddEdge(&Edge{From: "a", To: "checkA"})
	g.AddEdge(&Edge{From: "checkA", To: "b", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "checkA", To: "a", Condition: "outcome=fail"})
	g.AddEdge(&Edge{From: "b", To: "checkB"})
	g.AddEdge(&Edge{From: "checkB", To: "end", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "checkB", To: "b", Condition: "outcome=fail"})
	return g
}

// TestEngineRestartBudgetIsPerTarget pins #603: each resolved loop target has
// its OWN restart budget. With a run-wide (global) counter the second loop would
// have found the budget already drained by the first and failed; with per-target
// budgets both loops recover independently.
func TestEngineRestartBudgetIsPerTarget(t *testing.T) {
	// max_restarts=2. Loop A restarts twice (fails checkA attempts 1-2, then
	// succeeds); loop B likewise restarts twice. Aggregate restarts = 4 > 2,
	// which the OLD global counter would have rejected the moment loop B tried
	// its first restart (global count already 2).
	g := twoIndependentLoopsGraph("2")

	reg := newTestRegistry()
	var mu sync.Mutex
	checkAAttempts := 0
	checkBAttempts := 0
	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			defer mu.Unlock()
			var attempt int
			switch node.ID {
			case "checkA":
				checkAAttempts++
				attempt = checkAAttempts
			case "checkB":
				checkBAttempts++
				attempt = checkBAttempts
			}
			if attempt <= 2 {
				return Outcome{Status: OutcomeFail, ContextUpdates: map[string]string{"outcome": "fail"}}, nil
			}
			return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"outcome": "success"}}, nil
		},
	})

	dir := t.TempDir()
	cpPath := filepath.Join(dir, "cp.json")

	var eventMu sync.Mutex
	restartsByNode := map[string]int{}
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		if evt.Type == EventLoopRestart {
			eventMu.Lock()
			restartsByNode[evt.NodeID]++
			eventMu.Unlock()
		}
	})

	engine := NewEngine(g, reg, WithCheckpointPath(cpPath), WithPipelineEventHandler(handler))
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed (per-target budget should let both loops recover): %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("expected success, got %q", result.Status)
	}

	eventMu.Lock()
	defer eventMu.Unlock()
	if restartsByNode["a"] != 2 {
		t.Errorf("expected 2 restarts of target 'a', got %d", restartsByNode["a"])
	}
	if restartsByNode["b"] != 2 {
		t.Errorf("expected 2 restarts of target 'b', got %d", restartsByNode["b"])
	}

	cp, err := LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	if got := cp.RestartCountFor("a"); got != 2 {
		t.Errorf("expected RestartCounts[a]=2, got %d", got)
	}
	if got := cp.RestartCountFor("b"); got != 2 {
		t.Errorf("expected RestartCounts[b]=2, got %d", got)
	}
	// RestartCount stays the run-wide aggregate for manifests/events.
	if cp.RestartCount != 4 {
		t.Errorf("expected aggregate RestartCount=4, got %d", cp.RestartCount)
	}
}

// TestEngineRestartCircuitBreakerTripsPerTarget pins the other half of #603: the
// max_restarts ceiling still trips, and it trips against the OFFENDING target's
// own budget — a second loop that never recovers gets its full budget even
// though an earlier loop already spent restarts.
func TestEngineRestartCircuitBreakerTripsPerTarget(t *testing.T) {
	// max_restarts=2. Loop A recovers after 1 restart; loop B never recovers and
	// must consume its OWN full budget (2 restarts) before the breaker trips.
	g := twoIndependentLoopsGraph("2")

	reg := newTestRegistry()
	var mu sync.Mutex
	checkAAttempts := 0
	reg.Register(&testHandler{
		name: "conditional",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			mu.Lock()
			defer mu.Unlock()
			if node.ID == "checkA" {
				checkAAttempts++
				if checkAAttempts <= 1 {
					return Outcome{Status: OutcomeFail, ContextUpdates: map[string]string{"outcome": "fail"}}, nil
				}
				return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"outcome": "success"}}, nil
			}
			// checkB never succeeds.
			return Outcome{Status: OutcomeFail, ContextUpdates: map[string]string{"outcome": "fail"}}, nil
		},
	})

	dir := t.TempDir()
	cpPath := filepath.Join(dir, "cp.json")

	var eventMu sync.Mutex
	restartsByNode := map[string]int{}
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		if evt.Type == EventLoopRestart {
			eventMu.Lock()
			restartsByNode[evt.NodeID]++
			eventMu.Unlock()
		}
	})

	engine := NewEngine(g, reg, WithCheckpointPath(cpPath), WithPipelineEventHandler(handler))
	_, err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected max-restarts failure on the non-recovering loop")
	}
	if expected := fmt.Sprintf("max restarts (%d) exceeded", 2); err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}

	eventMu.Lock()
	defer eventMu.Unlock()
	// A recovered after 1 restart; B got its own full budget (2) before tripping.
	if restartsByNode["a"] != 1 {
		t.Errorf("expected 1 restart of target 'a', got %d", restartsByNode["a"])
	}
	if restartsByNode["b"] != 2 {
		t.Errorf("expected target 'b' to get its full budget (2 restarts) before tripping, got %d", restartsByNode["b"])
	}
}

// TestCheckpointRestartCountsRoundTrip verifies per-target restart counts (#603)
// survive a save/load round-trip and that a legacy checkpoint without the field
// loads with a nil map (per-target budgets start fresh — conservative reset).
func TestCheckpointRestartCountsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cp.json")

	cp := &Checkpoint{RunID: "r1", CurrentNode: "a"}
	if got := cp.IncrementRestart("a"); got != 1 {
		t.Fatalf("IncrementRestart(a) = %d, want 1", got)
	}
	if got := cp.IncrementRestart("a"); got != 2 {
		t.Fatalf("IncrementRestart(a) = %d, want 2", got)
	}
	if got := cp.IncrementRestart("b"); got != 1 {
		t.Fatalf("IncrementRestart(b) = %d, want 1", got)
	}
	if cp.RestartCount != 3 {
		t.Fatalf("aggregate RestartCount = %d, want 3", cp.RestartCount)
	}

	if err := SaveCheckpoint(cp, path); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}
	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if got := loaded.RestartCountFor("a"); got != 2 {
		t.Errorf("RestartCountFor(a) = %d, want 2", got)
	}
	if got := loaded.RestartCountFor("b"); got != 1 {
		t.Errorf("RestartCountFor(b) = %d, want 1", got)
	}
	if loaded.RestartCount != 3 {
		t.Errorf("aggregate RestartCount = %d, want 3", loaded.RestartCount)
	}

	// Legacy checkpoint (pre-#603): scalar only, no restart_counts field.
	legacyPath := filepath.Join(dir, "legacy.json")
	legacy := `{"run_id":"old","current_node":"a","completed_nodes":["s"],"retry_counts":{},"context":{},"timestamp":"2026-05-01T00:00:00Z","restart_count":4}`
	if err := os.WriteFile(legacyPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	oldCp, err := LoadCheckpoint(legacyPath)
	if err != nil {
		t.Fatalf("LoadCheckpoint(legacy): %v", err)
	}
	if oldCp.RestartCounts != nil {
		t.Errorf("expected nil RestartCounts on legacy checkpoint, got %v", oldCp.RestartCounts)
	}
	if got := oldCp.RestartCountFor("anything"); got != 0 {
		t.Errorf("legacy RestartCountFor = %d, want 0 (fresh per-target budget)", got)
	}
	if oldCp.RestartCount != 4 {
		t.Errorf("legacy aggregate RestartCount = %d, want 4 (preserved)", oldCp.RestartCount)
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
