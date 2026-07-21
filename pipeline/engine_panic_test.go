// ABOUTME: Tests the Phase 0 failure-containment invariants on Engine.Run.
// ABOUTME: A handler panic is contained (no crash); every terminal exit emits exactly one TerminalStatus.
package pipeline

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// collectTerminalEvents returns an event handler that records every event
// carrying a non-empty TerminalStatus, plus the slice it fills.
func collectTerminalEvents() (PipelineEventHandler, *[]PipelineEvent, *sync.Mutex) {
	var mu sync.Mutex
	var out []PipelineEvent
	h := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		if evt.TerminalStatus != "" {
			mu.Lock()
			out = append(out, evt)
			mu.Unlock()
		}
	})
	return h, &out, &mu
}

// TestRun_ContainsHandlerPanic asserts a panic in a handler on the main run
// goroutine is contained — Run returns (nil, err) instead of crashing the
// process — and that exactly one fail terminal event still reaches the stream.
// Without containment, one panicking run would take down every other concurrent
// run a RunManager owns.
func TestRun_ContainsHandlerPanic(t *testing.T) {
	g := NewGraph("panic")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond", Attrs: map[string]string{}})
	g.AddNode(&Node{ID: "boom", Shape: "box", Attrs: map[string]string{}})
	g.Nodes["boom"].Handler = "boomer"
	g.AddNode(&Node{ID: "exit", Shape: "Msquare", Attrs: map[string]string{}})
	g.AddEdge(&Edge{From: "start", To: "boom"})
	g.AddEdge(&Edge{From: "boom", To: "exit"})

	reg := newTestRegistry()
	reg.Register(&testHandler{name: "boomer", executeFn: func(context.Context, *Node, *PipelineContext) (Outcome, error) {
		panic("boom in handler")
	}})

	handler, terminal, mu := collectTerminalEvents()
	eng := NewEngine(g, reg, WithPipelineEventHandler(handler))

	result, err := eng.Run(context.Background()) // must NOT panic out of Run
	if err == nil {
		t.Fatal("expected an error from the recovered panic")
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Fatalf("error should mention the panic, got: %v", err)
	}
	if result != nil {
		t.Fatalf("expected a nil result on panic (state may be inconsistent), got %+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*terminal) != 1 {
		t.Fatalf("expected exactly one terminal-status event, got %d", len(*terminal))
	}
	if got := (*terminal)[0].TerminalStatus; got != string(OutcomeFail) {
		t.Fatalf("TerminalStatus = %q, want %q", got, OutcomeFail)
	}
}

// TestResolveRetryTarget covers the retry_target existence guard: absent → the
// node itself; present-and-valid → the target; present-but-missing → a loud
// error (mirroring restart_target) rather than routing into an opaque
// "node not found" nil-result exit.
func TestResolveRetryTarget(t *testing.T) {
	g := NewGraph("rt")
	g.AddNode(&Node{ID: "a", Shape: "box", Attrs: map[string]string{}})
	g.AddNode(&Node{ID: "b", Shape: "box", Attrs: map[string]string{}})
	eng := NewEngine(g, newTestRegistry())

	if tgt, err := eng.resolveRetryTarget(&Node{ID: "a", Attrs: map[string]string{}}, "a"); err != nil || tgt != "a" {
		t.Fatalf("no retry_target: got (%q, %v), want (a, nil)", tgt, err)
	}
	if tgt, err := eng.resolveRetryTarget(&Node{ID: "a", Attrs: map[string]string{"retry_target": "b"}}, "a"); err != nil || tgt != "b" {
		t.Fatalf("valid retry_target: got (%q, %v), want (b, nil)", tgt, err)
	}
	if _, err := eng.resolveRetryTarget(&Node{ID: "a", Attrs: map[string]string{"retry_target": "nope"}}, "a"); err == nil {
		t.Fatal("missing retry_target must error, got nil")
	}
}

// TestRun_EmitsTerminalStatusOnNilResultInvariantError covers the class the
// backstop previously skipped: a terminal exit that returns (nil, err) — here a
// non-exit node with no outgoing edge — raised after pipeline_started fired.
// The backstop must still emit exactly one fail terminal event so a stream-only
// subscriber (e.g. Slack) sees the run finish instead of hanging forever.
func TestRun_EmitsTerminalStatusOnNilResultInvariantError(t *testing.T) {
	g := NewGraph("dangling")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond", Attrs: map[string]string{}})
	g.AddNode(&Node{ID: "dead", Shape: "box", Attrs: map[string]string{}})
	g.AddNode(&Node{ID: "exit", Shape: "Msquare", Attrs: map[string]string{}})
	g.AddEdge(&Edge{From: "start", To: "dead"})
	// "dead" is a non-exit node with NO outgoing edge → the engine returns an
	// invariant error with a nil result.

	reg := newTestRegistry()
	handler, terminal, mu := collectTerminalEvents()
	eng := NewEngine(g, reg, WithPipelineEventHandler(handler))

	result, err := eng.Run(context.Background())
	if err == nil {
		t.Fatal("expected an invariant error from the dangling node")
	}
	if result != nil {
		t.Fatalf("expected a nil result, got %+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*terminal) != 1 {
		t.Fatalf("backstop must emit exactly one fail terminal event, got %d", len(*terminal))
	}
	if got := (*terminal)[0].TerminalStatus; got != string(OutcomeFail) {
		t.Fatalf("TerminalStatus = %q, want %q", got, OutcomeFail)
	}
}
