// ABOUTME: Tests for issue #309 — the adapter maps dippin's graph-level
// ABOUTME: `on_failure:` default route into graph.Attrs["fallback_target"] so the
// ABOUTME: #295 strict-failure cascade routes a bare agent failure to it.
package pipeline

import (
	"context"
	"testing"
	"time"
)

// onFailureDip is a minimal workflow whose `Work` agent has only an
// unconditional edge — on failure it must route via the graph-level
// `on_failure: Escalate` default rather than dead-stopping.
const onFailureDip = `workflow OnFailureTest
  start: Start
  exit: Done
  defaults
    on_failure: Escalate
  agent Start
    label: Start
  agent Work
    label: Work
    prompt: do the work
  agent Escalate
    label: Escalate
    prompt: escalate to a human
  agent Done
    label: Done
  edges
    Start -> Work
    Work -> Done
    Escalate -> Done
`

// TestAdapterMapsOnFailureToGraphFallback is the unit-level round-trip: a
// workflow-level `on_failure: Escalate` must land in graph.Attrs["fallback_target"]
// (the key the engine's findFallbackTarget reads).
func TestAdapterMapsOnFailureToGraphFallback(t *testing.T) {
	g, _, err := LoadDippinWorkflow(onFailureDip, "on_failure.dip")
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v", err)
	}
	if got := g.Attrs["fallback_target"]; got != "Escalate" {
		t.Errorf("graph fallback_target = %q, want %q (issue #309: on_failure must map to the engine's fallback key)", got, "Escalate")
	}
}

// TestOnFailureRoutesFailingAgentAtRuntime is the integration test over a BUILT
// workflow: load the .dip through the adapter and run it through the engine; a
// failing agent with no node-level route must reach the on_failure node.
func TestOnFailureRoutesFailingAgentAtRuntime(t *testing.T) {
	g, _, err := LoadDippinWorkflow(onFailureDip, "on_failure.dip")
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v", err)
	}
	reg := newTestRegistryWithOutcomes(map[string]Outcome{
		"Work": {Status: string(OutcomeFail)},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := NewEngine(g, reg).Run(ctx)
	if err != nil {
		t.Fatalf("engine.Run error: %v", err)
	}
	if edgeTo, ok := traceEdgeTo(result.Trace, "Work"); !ok || edgeTo != "Escalate" {
		t.Fatalf("Work trace EdgeTo = %q (found=%v), want %q — on_failure default not honored at runtime (issue #309)", edgeTo, ok, "Escalate")
	}
}

// onFailurePrecedenceDip declares both a graph-level on_failure and a node-level
// fallback_target on Work; the node-level route must win.
const onFailurePrecedenceDip = `workflow OnFailurePrecedence
  start: Start
  exit: Done
  defaults
    on_failure: GraphEscalate
  agent Start
    label: Start
  agent Work
    label: Work
    prompt: do the work
    fallback_target: NodeEscalate
  agent NodeEscalate
    label: NodeEscalate
    prompt: node-level escalate
  agent GraphEscalate
    label: GraphEscalate
    prompt: graph-level escalate
  agent Done
    label: Done
  edges
    Start -> Work
    Work -> Done
    NodeEscalate -> Done
    GraphEscalate -> NodeEscalate
`

// TestNodeFallbackBeatsGraphOnFailure pins the precedence acceptance criterion:
// a node-level fallback_target wins over the graph-level on_failure default.
func TestNodeFallbackBeatsGraphOnFailure(t *testing.T) {
	g, _, err := LoadDippinWorkflow(onFailurePrecedenceDip, "on_failure_precedence.dip")
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v", err)
	}
	if got := g.Attrs["fallback_target"]; got != "GraphEscalate" {
		t.Fatalf("graph fallback_target = %q, want %q (precondition for the precedence test)", got, "GraphEscalate")
	}
	reg := newTestRegistryWithOutcomes(map[string]Outcome{
		"Work": {Status: string(OutcomeFail)},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := NewEngine(g, reg).Run(ctx)
	if err != nil {
		t.Fatalf("engine.Run error: %v", err)
	}
	if edgeTo, _ := traceEdgeTo(result.Trace, "Work"); edgeTo != "NodeEscalate" {
		t.Fatalf("Work trace EdgeTo = %q, want node-level %q (node fallback_target must beat graph on_failure, issue #309)", edgeTo, "NodeEscalate")
	}
}
