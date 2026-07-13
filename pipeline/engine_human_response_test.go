// ABOUTME: Tests for one-shot human_response semantics (#352 item 2): the engine
// ABOUTME: clears human_response after the first prompt-consuming node, including across checkpoint resume.
package pipeline

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

// humanResponseRecorder builds a registry where the wait.human handler writes
// human_response via ContextUpdates and every codergen node records the
// human_response value visible at its execution time.
type humanResponseRecorder struct {
	mu   sync.Mutex
	seen map[string][]string // nodeID -> value per execution
}

func (r *humanResponseRecorder) record(nodeID, val string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen[nodeID] = append(r.seen[nodeID], val)
}

func (r *humanResponseRecorder) last(nodeID string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	vals := r.seen[nodeID]
	if len(vals) == 0 {
		return "", false
	}
	return vals[len(vals)-1], true
}

func newHumanResponseHarness(gateResponse string) (*HandlerRegistry, *humanResponseRecorder) {
	rec := &humanResponseRecorder{seen: make(map[string][]string)}
	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "wait.human",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:         OutcomeSuccess,
				ContextUpdates: map[string]string{ContextKeyHumanResponse: gateResponse},
			}, nil
		},
	})
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			val, _ := pctx.Get(ContextKeyHumanResponse)
			rec.record(node.ID, val)
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})
	return reg, rec
}

// gateChainGraph builds start -> Gate(hexagon) -> A(box) -> B(box) -> end.
func gateChainGraph() *Graph {
	g := NewGraph("human_response_oneshot")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "Gate", Shape: "hexagon", Label: "Gate"})
	g.AddNode(&Node{ID: "A", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "B", Shape: "box", Label: "B"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "Gate"})
	g.AddEdge(&Edge{From: "Gate", To: "A"})
	g.AddEdge(&Edge{From: "A", To: "B"})
	g.AddEdge(&Edge{From: "B", To: "end"})
	return g
}

func TestEngineHumanResponseOneShot(t *testing.T) {
	g := gateChainGraph()
	reg, rec := newHumanResponseHarness("approve")

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("expected success, got %q", result.Status)
	}

	// The first consuming node sees the response.
	if got, _ := rec.last("A"); got != "approve" {
		t.Errorf("node A should see human_response, got %q", got)
	}
	// The next consuming node must NOT see it (one-shot).
	if got, _ := rec.last("B"); got != "" {
		t.Errorf("node B should see cleared human_response, got %q", got)
	}
	// The bare key ends the run cleared.
	if got := result.Context[ContextKeyHumanResponse]; got != "" {
		t.Errorf("expected bare human_response cleared in final context, got %q", got)
	}
	// The gate's scoped copy keeps the full value for explicit reference.
	if got := result.Context["node.Gate."+ContextKeyHumanResponse]; got != "approve" {
		t.Errorf("expected node.Gate.human_response to retain the value, got %q", got)
	}
	// The clear must not be misattributed to any consuming node's scope.
	for _, key := range []string{"node.A." + ContextKeyHumanResponse, "node.B." + ContextKeyHumanResponse} {
		if val, ok := result.Context[key]; ok {
			t.Errorf("unexpected scoped key %s=%q from engine-side clear", key, val)
		}
	}
}

func TestEngineHumanResponseSurvivesRetry(t *testing.T) {
	// A node that retries must see the same human_response on re-execution;
	// the clear fires only when the node completes.
	g := NewGraph("human_response_retry")
	g.Attrs["default_max_retry"] = "3"
	g.Attrs["default_retry_policy"] = "none"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "Gate", Shape: "hexagon", Label: "Gate"})
	g.AddNode(&Node{ID: "A", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "B", Shape: "box", Label: "B"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "Gate"})
	g.AddEdge(&Edge{From: "Gate", To: "A"})
	g.AddEdge(&Edge{From: "A", To: "B"})
	g.AddEdge(&Edge{From: "B", To: "end"})

	rec := &humanResponseRecorder{seen: make(map[string][]string)}
	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "wait.human",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:         OutcomeSuccess,
				ContextUpdates: map[string]string{ContextKeyHumanResponse: "approve"},
			}, nil
		},
	})
	var mu sync.Mutex
	attempts := 0
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			val, _ := pctx.Get(ContextKeyHumanResponse)
			rec.record(node.ID, val)
			if node.ID == "A" {
				mu.Lock()
				attempts++
				current := attempts
				mu.Unlock()
				if current < 2 {
					return Outcome{Status: OutcomeRetry}, nil
				}
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("expected success, got %q", result.Status)
	}

	rec.mu.Lock()
	aSeen := append([]string(nil), rec.seen["A"]...)
	rec.mu.Unlock()
	if len(aSeen) != 2 {
		t.Fatalf("expected 2 executions of A, got %d", len(aSeen))
	}
	for i, v := range aSeen {
		if v != "approve" {
			t.Errorf("A execution %d should still see human_response, got %q", i, v)
		}
	}
	if got, _ := rec.last("B"); got != "" {
		t.Errorf("node B should see cleared human_response, got %q", got)
	}
}

func TestEngineHumanResponseFreshWriteNotClobbered(t *testing.T) {
	// If the consuming node itself writes a new human_response via
	// ContextUpdates, the engine must not clear the fresh value.
	g := gateChainGraph()

	rec := &humanResponseRecorder{seen: make(map[string][]string)}
	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "wait.human",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:         OutcomeSuccess,
				ContextUpdates: map[string]string{ContextKeyHumanResponse: "approve"},
			}, nil
		},
	})
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			val, _ := pctx.Get(ContextKeyHumanResponse)
			rec.record(node.ID, val)
			if node.ID == "A" {
				return Outcome{
					Status:         OutcomeSuccess,
					ContextUpdates: map[string]string{ContextKeyHumanResponse: "fresh"},
				}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	engine := NewEngine(g, reg)
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if got, _ := rec.last("B"); got != "fresh" {
		t.Errorf("node B should see the fresh value written by A, got %q", got)
	}
}

func TestEngineHumanResponseNotClearedByNonConsumingNodes(t *testing.T) {
	// Tool/conditional nodes do not feed pctx into an LLM prompt; they must
	// not consume the response.
	g := NewGraph("human_response_tool_passthrough")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "Gate", Shape: "hexagon", Label: "Gate"})
	g.AddNode(&Node{ID: "T", Shape: "parallelogram", Label: "Tool"})
	g.AddNode(&Node{ID: "A", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "Gate"})
	g.AddEdge(&Edge{From: "Gate", To: "T"})
	g.AddEdge(&Edge{From: "T", To: "A"})
	g.AddEdge(&Edge{From: "A", To: "end"})

	reg, rec := newHumanResponseHarness("approve")

	engine := NewEngine(g, reg)
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if got, _ := rec.last("A"); got != "approve" {
		t.Errorf("tool node must not consume human_response; A got %q", got)
	}
}

func TestEngineHumanResponseConsumedByParallel(t *testing.T) {
	// A parallel node whose branches include a codergen node resolves branch
	// prompts (which receive the injection), so it counts as a consumer.
	g := NewGraph("human_response_parallel")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "Gate", Shape: "hexagon", Label: "Gate"})
	g.AddNode(&Node{ID: "P", Shape: "component", Label: "Parallel",
		Attrs: map[string]string{"parallel_targets": "B1"}})
	g.AddNode(&Node{ID: "B1", Shape: "box", Label: "Branch"})
	g.AddNode(&Node{ID: "A", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "Gate"})
	g.AddEdge(&Edge{From: "Gate", To: "P"})
	g.AddEdge(&Edge{From: "P", To: "A"})
	g.AddEdge(&Edge{From: "A", To: "end"})

	reg, rec := newHumanResponseHarness("approve")

	engine := NewEngine(g, reg)
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if got, _ := rec.last("A"); got != "" {
		t.Errorf("parallel node with a codergen branch should consume human_response; A got %q", got)
	}
}

func TestEngineHumanResponseNotConsumedByToolOnlyParallel(t *testing.T) {
	// A parallel fan-out whose branches are all non-LLM handlers (a tool-only
	// validation fan-out, say) resolves no prompts — it must NOT consume the
	// response; the first real codergen node after the join still gets it.
	g := NewGraph("human_response_parallel_tools")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "Gate", Shape: "hexagon", Label: "Gate"})
	g.AddNode(&Node{ID: "P", Shape: "component", Label: "Parallel",
		Attrs: map[string]string{"parallel_targets": "T1, T2"}})
	g.AddNode(&Node{ID: "T1", Shape: "parallelogram", Label: "Tool1"})
	g.AddNode(&Node{ID: "T2", Shape: "parallelogram", Label: "Tool2"})
	g.AddNode(&Node{ID: "A", Shape: "box", Label: "A"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "Gate"})
	g.AddEdge(&Edge{From: "Gate", To: "P"})
	g.AddEdge(&Edge{From: "P", To: "A"})
	g.AddEdge(&Edge{From: "A", To: "end"})

	reg, rec := newHumanResponseHarness("approve")

	engine := NewEngine(g, reg)
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if got, _ := rec.last("A"); got != "approve" {
		t.Errorf("tool-only parallel must not consume human_response; A got %q", got)
	}
}

func TestEngineHumanResponseClearPersistsAcrossResume(t *testing.T) {
	// The clear is a context mutation like any other: a resumed run must not
	// resurrect the stale response from the checkpoint.
	g := gateChainGraph()
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")

	// Run 1: A consumes, then B fails the run.
	rec1 := &humanResponseRecorder{seen: make(map[string][]string)}
	reg1 := newTestRegistry()
	reg1.Register(&testHandler{
		name: "wait.human",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:         OutcomeSuccess,
				ContextUpdates: map[string]string{ContextKeyHumanResponse: "approve"},
			}, nil
		},
	})
	reg1.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			val, _ := pctx.Get(ContextKeyHumanResponse)
			rec1.record(node.ID, val)
			if node.ID == "B" {
				return Outcome{Status: OutcomeFail}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})
	engine1 := NewEngine(g, reg1, WithCheckpointPath(cpPath))
	if _, err := engine1.Run(context.Background()); err == nil {
		t.Fatalf("run 1 should have stopped at B's strict failure")
	}
	if got, _ := rec1.last("A"); got != "approve" {
		t.Fatalf("run 1: A should see human_response, got %q", got)
	}

	// Run 2: resume from the checkpoint; B succeeds and must NOT see the
	// stale response.
	reg2, rec2 := newHumanResponseHarness("should-not-be-used")
	engine2 := NewEngine(g, reg2, WithCheckpointPath(cpPath))
	result2, err := engine2.Run(context.Background())
	if err != nil {
		t.Fatalf("run 2 failed: %v", err)
	}
	if result2.Status != OutcomeSuccess {
		t.Fatalf("run 2 expected success, got %q", result2.Status)
	}
	if got, ok := rec2.last("B"); !ok {
		t.Fatalf("run 2: B never executed")
	} else if got != "" {
		t.Errorf("run 2: B resurrected stale human_response %q from checkpoint", got)
	}
}
