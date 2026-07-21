// ABOUTME: Proves a crash at a human gate resumes at the gate without re-running
// ABOUTME: the completed upstream node — tracker's node-granularity durability.
package tracker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// resumeGateDip has a real upstream agent (Work, with a prompt → calls the LLM)
// before the human gate, so we can count whether it re-runs on resume. Start and
// Done are bare agents (passthrough start/exit handlers — no LLM call).
const resumeGateDip = `workflow resumetest
  start: Start
  exit: Done

  agent Start
    label: Start

  agent Work
    prompt: do the upstream work

  human Ask
    label: "confirm?"
    mode: freeform

  agent Done
    label: Done

  edges
    Start -> Work
    Work -> Ask
    Ask -> Done
`

// countingCompleter counts Complete calls so a test can detect a re-executed node.
type countingCompleter struct {
	mu   sync.Mutex
	n    int
	resp *llm.Response
}

func (c *countingCompleter) Complete(context.Context, *llm.Request) (*llm.Response, error) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return c.resp, nil
}

func (c *countingCompleter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// erroringInterviewer fails every gate — a deterministic stand-in for a crash /
// interruption while a gate is waiting.
type erroringInterviewer struct{}

func (erroringInterviewer) Ask(string, []string, string) (string, error) {
	return "", errors.New("interrupted")
}
func (erroringInterviewer) AskFreeform(string) (string, error) {
	return "", errors.New("interrupted")
}

// TestResume_AtGate_SkipsCompletedUpstreamNode proves the durable-recovery
// contract: when a run is interrupted while a human gate is waiting, resuming
// replays from the gate node — the completed upstream agent (Work) is NOT
// re-run (its output was checkpointed before the gate blocked).
func TestResume_AtGate_SkipsCompletedUpstreamNode(t *testing.T) {
	workDir := t.TempDir()
	checkpoint := filepath.Join(workDir, "cp.json")
	resp := &llm.Response{
		Message:      llm.AssistantMessage("did the work"),
		FinishReason: llm.FinishReason{Reason: "stop"},
	}
	counter := &countingCompleter{resp: resp}

	// First run: Work executes and is checkpointed; the gate is interrupted, so
	// the run ends with the checkpoint pinned at the gate node.
	_, _ = Run(context.Background(), resumeGateDip, Config{
		Format:        "dip",
		WorkingDir:    workDir,
		CheckpointDir: checkpoint,
		LLMClient:     counter,
		Interviewer:   erroringInterviewer{},
	})
	if got := counter.count(); got != 1 {
		t.Fatalf("after first run, Work executions = %d; want 1", got)
	}
	if _, err := os.Stat(checkpoint); err != nil {
		t.Fatalf("first run should have written a checkpoint at the gate: %v", err)
	}

	// Resume from the same checkpoint: Work is completed → skipped; the gate
	// re-runs and is answered; Done finishes the run.
	res, err := Run(context.Background(), resumeGateDip, Config{
		Format:        "dip",
		WorkingDir:    workDir,
		CheckpointDir: checkpoint,
		LLMClient:     counter,
		Interviewer:   &recordingInterviewer{answer: "go"},
	})
	if err != nil {
		t.Fatalf("resume run: %v", err)
	}
	if !pipeline.TerminalStatus(res.Status).IsSuccess() {
		t.Fatalf("resume status = %q, want success", res.Status)
	}
	// Still 1: if the upstream Work node had re-run on resume, this would be 2.
	if got := counter.count(); got != 1 {
		t.Fatalf("total Work executions = %d; want 1 — the upstream node re-ran on resume", got)
	}
}
