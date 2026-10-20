// ABOUTME: PausingInterviewer — pause mode: a gate with no one to answer it pauses
// ABOUTME: the run with an exact record; a held answer is applied once on resume.
package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// questionChangedNote tells the person answering why an answer was dropped.
const questionChangedNote = "the question changed since you answered — answer again"

// GatePauser is implemented by an interviewer that pauses gates instead of
// answering them (PausingInterviewer). The human handler uses it to fail a
// gate closed where pausing is not supported yet (inside a subgraph).
type GatePauser interface {
	PausesGates() bool
}

// PendingAnswer is an answer `tracker answer` supplies for one paused gate. It
// is held in memory by the resuming process only — never written to disk for
// the run to read back — and applies to the gate with the same NodeID,
// ScopePath and Fingerprint.
type PendingAnswer struct {
	NodeID      string
	ScopePath   []string
	Fingerprint string
	// Nonce is the paused record's nonce the answer was validated against.
	Nonce string
	// Label is the chosen option (choice, labeled freeform, yes_no).
	Label string
	// Text is free text (freeform gates).
	Text string
	// Interview maps GateQuestion.ID to its answer (interview gates).
	Interview map[string]string
	// Cancel cancels an interview gate (the handler maps it to OutcomeFail).
	Cancel bool
}

// PausingInterviewer is the pause-mode interviewer. Every gate call either
// returns the held answer (once, for the matching gate) or pauses the run with
// pipeline.NewPausedGateError carrying exactly what the gate asked. It reads
// the gate's identity from the call's context (GateInfoFrom), so concurrent
// branch gates never cross wires.
type PausingInterviewer struct {
	mu      sync.Mutex
	answers []PendingAnswer
}

// NewPausingInterviewer returns a pause-mode interviewer holding answer (nil
// for a run that has no answer yet).
func NewPausingInterviewer(answer *PendingAnswer) *PausingInterviewer {
	if answer == nil {
		return &PausingInterviewer{}
	}
	return NewPausingInterviewerFor([]PendingAnswer{*answer})
}

// NewPausingInterviewerFor returns a pause-mode interviewer holding one answer
// per waiting gate. Every gate of a paused parallel node must be answered in
// the same resume: the resume re-runs the node, and a gate without an answer
// would pause it again and discard the others.
func NewPausingInterviewerFor(answers []PendingAnswer) *PausingInterviewer {
	return &PausingInterviewer{answers: append([]PendingAnswer(nil), answers...)}
}

// PausesGates reports that this interviewer pauses gates (GatePauser).
func (p *PausingInterviewer) PausesGates() bool { return true }

// Actor classifies answers as human: every answer came from a person running
// `tracker answer`.
func (p *PausingInterviewer) Actor() pipeline.Actor { return pipeline.ActorHuman }

// Ask implements Interviewer. The handler calls the context variants; the
// legacy methods exist for interface satisfaction and carry no gate identity.
func (p *PausingInterviewer) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	return p.AskContext(context.Background(), prompt, choices, defaultChoice)
}

// AskContext implements ChoiceContextInterviewer (choice and yes_no gates).
func (p *PausingInterviewer) AskContext(ctx context.Context, prompt string, choices []string, defaultChoice string) (string, error) {
	info, _ := GateInfoFrom(ctx)
	mode := info.Mode
	if mode != "yes_no" {
		mode = "choice"
	}
	rec := p.record(info, mode, prompt, choices, defaultChoice, nil)
	ans, err := p.take(rec)
	if err != nil {
		return "", err
	}
	return ans.Label, nil
}

// AskFreeform implements FreeformInterviewer.
func (p *PausingInterviewer) AskFreeform(prompt string) (string, error) {
	return p.AskFreeformContext(context.Background(), prompt)
}

// AskFreeformContext implements FreeformContextInterviewer.
func (p *PausingInterviewer) AskFreeformContext(ctx context.Context, prompt string) (string, error) {
	return p.AskFreeformWithLabelsContext(ctx, prompt, nil, "")
}

// AskFreeformWithLabels implements LabeledFreeformInterviewer.
func (p *PausingInterviewer) AskFreeformWithLabels(prompt string, labels []string, defaultLabel string) (string, error) {
	return p.AskFreeformWithLabelsContext(context.Background(), prompt, labels, defaultLabel)
}

// AskFreeformWithLabelsContext implements LabeledFreeformContextInterviewer.
func (p *PausingInterviewer) AskFreeformWithLabelsContext(ctx context.Context, prompt string, labels []string, defaultLabel string) (string, error) {
	info, _ := GateInfoFrom(ctx)
	rec := p.record(info, "freeform", prompt, labels, defaultLabel, nil)
	ans, err := p.take(rec)
	if err != nil {
		return "", err
	}
	if ans.Label != "" {
		return ans.Label, nil
	}
	return ans.Text, nil
}

// AskInterview implements InterviewInterviewer.
func (p *PausingInterviewer) AskInterview(questions []Question, previous *InterviewResult) (*InterviewResult, error) {
	return p.AskInterviewContext(context.Background(), questions, previous)
}

// AskInterviewContext implements InterviewContextInterviewer.
func (p *PausingInterviewer) AskInterviewContext(ctx context.Context, questions []Question, _ *InterviewResult) (*InterviewResult, error) {
	info, _ := GateInfoFrom(ctx)
	rec := p.record(info, "interview", info.Prompt, nil, "", gateQuestionsOf(questions))
	ans, err := p.take(rec)
	if err != nil {
		return nil, err
	}
	if ans.Cancel {
		return &InterviewResult{Canceled: true}, nil
	}
	res := &InterviewResult{Questions: make([]InterviewAnswer, 0, len(questions))}
	for _, q := range rec.Questions {
		res.Questions = append(res.Questions, InterviewAnswer{ID: q.ID, Text: q.Text, Options: q.Options, Answer: ans.Interview[q.ID]})
	}
	return res, nil
}

// record builds the paused-gate record for a gate call.
func (p *PausingInterviewer) record(info GateInfo, mode, prompt string, choices []string, def string, questions []pipeline.GateQuestion) pipeline.PausedGate {
	rec := pipeline.PausedGate{
		NodeID:      info.NodeID,
		ScopePath:   info.ScopePath,
		Mode:        mode,
		Label:       info.Label,
		Prompt:      prompt,
		Choices:     append([]string(nil), choices...),
		Default:     def,
		Questions:   questions,
		BranchIndex: info.BranchIndex,
		Nonce:       newNonce(),
		At:          time.Now().UTC(),
	}
	if len(rec.Choices) == 0 {
		rec.Choices = nil
	}
	rec.Fingerprint = pipeline.GateFingerprint(rec)
	return rec
}

// take returns the held answer when it is for this gate and the question is
// unchanged, consuming it; otherwise it returns the pause for rec. An answer
// for this gate whose question changed is dropped, and the pause says so.
func (p *PausingInterviewer) take(rec pipeline.PausedGate) (*PendingAnswer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := slices.IndexFunc(p.answers, func(a PendingAnswer) bool {
		return a.NodeID == rec.NodeID && slices.Equal(a.ScopePath, rec.ScopePath)
	})
	if i < 0 {
		return nil, pipeline.NewPausedGateError(rec)
	}
	ans := p.answers[i]
	p.answers = slices.Delete(p.answers, i, i+1)
	if ans.Fingerprint != rec.Fingerprint {
		rec.Note = questionChangedNote
		return nil, pipeline.NewPausedGateError(rec)
	}
	return &ans, nil
}

// gateQuestionsOf converts parsed interview questions to the recorded form,
// with the same q<N> IDs the interview result uses.
func gateQuestionsOf(questions []Question) []pipeline.GateQuestion {
	out := make([]pipeline.GateQuestion, 0, len(questions))
	for _, q := range questions {
		out = append(out, pipeline.GateQuestion{
			ID: fmt.Sprintf("q%d", q.Index), Text: q.Text, Context: q.Context,
			Options: q.Options, IsYesNo: q.IsYesNo,
		})
	}
	return out
}

// newNonce returns 16 random bytes as hex.
func newNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error (Go 1.24+)
	return hex.EncodeToString(b)
}

var (
	_ ChoiceContextInterviewer          = (*PausingInterviewer)(nil)
	_ FreeformContextInterviewer        = (*PausingInterviewer)(nil)
	_ LabeledFreeformContextInterviewer = (*PausingInterviewer)(nil)
	_ InterviewContextInterviewer       = (*PausingInterviewer)(nil)
	_ LabeledFreeformInterviewer        = (*PausingInterviewer)(nil)
	_ InterviewInterviewer              = (*PausingInterviewer)(nil)
	_ GatePauser                        = (*PausingInterviewer)(nil)
)
