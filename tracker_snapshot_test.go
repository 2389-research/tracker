// ABOUTME: Tests for the run-start RunSnapshot event (#475).
// ABOUTME: EventPipelineStarted carries the node inventory so a fresh subscriber can seed state.
package tracker

import (
	"context"
	"sync"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// TestRun_EmitsSnapshotOnStart asserts EventPipelineStarted carries a
// RunSnapshot with the graph's node inventory and start/exit nodes.
func TestRun_EmitsSnapshotOnStart(t *testing.T) {
	var mu sync.Mutex
	var snap *pipeline.RunSnapshot

	_, err := Run(context.Background(), gateDip, Config{
		Format:      "dip",
		WorkingDir:  t.TempDir(),
		LLMClient:   successStub(),
		Interviewer: &recordingInterviewer{answer: "go"},
		EventHandler: pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
			if evt.Type == pipeline.EventPipelineStarted && evt.Snapshot != nil {
				mu.Lock()
				snap = evt.Snapshot
				mu.Unlock()
			}
		}),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if snap == nil {
		t.Fatal("expected a RunSnapshot on EventPipelineStarted")
	}
	if snap.StartNode != "Begin" || snap.ExitNode != "Done" {
		t.Fatalf("snapshot start/exit = %q/%q, want Begin/Done", snap.StartNode, snap.ExitNode)
	}
	if len(snap.CompletedNodes) != 0 {
		t.Fatalf("fresh run should have no completed nodes, got %v", snap.CompletedNodes)
	}
	got := map[string]bool{}
	for _, n := range snap.Nodes {
		got[n.ID] = true
	}
	for _, want := range []string{"Begin", "Ask", "Done"} {
		if !got[want] {
			t.Fatalf("snapshot node inventory %v missing %q", snap.Nodes, want)
		}
	}
}
