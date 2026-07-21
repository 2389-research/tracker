// ABOUTME: ThreadInterviewer implements the tracker human-gate interfaces over a Slack thread.
// ABOUTME: Each gate is posted to the thread and blocks until the thread resolves it.
package chatops

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
)

// errGateCanceled is returned to the pipeline when a gate is abandoned (run
// cancelled or the interviewer torn down) so the human handler routes to fail.
var errGateCanceled = errors.New("slack gate canceled")

// ThreadInterviewer implements tracker's human-gate interviewer interfaces by
// presenting each gate in a Slack thread and blocking until the thread resolves
// it (a button click, modal submit, or reply). One instance serves one run.
//
// It implements the full family (Interviewer, FreeformInterviewer,
// LabeledFreeformInterviewer, InterviewInterviewer) plus Actor()/Cancel()/
// SetPipelineContext, so tracker's human handler picks the richest mode.
type ThreadInterviewer struct {
	ui    ThreadUI
	newID func() string

	mu      sync.Mutex
	pending map[string]chan GateAnswer
	pctx    context.Context

	cancelOnce sync.Once
	canceled   chan struct{}
}

// NewThreadInterviewer builds an interviewer bound to a thread's UI. newID must
// return a fresh unique id per call (used to correlate answers).
func NewThreadInterviewer(ui ThreadUI, newID func() string) *ThreadInterviewer {
	return &ThreadInterviewer{
		ui:       ui,
		newID:    newID,
		pending:  make(map[string]chan GateAnswer),
		canceled: make(chan struct{}),
	}
}

// Compile-time proof the full interviewer family is satisfied.
var (
	_ handlers.Interviewer                = (*ThreadInterviewer)(nil)
	_ handlers.FreeformInterviewer        = (*ThreadInterviewer)(nil)
	_ handlers.LabeledFreeformInterviewer = (*ThreadInterviewer)(nil)
	_ handlers.InterviewInterviewer       = (*ThreadInterviewer)(nil)
)

// Actor marks answers as human-driven for override auditing.
func (s *ThreadInterviewer) Actor() pipeline.Actor { return pipeline.ActorHuman }

// SetPipelineContext lets a run cancellation unblock a waiting gate. Guarded
// because parallel-branch human gates can drive one interviewer concurrently.
func (s *ThreadInterviewer) SetPipelineContext(ctx context.Context) {
	s.mu.Lock()
	s.pctx = ctx
	s.mu.Unlock()
}

// PendingClearer is an optional ThreadUI capability: clear a thread's pending
// freeform gate when it stops waiting (resolved or abandoned), so a later
// unrelated reply isn't consumed by a stale gate.
type PendingClearer interface {
	ClearPending(gateID string)
}

// Cancel abandons every waiting gate (idempotent). The Slack transport calls it
// on run teardown; tracker's Engine.Close also calls it.
func (s *ThreadInterviewer) Cancel() {
	s.cancelOnce.Do(func() { close(s.canceled) })
}

// Ask presents a choice (or yes/no) gate and returns the chosen label.
func (s *ThreadInterviewer) Ask(prompt string, choices []string, def string) (string, error) {
	kind := GateChoice
	if isYesNo(choices) {
		kind = GateYesNo
	}
	ans, err := s.await(Gate{Kind: kind, Prompt: prompt, Choices: choices, Default: def})
	if err != nil {
		return "", err
	}
	return ans.Choice, nil
}

// AskFreeform presents an open-ended gate and returns the reply text.
func (s *ThreadInterviewer) AskFreeform(prompt string) (string, error) {
	ans, err := s.await(Gate{Kind: GateFreeform, Prompt: prompt})
	if err != nil {
		return "", err
	}
	return ans.Freeform, nil
}

// AskFreeformWithLabels presents selectable labels alongside a freeform "other"
// escape hatch; a typed reply wins over a selected label.
func (s *ThreadInterviewer) AskFreeformWithLabels(prompt string, labels []string, def string) (string, error) {
	ans, err := s.await(Gate{Kind: GateChoice, Prompt: prompt, Choices: labels, Default: def})
	if err != nil {
		return "", err
	}
	if ans.Freeform != "" {
		return ans.Freeform, nil
	}
	return ans.Choice, nil
}

// AskInterview presents a structured form as a sequence of one-question-at-a-time
// thread gates (buttons for options / yes-no, a reply for open-ended), matching
// the TUI's flow and reusing the same button/reply machinery — no Slack modal
// needed. A cancelled interview returns a Canceled result (not an error) so the
// pipeline routes on cancellation.
func (s *ThreadInterviewer) AskInterview(questions []handlers.Question, _ *handlers.InterviewResult) (*handlers.InterviewResult, error) {
	answers := make([]handlers.InterviewAnswer, 0, len(questions))
	for i, q := range questions {
		ans, err := s.await(questionGate(q, i+1, len(questions)))
		if errors.Is(err, errGateCanceled) {
			return &handlers.InterviewResult{Questions: answers, Canceled: true, Incomplete: true}, nil
		}
		if err != nil {
			return nil, err
		}
		answers = append(answers, handlers.InterviewAnswer{
			ID:      fmt.Sprintf("q%d", q.Index),
			Text:    q.Text,
			Options: q.Options,
			Answer:  interviewAnswerText(ans),
		})
	}
	return &handlers.InterviewResult{Questions: answers}, nil
}

// questionGate renders one interview question as a thread gate: yes/no or option
// buttons when the question is closed, else an open-ended reply.
func questionGate(q handlers.Question, n, total int) Gate {
	prompt := fmt.Sprintf("*(%d/%d)* %s", n, total, q.Text)
	if q.Context != "" {
		prompt += "\n_" + q.Context + "_"
	}
	switch {
	case q.IsYesNo:
		return Gate{Kind: GateYesNo, Prompt: prompt, Choices: []string{"Yes", "No"}}
	case len(q.Options) > 0:
		return Gate{Kind: GateChoice, Prompt: prompt, Choices: q.Options}
	default:
		return Gate{Kind: GateFreeform, Prompt: prompt}
	}
}

func interviewAnswerText(ans GateAnswer) string {
	if ans.Freeform != "" {
		return ans.Freeform
	}
	return ans.Choice
}

// Resolve delivers a human's answer to the gate identified by gateID. The Slack
// event loop calls it on a button click / modal submit / reply. Returns false
// if no such gate is pending (already answered, unknown, or torn down).
func (s *ThreadInterviewer) Resolve(gateID string, ans GateAnswer) bool {
	s.mu.Lock()
	ch, ok := s.pending[gateID]
	s.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- ans:
		return true
	default:
		return false // already resolved
	}
}

// await registers a gate, posts it, and blocks until it is resolved, the
// pipeline context is cancelled, or the interviewer is torn down.
func (s *ThreadInterviewer) await(g Gate) (GateAnswer, error) {
	g.ID = s.newID()
	ch := make(chan GateAnswer, 1)
	s.mu.Lock()
	s.pending[g.ID] = ch
	s.mu.Unlock()
	defer s.cleanup(g)

	if err := s.ui.PostGate(g); err != nil {
		return GateAnswer{}, err
	}

	select {
	case ans := <-ch:
		if ans.Canceled {
			return GateAnswer{}, errGateCanceled
		}
		return ans, nil
	case <-s.pipelineDone():
		return GateAnswer{}, errGateCanceled
	case <-s.canceled:
		return GateAnswer{}, errGateCanceled
	}
}

// cleanup removes the gate's pending channel and asks the transport to clear any
// per-thread pending entry it armed for this gate. ClearPending is match-guarded
// (it clears only if the transport's pending slot still points at g.ID), so this
// is called for EVERY gate kind: a transport that arms its slot only for freeform
// (Slack) treats a choice-gate clear as a harmless no-op, while a transport that
// arms its single slot for all kinds (the CLI REPL) needs the clear on an
// abandoned choice/labeled gate — otherwise the stale slot swallows the user's
// next request.
func (s *ThreadInterviewer) cleanup(g Gate) {
	s.mu.Lock()
	delete(s.pending, g.ID)
	s.mu.Unlock()
	if pc, ok := s.ui.(PendingClearer); ok {
		pc.ClearPending(g.ID)
	}
}

// pipelineDone returns the pipeline context's Done channel, or nil (which blocks
// forever in a select) when no context has been set.
func (s *ThreadInterviewer) pipelineDone() <-chan struct{} {
	s.mu.Lock()
	pctx := s.pctx
	s.mu.Unlock()
	if pctx == nil {
		return nil
	}
	return pctx.Done()
}

// isYesNo reports whether choices are exactly a Yes/No pair (order-insensitive),
// so the gate can render as two buttons rather than a radio list.
func isYesNo(choices []string) bool {
	if len(choices) != 2 {
		return false
	}
	a, b := strings.ToLower(choices[0]), strings.ToLower(choices[1])
	return (a == "yes" && b == "no") || (a == "no" && b == "yes")
}
