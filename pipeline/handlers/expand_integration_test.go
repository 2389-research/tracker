package handlers

import (
	"context"
	"strings"
	"testing"

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
					"prompt": "Goal: ${graph.goal}\nUser input: ${ctx.human_response}",
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
	subgraphHandler := pipeline.NewSubgraphHandler(graphs, registry)
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
