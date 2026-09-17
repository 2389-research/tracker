// ABOUTME: Tests for the retry-exhausted fallback one-shot latch (#642): the
// ABOUTME: latch is persisted in the checkpoint and honored on resume.
package pipeline

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// retryFallbackLoopGraph builds start -> A -> done with A's retry-exhausted
// fallback_retry_target routing to rescue, and rescue looping back into A.
func retryFallbackLoopGraph() *Graph {
	g := NewGraph("retry-fallback-latch")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "A", Shape: "box", Attrs: map[string]string{
		"max_retries":           "0",
		"fallback_retry_target": "rescue",
	}})
	g.AddNode(&Node{ID: "rescue", Shape: "box"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "start", To: "A"})
	g.AddEdge(&Edge{From: "A", To: "done"})
	g.AddEdge(&Edge{From: "rescue", To: "A"})
	return g
}

func retryFallbackRegistry(aCalls, rescueCalls *int) *HandlerRegistry {
	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			switch node.ID {
			case "A":
				*aCalls++
				return Outcome{Status: OutcomeRetry}, nil
			case "rescue":
				*rescueCalls++
				return Outcome{Status: OutcomeSuccess}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})
	return reg
}

// TestRetryExhaustedFallbackLatchPersistedInCheckpoint verifies the latch is
// written to the checkpoint when the fallback is taken and that the run halts
// on the second exhaustion with a fallback_latched event.
func TestRetryExhaustedFallbackLatchPersistedInCheckpoint(t *testing.T) {
	g := retryFallbackLoopGraph()
	var aCalls, rescueCalls int
	reg := retryFallbackRegistry(&aCalls, &rescueCalls)

	var events []PipelineEvent
	cpPath := filepath.Join(t.TempDir(), "cp.json")
	engine := NewEngine(g, reg,
		WithCheckpointPath(cpPath),
		WithPipelineEventHandler(PipelineEventHandlerFunc(func(evt PipelineEvent) { events = append(events, evt) })),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, _ := engine.Run(ctx)
	if ctx.Err() != nil {
		t.Fatalf("run did not terminate (A ran %d times)", aCalls)
	}
	if result.Status != OutcomeFail {
		t.Fatalf("status = %q, want fail", result.Status)
	}
	if rescueCalls != 1 || aCalls != 2 {
		t.Fatalf("rescue=%d A=%d, want rescue=1 A=2", rescueCalls, aCalls)
	}
	latched := 0
	for _, evt := range events {
		if evt.Type == EventFallbackLatched && evt.NodeID == "A" {
			latched++
		}
	}
	if latched != 1 {
		t.Errorf("fallback_latched events = %d, want 1", latched)
	}
	cp, err := LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if !cp.IsFallbackTaken("A") {
		t.Error("checkpoint does not record A's fallback as taken — a resume would re-take it")
	}
}

// TestRetryExhaustedFallbackLatchSurvivesResume seeds a checkpoint that already
// latched A's fallback and resumes at A: the fallback must NOT be taken again.
func TestRetryExhaustedFallbackLatchSurvivesResume(t *testing.T) {
	g := retryFallbackLoopGraph()
	var aCalls, rescueCalls int
	reg := retryFallbackRegistry(&aCalls, &rescueCalls)

	cpPath := filepath.Join(t.TempDir(), "cp.json")
	seed := &Checkpoint{CurrentNode: "A", CompletedNodes: []string{"start"}, RetryCounts: map[string]int{}, Context: map[string]string{}}
	seed.MarkFallbackTaken("A")
	if err := SaveCheckpoint(seed, cpPath); err != nil {
		t.Fatalf("save seed checkpoint: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, _ := NewEngine(g, reg, WithCheckpointPath(cpPath)).Run(ctx)
	if ctx.Err() != nil {
		t.Fatalf("resumed run did not terminate (A ran %d times)", aCalls)
	}
	if result.Status != OutcomeFail {
		t.Fatalf("status = %q, want fail", result.Status)
	}
	if rescueCalls != 0 {
		t.Errorf("rescue ran %d times after resume, want 0 (latch must survive resume)", rescueCalls)
	}
	if aCalls != 1 {
		t.Errorf("A ran %d times, want 1", aCalls)
	}
}
