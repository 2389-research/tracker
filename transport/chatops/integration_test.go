// ABOUTME: Proves a ThreadInterviewer drives a real tracker pipeline gate end-to-end.
package chatops

import (
	"context"
	"strings"
	"testing"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/llm"
)

// autoUI resolves each posted gate with a canned answer, standing in for a human
// clicking a Slack button. It holds a back-reference to the interviewer so
// PostGate can drive Resolve — the same call the real Slack event loop makes.
type autoUI struct {
	iv     *ThreadInterviewer
	answer GateAnswer
}

func (a *autoUI) PostGate(g Gate) error {
	go a.iv.Resolve(g.ID, a.answer)
	return nil
}
func (a *autoUI) Post(string) error { return nil }

type stubCompleter struct{}

func (stubCompleter) Complete(context.Context, *llm.Request) (*llm.Response, error) {
	return &llm.Response{Message: llm.AssistantMessage("ok"), FinishReason: llm.FinishReason{Reason: "stop"}}, nil
}

const gateDip = `workflow gate
  start: Begin
  exit: Done

  agent Begin
    label: Begin

  human Ask
    label: "proceed?"
    mode: freeform

  agent Done
    label: Done

  edges
    Begin -> Ask
    Ask -> Done
`

// TestThreadInterviewer_DrivesRealGate runs an actual pipeline whose human gate
// is answered by a ThreadInterviewer, confirming Config.Interviewer plumbs a
// Slack answer all the way into the run context.
func TestThreadInterviewer_DrivesRealGate(t *testing.T) {
	ui := &autoUI{answer: GateAnswer{Freeform: "make it so"}}
	iv := NewThreadInterviewer(ui, seqIDs())
	ui.iv = iv

	res, err := tracker.Run(context.Background(), gateDip, tracker.Config{
		Format:      "dip",
		WorkingDir:  t.TempDir(),
		LLMClient:   stubCompleter{},
		Interviewer: iv,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "success" {
		t.Fatalf("status = %q, want success", res.Status)
	}
	if got := res.Context["human_response"]; !strings.Contains(got, "make it so") {
		t.Fatalf("human_response = %q, want it to carry the Slack answer", got)
	}
}
