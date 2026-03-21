// ABOUTME: Tests for reasoning_effort field wiring from node attributes to LLM requests.
package handlers

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

func TestCodergenHandler_ReasoningEffort(t *testing.T) {
	tests := []struct {
		name               string
		nodeAttrs          map[string]string
		graphAttrs         map[string]string
		expectedEffort     string
	}{
		{
			name: "node level reasoning_effort",
			nodeAttrs: map[string]string{
				"prompt":           "test prompt",
				"reasoning_effort": "high",
			},
			expectedEffort: "high",
		},
		{
			name: "graph level reasoning_effort",
			nodeAttrs: map[string]string{
				"prompt": "test prompt",
			},
			graphAttrs: map[string]string{
				"reasoning_effort": "medium",
			},
			expectedEffort: "medium",
		},
		{
			name: "node overrides graph",
			nodeAttrs: map[string]string{
				"prompt":           "test prompt",
				"reasoning_effort": "low",
			},
			graphAttrs: map[string]string{
				"reasoning_effort": "high",
			},
			expectedEffort: "low",
		},
		{
			name: "no reasoning_effort specified",
			nodeAttrs: map[string]string{
				"prompt": "test prompt",
			},
			expectedEffort: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &pipeline.Node{
				ID:      "test",
				Handler: "codergen",
				Attrs:   tt.nodeAttrs,
			}

			// Create mock client that captures the reasoning_effort in the request
			var capturedRequest *llm.Request
			client := &requestCapturingCompleter{
				onRequest: func(req *llm.Request) {
					capturedRequest = req
				},
			}

			opts := []CodergenOption{}
			if tt.graphAttrs != nil {
				opts = append(opts, WithGraphAttrs(tt.graphAttrs))
			}
			handler := NewCodergenHandler(client, t.TempDir(), opts...)
			pctx := pipeline.NewPipelineContext()

			_, err := handler.Execute(context.Background(), node, pctx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if capturedRequest == nil {
				t.Fatal("expected request to be captured")
			}

			if capturedRequest.ReasoningEffort != tt.expectedEffort {
				t.Errorf("expected ReasoningEffort=%q, got %q", tt.expectedEffort, capturedRequest.ReasoningEffort)
			}
		})
	}
}

// requestCapturingCompleter captures the llm.Request for inspection in tests
type requestCapturingCompleter struct {
	onRequest func(*llm.Request)
}

func (c *requestCapturingCompleter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if c.onRequest != nil {
		c.onRequest(req)
	}
	return &llm.Response{
		Message:      llm.AssistantMessage("ok"),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}, nil
}
