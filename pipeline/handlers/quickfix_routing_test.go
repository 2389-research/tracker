// ABOUTME: quickfix.dip routing on the real engine: free text at ReviewDiff revises, the revise
// ABOUTME: budget ends at FinalDecision, red tests still reach the gate, auto-approve discards.
package handlers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func loadQuickfixGraph(t *testing.T, change string) *pipeline.Graph {
	t.Helper()
	path := filepath.Join("..", "..", "examples", "quickfix.dip")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	g, _, err := pipeline.LoadDippinWorkflow(string(source), path)
	if err != nil {
		t.Fatalf("LoadDippinWorkflow: %v", err)
	}
	if change != "" {
		g.Attrs["params.change"] = change
	}
	return g
}

// scriptedGate answers each human gate from a queue, in order, and records
// every prompt it was shown.
type scriptedGate struct {
	mu      sync.Mutex
	answers []string
	prompts []string
}

func (s *scriptedGate) next(prompt string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts = append(s.prompts, prompt)
	if len(s.answers) == 0 {
		return "", context.Canceled
	}
	a := s.answers[0]
	s.answers = s.answers[1:]
	return a, nil
}

func (s *scriptedGate) Ask(prompt string, _ []string, _ string) (string, error) {
	return s.next(prompt)
}
func (s *scriptedGate) AskFreeform(prompt string) (string, error) { return s.next(prompt) }
func (s *scriptedGate) AskFreeformWithLabels(prompt string, _ []string, _ string) (string, error) {
	return s.next(prompt)
}

// quickfixRun runs quickfix with stub tool and agent nodes: every tool
// succeeds (stdout "<node>-ok") unless toolOutcomes overrides it.
type quickfixRun struct {
	toolOutcomes map[string]pipeline.Outcome
	agentCalls   map[string]int
	visited      map[string]int // stage_started per node (a restart clears CompletedNodes)
	// revisionFeedback is what the second MakeChange run sees as feedback.
	revisionFeedback map[string]string
	mu               sync.Mutex
}

func (q *quickfixRun) run(t *testing.T, g *pipeline.Graph, iv Interviewer) *pipeline.EngineResult {
	t.Helper()
	q.agentCalls = map[string]int{}
	q.visited = map[string]int{}
	q.revisionFeedback = map[string]string{}
	tool := func(_ context.Context, node *pipeline.Node, _ *pipeline.PipelineContext) (pipeline.Outcome, error) {
		if out, ok := q.toolOutcomes[node.ID]; ok {
			return out, nil
		}
		return pipeline.Outcome{Status: pipeline.OutcomeSuccess, ContextUpdates: map[string]string{pipeline.ContextKeyToolStdout: node.ID + "-ok"}}, nil
	}
	agent := func(_ context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
		q.mu.Lock()
		q.agentCalls[node.ID]++
		if node.ID == "MakeChange" && q.agentCalls[node.ID] == 2 {
			for _, k := range []string{"node.ReviewDiff.human_response", "node.AskRevision.human_response"} {
				q.revisionFeedback[k], _ = pctx.Get(k)
			}
		}
		q.mu.Unlock()
		return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
	}
	reg := NewDefaultRegistry(g, WithToolExecFunc(tool), WithCodergenFunc(agent), WithInterviewer(iv, g))
	events := pipeline.PipelineEventHandlerFunc(func(e pipeline.PipelineEvent) {
		if e.Type == pipeline.EventStageStarted {
			q.mu.Lock()
			q.visited[e.NodeID]++
			q.mu.Unlock()
		}
	})
	res, err := pipeline.NewEngine(g, reg, pipeline.WithArtifactDir(t.TempDir()), pipeline.WithPipelineEventHandler(events)).Run(context.Background())
	if err != nil {
		t.Fatalf("engine run: %v", err)
	}
	return res
}

func (q *quickfixRun) reached(id string) bool { return q.visited[id] > 0 }

func TestQuickfix_FreeTextRoutesToAskRevision(t *testing.T) {
	g := loadQuickfixGraph(t, "fix the README typo")
	iv := &scriptedGate{answers: []string{"also fix the second typo", "the one in the last line", "approve"}}
	q := &quickfixRun{}
	res := q.run(t, g, iv)
	if res.Status != pipeline.OutcomeSuccess {
		t.Fatalf("status = %q, want success", res.Status)
	}
	for _, id := range []string{"AskRevision", "CheckReviseBudget", "Commit"} {
		if !q.reached(id) {
			t.Errorf("%s not reached; visited = %v", id, q.visited)
		}
	}
	if q.reached("Discard") {
		t.Error("free text at ReviewDiff must never discard")
	}
	if q.visited["ReviewDiff"] != 2 || q.agentCalls["MakeChange"] != 2 {
		t.Errorf("ReviewDiff ran %d and MakeChange %d times, want 2 each (the change, then the revision)", q.visited["ReviewDiff"], q.agentCalls["MakeChange"])
	}
	if got := q.revisionFeedback["node.ReviewDiff.human_response"]; got != "also fix the second typo" {
		t.Errorf("the revision run saw ReviewDiff feedback %q, want the typed text", got)
	}
	if got := q.revisionFeedback["node.AskRevision.human_response"]; got != "the one in the last line" {
		t.Errorf("the revision run saw AskRevision feedback %q, want the answer", got)
	}
}

func TestQuickfix_ReviseExhaustionReachesFinalDecision(t *testing.T) {
	g := loadQuickfixGraph(t, "fix the README typo")
	iv := &scriptedGate{answers: []string{"revise", "do it differently", "reject"}}
	q := &quickfixRun{toolOutcomes: map[string]pipeline.Outcome{
		"CheckReviseBudget": {Status: pipeline.OutcomeFail, FailureReason: "revise budget exhausted"},
	}}
	res := q.run(t, g, iv)
	if !q.reached("FinalDecision") || !q.reached("Discard") {
		t.Fatalf("exhaustion must reach FinalDecision then Discard on reject; visited = %v", q.visited)
	}
	if q.reached("AbortRun") || q.reached("Commit") {
		t.Errorf("exhaustion must not abort or commit; visited = %v", q.visited)
	}
	if res.Status != pipeline.OutcomeSuccess {
		t.Errorf("status = %q, want success (a rejected change is a clean end)", res.Status)
	}
}

func TestQuickfix_RedTestsReachGate(t *testing.T) {
	g := loadQuickfixGraph(t, "fix the README typo")
	iv := &scriptedGate{answers: []string{"reject"}}
	q := &quickfixRun{toolOutcomes: map[string]pipeline.Outcome{
		"RunTests": {Status: pipeline.OutcomeSuccess, ContextUpdates: map[string]string{pipeline.ContextKeyToolStdout: "--- FAIL: TestX\n`go test ./...` failed (exit 1)\ntests-fail"}},
	}}
	q.run(t, g, iv)
	if !q.reached("ReviewDiff") {
		t.Fatalf("red tests must reach the ReviewDiff gate; visited = %v", q.visited)
	}
	if len(iv.prompts) == 0 || !strings.Contains(iv.prompts[0], "tests-fail") {
		t.Errorf("the gate must show the red test result; prompt = %q", iv.prompts)
	}
}

func TestQuickfix_AutoApproveRejects(t *testing.T) {
	g := loadQuickfixGraph(t, "fix the README typo")
	q := &quickfixRun{}
	q.run(t, g, &AutoApproveFreeformInterviewer{})
	if !q.reached("Discard") || q.reached("Commit") {
		t.Fatalf("--auto-approve must take ReviewDiff's default (reject); visited = %v", q.visited)
	}
}

func TestQuickfix_NoParamAsksFirst(t *testing.T) {
	g := loadQuickfixGraph(t, "")
	iv := &scriptedGate{answers: []string{"fix the README typo", "reject"}}
	q := &quickfixRun{}
	q.run(t, g, iv)
	if !q.reached("AskChange") {
		t.Fatalf("without --param change the change is asked for; visited = %v", q.visited)
	}
}
