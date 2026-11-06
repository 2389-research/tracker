package handlers

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// TestVariableExpansion_Integration tests that variables are properly expanded in codergen handler
func TestVariableExpansion_Integration(t *testing.T) {
	// Create a mock LLM client
	mock := &mockLLMClient{
		response: "STATUS:success",
	}

	// Create a graph with variable references
	graph := &pipeline.Graph{
		Name: "test",
		Attrs: map[string]string{
			"goal": "Test variable expansion",
		},
		Nodes: map[string]*pipeline.Node{
			"test": {
				ID:    "test",
				Shape: "agent",
				Attrs: map[string]string{
					"prompt":      "Goal: ${graph.goal}\nUser input: ${ctx.human_response}",
					"auto_status": "true",
				},
			},
		},
		StartNode: "test",
		ExitNode:  "test",
	}

	// Create context with human response
	ctx := pipeline.NewPipelineContext()
	ctx.Set("human_response", "build a test app")

	// Create handler
	handler := NewCodergenHandler(mock, "/tmp", WithGraphAttrs(graph.Attrs))

	// Execute
	outcome, err := handler.Execute(context.Background(), graph.Nodes["test"], ctx)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("got status %q, want success", outcome.Status)
	}

	// Verify that the prompt was expanded correctly
	if !strings.Contains(mock.lastPrompt, "Goal: Test variable expansion") {
		t.Errorf("graph.goal not expanded in prompt: %s", mock.lastPrompt)
	}
	if !strings.Contains(mock.lastPrompt, "User input: build a test app") {
		t.Errorf("ctx.human_response not expanded in prompt: %s", mock.lastPrompt)
	}
}

// TestSubgraphParamInjection_Integration tests that params are injected into subgraphs
func TestSubgraphParamInjection_Integration(t *testing.T) {
	// Create child graph with param variables
	childGraph := &pipeline.Graph{
		Name: "child",
		Attrs: map[string]string{
			"goal": "Process task",
		},
		Nodes: map[string]*pipeline.Node{
			"process": {
				ID:      "process",
				Shape:   "agent",
				Handler: "codergen",
				Attrs: map[string]string{
					"prompt":      "Executing: ${params.task} at severity ${params.severity}",
					"auto_status": "true",
				},
			},
		},
		StartNode: "process",
		ExitNode:  "process",
	}

	// Create parent graph with subgraph call
	parentGraph := &pipeline.Graph{
		Name: "parent",
		Attrs: map[string]string{
			"goal": "Run child",
		},
		Nodes: map[string]*pipeline.Node{
			"call_child": {
				ID:      "call_child",
				Shape:   "subgraph",
				Handler: "subgraph",
				Attrs: map[string]string{
					"subgraph_ref":    "child",
					"subgraph_params": "task=code review,severity=high",
				},
			},
		},
		StartNode: "call_child",
		ExitNode:  "call_child",
	}

	// Create mock client
	mock := &mockLLMClient{
		response: "STATUS:success",
	}

	// Create registry
	registry := pipeline.NewHandlerRegistry()

	// Register agent handler
	agentHandler := NewCodergenHandler(mock, "/tmp", WithGraphAttrs(childGraph.Attrs))
	registry.Register(agentHandler)

	// Register subgraph handler
	graphs := map[string]*pipeline.Graph{
		"child": childGraph,
	}
	subgraphHandler := pipeline.NewSubgraphHandler(graphs, registry, nil, nil)
	registry.Register(subgraphHandler)

	// Run parent graph
	engine := pipeline.NewEngine(parentGraph, registry)
	result, err := engine.Run(context.Background())

	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if result.Status != pipeline.OutcomeSuccess {
		t.Errorf("got status %q, want success", result.Status)
	}

	// Verify params were expanded
	if !strings.Contains(mock.lastPrompt, "code review") {
		t.Errorf("params.task not expanded: %s", mock.lastPrompt)
	}
	if !strings.Contains(mock.lastPrompt, "high") {
		t.Errorf("params.severity not expanded: %s", mock.lastPrompt)
	}
}

// mockLLMClient for testing
type mockLLMClient struct {
	response   string
	lastPrompt string
}

func (m *mockLLMClient) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	// Capture the last message for verification
	if len(req.Messages) > 0 {
		lastMsg := req.Messages[len(req.Messages)-1]
		// Extract text from content parts
		var textParts []string
		for _, part := range lastMsg.Content {
			if part.Kind == llm.KindText {
				textParts = append(textParts, part.Text)
			}
		}
		m.lastPrompt = strings.Join(textParts, "\n")
	}

	return &llm.Response{
		Message: llm.Message{
			Role: "assistant",
			Content: []llm.ContentPart{
				{Kind: llm.KindText, Text: m.response},
			},
		},
		Usage: llm.Usage{
			InputTokens:  100,
			OutputTokens: 50,
		},
	}, nil
}

func (m *mockLLMClient) MaxContextWindow() int {
	return 128000
}

func (m *mockLLMClient) SupportsStreaming() bool {
	return false
}

func TestSubgraphScopedEvents_Integration(t *testing.T) {
	// Build child graph: start -> agent -> exit
	childGraph := pipeline.NewGraph("child")
	childGraph.AddNode(&pipeline.Node{ID: "cs", Shape: "Mdiamond", Label: "ChildStart"})
	childGraph.AddNode(&pipeline.Node{ID: "ca", Shape: "box", Label: "ChildAgent", Attrs: map[string]string{"auto_status": "true", "prompt": "say hello"}})
	childGraph.AddNode(&pipeline.Node{ID: "ce", Shape: "Msquare", Label: "ChildEnd"})
	childGraph.AddEdge(&pipeline.Edge{From: "cs", To: "ca"})
	childGraph.AddEdge(&pipeline.Edge{From: "ca", To: "ce"})

	// Build parent graph: start -> subgraph -> exit
	parentGraph := pipeline.NewGraph("parent")
	parentGraph.AddNode(&pipeline.Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	parentGraph.AddNode(&pipeline.Node{ID: "sg", Shape: "tab", Label: "SubA", Attrs: map[string]string{"subgraph_ref": "child"}})
	parentGraph.AddNode(&pipeline.Node{ID: "end", Shape: "Msquare", Label: "End"})
	parentGraph.AddEdge(&pipeline.Edge{From: "s", To: "sg"})
	parentGraph.AddEdge(&pipeline.Edge{From: "sg", To: "end"})

	// Collect all pipeline events.
	var events []pipeline.PipelineEvent
	handler := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		events = append(events, evt)
	})

	mock := &mockLLMClient{response: "STATUS:success"}

	// Use NewDefaultRegistry — the same path as the TUI.
	subgraphs := map[string]*pipeline.Graph{"child": childGraph}
	registry := NewDefaultRegistry(parentGraph,
		WithLLMClient(mock, "/tmp"),
		WithPipelineEventHandler(handler),
		WithSubgraphs(subgraphs),
	)

	engine := pipeline.NewEngine(parentGraph, registry,
		pipeline.WithPipelineEventHandler(handler),
	)

	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected success, got %q", result.Status)
	}

	// Check for scoped stage_started events from the child.
	scopedStarted := map[string]bool{}
	for _, evt := range events {
		if evt.Type == pipeline.EventStageStarted && strings.Contains(evt.NodeID, "/") {
			scopedStarted[evt.NodeID] = true
		}
	}

	expected := []string{"sg/cs", "sg/ca", "sg/ce"}
	for _, want := range expected {
		if !scopedStarted[want] {
			t.Errorf("missing scoped event for %q; got events: %v", want, scopedStarted)
		}
	}

	// Verify parent events are also present (unscoped).
	parentStarted := map[string]bool{}
	for _, evt := range events {
		if evt.Type == pipeline.EventStageStarted && !strings.Contains(evt.NodeID, "/") {
			parentStarted[evt.NodeID] = true
		}
	}
	for _, want := range []string{"s", "sg", "end"} {
		if !parentStarted[want] {
			t.Errorf("missing parent event for %q; got events: %v", want, parentStarted)
		}
	}
}

// TestSubgraphAgentEventsShareTheirNodesScopedID is the regression test for
// #717: an agent nested in a subgraph must report under the SAME node id in
// every stream.
//
// run.json derives per-node usage from the activity log by node id, taking each
// node's most direct tier (runusage.go). The subgraph's own pipeline events are
// scoped "Sg/Child" — decision_outcome carries that node's token rollup — while
// the agent's call-level lines (llm_finish / turn_metrics) are stamped by the
// agent package. Overwriting the stamp there put those lines under "Child"
// (or "Sg"), a node id no decision_outcome ever names, so both the call tier
// under one id and the rollup tier under the other were counted: the run's
// totals came out at twice the budget guard's snapshot for the same run.
func TestSubgraphAgentEventsShareTheirNodesScopedID(t *testing.T) {
	childGraph := pipeline.NewGraph("child")
	childGraph.AddNode(&pipeline.Node{ID: "cs", Shape: "Mdiamond", Label: "ChildStart"})
	childGraph.AddNode(&pipeline.Node{
		ID: "Child", Shape: "box", Label: "ChildAgent",
		Attrs: map[string]string{"prompt": "say hello", "llm_model": "stub"}, // prompt required: a promptless agent node runs no session
	})
	childGraph.AddNode(&pipeline.Node{ID: "ce", Shape: "Msquare", Label: "ChildEnd"})
	childGraph.AddEdge(&pipeline.Edge{From: "cs", To: "Child"})
	childGraph.AddEdge(&pipeline.Edge{From: "Child", To: "ce"})

	parentGraph := pipeline.NewGraph("parent")
	parentGraph.AddNode(&pipeline.Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	parentGraph.AddNode(&pipeline.Node{ID: "Sg", Shape: "tab", Label: "SubA", Attrs: map[string]string{"subgraph_ref": "child"}})
	parentGraph.AddNode(&pipeline.Node{ID: "end", Shape: "Msquare", Label: "End"})
	parentGraph.AddEdge(&pipeline.Edge{From: "s", To: "Sg"})
	parentGraph.AddEdge(&pipeline.Edge{From: "Sg", To: "end"})

	var mu sync.Mutex
	var agentNodeIDs []string
	var pipelineOutcomeIDs []string
	agentEvents := agent.EventHandlerFunc(func(evt agent.Event) {
		if evt.Type != agent.EventLLMFinish && evt.Type != agent.EventTurnMetrics {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		agentNodeIDs = append(agentNodeIDs, evt.NodeID)
	})
	pipelineEvents := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		if evt.Type != pipeline.EventDecisionOutcome || evt.Decision == nil {
			return
		}
		if evt.Decision.TokenInput == 0 && evt.Decision.TokenOutput == 0 {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		pipelineOutcomeIDs = append(pipelineOutcomeIDs, evt.NodeID)
	})

	registry := NewDefaultRegistry(parentGraph,
		WithLLMClient(&mockLLMClient{response: "done"}, t.TempDir()),
		WithPipelineEventHandler(pipelineEvents),
		WithAgentEventHandler(agentEvents),
		WithSubgraphs(map[string]*pipeline.Graph{"child": childGraph}),
	)
	engine := pipeline.NewEngine(parentGraph, registry, pipeline.WithPipelineEventHandler(pipelineEvents))
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(agentNodeIDs) == 0 {
		t.Fatal("the agent emitted no usage-bearing events; the test proves nothing")
	}
	if len(pipelineOutcomeIDs) == 0 {
		t.Fatal("no usage-bearing decision_outcome; the test proves nothing")
	}
	for _, id := range agentNodeIDs {
		if id != "Sg/Child" {
			t.Errorf("agent usage event node_id = %q, want %q (the same id its decision_outcome uses)", id, "Sg/Child")
		}
	}
	for _, id := range pipelineOutcomeIDs {
		if id != "Sg/Child" {
			t.Errorf("decision_outcome node_id = %q, want %q", id, "Sg/Child")
		}
	}
}
