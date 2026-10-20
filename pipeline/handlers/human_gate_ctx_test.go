// ABOUTME: Tests that the human handler puts the gate's identity and scope on
// ABOUTME: the per-gate context (GateInfoFrom), including inside parallel branches.
package handlers

import (
	"context"
	"sync"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// ctxRecordingInterviewer answers every choice gate with its first choice and
// records the GateInfo it found on the call's context.
type ctxRecordingInterviewer struct {
	mu    sync.Mutex
	infos []GateInfo
}

func (c *ctxRecordingInterviewer) Ask(prompt string, choices []string, def string) (string, error) {
	return c.AskContext(context.Background(), prompt, choices, def)
}

func (c *ctxRecordingInterviewer) AskContext(ctx context.Context, _ string, choices []string, _ string) (string, error) {
	info, _ := GateInfoFrom(ctx)
	c.mu.Lock()
	c.infos = append(c.infos, info)
	c.mu.Unlock()
	return choices[0], nil
}

func TestHumanHandler_PutsGateInfoOnContext(t *testing.T) {
	g := pipeline.NewGraph("gate_ctx")
	g.AddNode(&pipeline.Node{ID: "Review", Shape: "hexagon", Label: "Review it", Attrs: map[string]string{"prompt": "Look closely"}})
	g.AddNode(&pipeline.Node{ID: "Done", Shape: "Msquare"})
	g.AddEdge(&pipeline.Edge{From: "Review", To: "Done", Label: "approve"})
	iv := &ctxRecordingInterviewer{}
	h := NewHumanHandler(iv, g)
	if _, err := h.Execute(context.Background(), g.Nodes["Review"], pipeline.NewPipelineContext()); err != nil {
		t.Fatal(err)
	}
	if len(iv.infos) != 1 {
		t.Fatalf("interviewer called %d times", len(iv.infos))
	}
	got := iv.infos[0]
	if got.NodeID != "Review" || got.Mode != "choice" || got.Label != "Review it" || got.Prompt != "Review it\n\nLook closely" || len(got.ScopePath) != 0 {
		t.Errorf("GateInfo = %+v", got)
	}
}

func TestHumanHandler_GateInfoScopeInParallelBranch(t *testing.T) {
	g := buildTestGraph([]string{"B1", "B2"}, "gate")
	for _, id := range []string{"B1", "B2"} {
		g.Nodes[id].Label = "Pick " + id
		g.AddNode(&pipeline.Node{ID: id + "_next", Shape: "box"})
		g.AddEdge(&pipeline.Edge{From: id, To: id + "_next", Label: "go"})
	}
	iv := &ctxRecordingInterviewer{}
	human := NewHumanHandler(iv, g)
	reg := pipeline.NewHandlerRegistry()
	reg.Register(&stubHandler{name: "gate", execFunc: human.Execute})
	ph := NewParallelHandler(g, reg, nil)
	if _, err := ph.Execute(context.Background(), g.Nodes["parallel_node"], pipeline.NewPipelineContext()); err != nil {
		t.Fatal(err)
	}
	byNode := map[string]GateInfo{}
	for _, info := range iv.infos {
		byNode[info.NodeID] = info
	}
	for i, id := range []string{"B1", "B2"} {
		info, ok := byNode[id]
		if !ok {
			t.Fatalf("no gate call for %s", id)
		}
		// The gate is the branch target, so its node ID already names it.
		if len(info.ScopePath) != 0 || info.BranchIndex != i {
			t.Errorf("%s: ScopePath=%v BranchIndex=%d; want [], %d", id, info.ScopePath, info.BranchIndex, i)
		}
	}
}
