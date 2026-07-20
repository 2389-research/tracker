// ABOUTME: Tests for Config.Interviewer — the custom in-process gate seam (#474).
// ABOUTME: Covers resolveInterviewer priority ordering and an end-to-end gate drive.
package tracker

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
)

// recordingInterviewer implements the full handlers.Interviewer family and
// records which gate methods were invoked.
type recordingInterviewer struct {
	mu       sync.Mutex
	answer   string
	freeform []string
	asks     []string
}

func (r *recordingInterviewer) Ask(prompt string, _ []string, _ string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asks = append(r.asks, prompt)
	return r.answer, nil
}

func (r *recordingInterviewer) AskFreeform(prompt string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.freeform = append(r.freeform, prompt)
	return r.answer, nil
}

func (r *recordingInterviewer) AskFreeformWithLabels(prompt string, _ []string, _ string) (string, error) {
	return r.AskFreeform(prompt)
}

func (r *recordingInterviewer) AskInterview(_ []handlers.Question, _ *handlers.InterviewResult) (*handlers.InterviewResult, error) {
	return &handlers.InterviewResult{}, nil
}

func (r *recordingInterviewer) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.asks) + len(r.freeform)
}

// TestResolveInterviewer_InjectedWins verifies Config.Interviewer takes
// precedence over AutoApprove, WebhookGate, and Autopilot.
func TestResolveInterviewer_InjectedWins(t *testing.T) {
	inj := &recordingInterviewer{answer: "ok"}
	got, err := resolveInterviewer(Config{
		Interviewer: inj,
		AutoApprove: true,
		WebhookGate: &WebhookGateConfig{WebhookURL: "http://example.invalid"},
		Autopilot:   "mid",
	}, nil, nil)
	if err != nil {
		t.Fatalf("resolveInterviewer: %v", err)
	}
	if got != inj {
		t.Fatalf("expected injected interviewer to win, got %T", got)
	}
}

// TestResolveInterviewer_NilFallsThrough verifies a nil Interviewer preserves
// the existing AutoApprove > WebhookGate > Autopilot > nil chain.
func TestResolveInterviewer_NilFallsThrough(t *testing.T) {
	got, err := resolveInterviewer(Config{AutoApprove: true}, nil, nil)
	if err != nil {
		t.Fatalf("resolveInterviewer(AutoApprove): %v", err)
	}
	if _, ok := got.(*handlers.AutoApproveFreeformInterviewer); !ok {
		t.Fatalf("expected AutoApproveFreeformInterviewer, got %T", got)
	}

	got, err = resolveInterviewer(Config{}, nil, nil)
	if err != nil {
		t.Fatalf("resolveInterviewer(empty): %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil interviewer for no automation, got %T", got)
	}
}

const gateDip = `workflow gatetest
  start: Begin
  exit: Done

  agent Begin
    label: Begin

  human Ask
    label: "What next?"
    mode: freeform

  agent Done
    label: Done

  edges
    Begin -> Ask
    Ask -> Done
`

// TestRun_CustomInterviewerDrivesGate proves an injected Config.Interviewer is
// called for a wait.human gate and its answer flows into the run context.
func TestRun_CustomInterviewerDrivesGate(t *testing.T) {
	inj := &recordingInterviewer{answer: "make it so"}
	client := &stubCompleter{response: &llm.Response{
		Message:      llm.AssistantMessage("done"),
		FinishReason: llm.FinishReason{Reason: "stop"},
	}}

	result, err := Run(context.Background(), gateDip, Config{
		Format:      "dip",
		WorkingDir:  t.TempDir(),
		LLMClient:   client,
		Interviewer: inj,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if inj.calls() == 0 {
		t.Fatal("expected injected interviewer to be called for the human gate")
	}
	if !pipeline.TerminalStatus(result.Status).IsSuccess() {
		t.Fatalf("expected success terminal status, got %q", result.Status)
	}
	if got := result.Context["human_response"]; !strings.Contains(got, "make it so") {
		t.Fatalf("human_response = %q, want it to contain the interviewer answer", got)
	}
}
