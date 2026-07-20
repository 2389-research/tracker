// ABOUTME: Tests that EventCostUpdated is node-attributed (#475).
// ABOUTME: A subscriber can attribute cost to a node and diff per-node deltas from the stream.
package tracker

import (
	"context"
	"sync"
	"testing"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

const costDip = `workflow costtest
  start: Work
  exit: Done

  agent Work
    label: Work
    prompt:
      do the thing

  agent Done
    label: Done

  edges
    Work -> Done
`

// TestRun_CostUpdatedCarriesNodeID asserts every EventCostUpdated names the
// node whose completion triggered it and carries a cost snapshot, so a
// subscriber can attribute cost per node from the stream alone.
func TestRun_CostUpdatedCarriesNodeID(t *testing.T) {
	// A usage-bearing response so the Work node's session records tokens into
	// the trace, making the cost snapshot non-empty and firing cost events.
	client := &stubCompleter{response: &llm.Response{
		Message:      llm.AssistantMessage("done"),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 1000, OutputTokens: 500},
	}}

	var mu sync.Mutex
	var costEvents []pipeline.PipelineEvent

	_, err := Run(context.Background(), costDip, Config{
		Format:     "dip",
		WorkingDir: t.TempDir(),
		LLMClient:  client,
		Model:      "claude-sonnet-4-6",
		EventHandler: pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
			if evt.Type == pipeline.EventCostUpdated {
				mu.Lock()
				costEvents = append(costEvents, evt)
				mu.Unlock()
			}
		}),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(costEvents) == 0 {
		t.Fatal("expected at least one EventCostUpdated")
	}
	sawWork := false
	for i, evt := range costEvents {
		if evt.NodeID == "" {
			t.Fatalf("EventCostUpdated[%d] has no NodeID", i)
		}
		if evt.Cost == nil {
			t.Fatalf("EventCostUpdated[%d] has no Cost snapshot", i)
		}
		if evt.NodeID == "Work" {
			sawWork = true
		}
	}
	if !sawWork {
		t.Fatal("expected a cost update attributed to the Work node")
	}
}
