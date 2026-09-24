// ABOUTME: BubbleteaInterviewer bridges pipeline gate handlers to the TUI.
// ABOUTME: Mode 1 runs inline tea.Programs per gate; Mode 2 delegates via SendFunc to a running TUI.
package tui

import (
	"context"
	"fmt"
	"os"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
)

// Compile-time interface assertions, including the context-aware gate variants
// (#599) so a per-gate timeout unblocks only that gate in Mode 2.
var _ handlers.Interviewer = (*BubbleteaInterviewer)(nil)
var _ handlers.FreeformInterviewer = (*BubbleteaInterviewer)(nil)
var _ handlers.LabeledFreeformInterviewer = (*BubbleteaInterviewer)(nil)
var _ handlers.InterviewInterviewer = (*BubbleteaInterviewer)(nil)
var _ handlers.ChoiceContextInterviewer = (*BubbleteaInterviewer)(nil)
var _ handlers.FreeformContextInterviewer = (*BubbleteaInterviewer)(nil)
var _ handlers.LabeledFreeformContextInterviewer = (*BubbleteaInterviewer)(nil)
var _ handlers.InterviewContextInterviewer = (*BubbleteaInterviewer)(nil)

// SendFunc is a function that sends a Bubbletea message to a running program.
// In Mode 2, this is typically tea.Program.Send.
type SendFunc func(msg tea.Msg)

// BubbleteaInterviewer implements handlers.Interviewer and handlers.FreeformInterviewer.
// In Mode 1 (send == nil), each gate spins up a short-lived inline tea.Program.
// In Mode 2 (send != nil), gates delegate via the send function and block until
// a response is received through the reply channel.
type BubbleteaInterviewer struct {
	send SendFunc
}

// NewBubbleteaInterviewer creates a Mode 2 BubbleteaInterviewer that delegates
// gate prompts to a running TUI program via the provided send function.
func NewBubbleteaInterviewer(send SendFunc) *BubbleteaInterviewer {
	return &BubbleteaInterviewer{send: send}
}

// NewMode1Interviewer creates a Mode 1 BubbleteaInterviewer that runs inline
// tea.Programs for each gate prompt. No running TUI program required.
func NewMode1Interviewer() *BubbleteaInterviewer {
	return &BubbleteaInterviewer{}
}

// Actor returns ActorHuman — gate response came from a real human at the TUI.
func (b *BubbleteaInterviewer) Actor() pipeline.Actor { return pipeline.ActorHuman }

// Ask presents a choice prompt and returns the selected option.
func (b *BubbleteaInterviewer) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	return b.AskContext(context.Background(), prompt, choices, defaultChoice)
}

// AskFreeform presents a freeform text prompt and returns the user's input.
func (b *BubbleteaInterviewer) AskFreeform(prompt string) (string, error) {
	return b.AskFreeformContext(context.Background(), prompt)
}

// AskFreeformWithLabels presents labeled options alongside a freeform textarea.
func (b *BubbleteaInterviewer) AskFreeformWithLabels(prompt string, labels []string, defaultLabel string) (string, error) {
	return b.AskFreeformWithLabelsContext(context.Background(), prompt, labels, defaultLabel)
}

// AskInterview presents a multi-field interview form and returns the structured result.
func (b *BubbleteaInterviewer) AskInterview(questions []handlers.Question, prev *handlers.InterviewResult) (*handlers.InterviewResult, error) {
	return b.AskInterviewContext(context.Background(), questions, prev)
}

// ── Context-aware gate variants (#599) ──────────────────────────────────────
//
// These carry a per-gate context so the human handler can cancel a single
// timed-out gate without run-wide teardown. In Mode 2 (a running TUI) the wait
// returns as soon as ctx is canceled, unblocking only this gate. Mode 1 spins an
// inline tea.Program per gate and cannot be interrupted by ctx, so it ignores
// ctx. The plain methods call these with context.Background().

// waitReply blocks on a Mode 2 reply channel, returning ctx.Err() when the
// per-gate context is canceled before the running TUI answers.
func waitReply(ctx context.Context, ch <-chan string) (string, error) {
	select {
	case reply, ok := <-ch:
		if !ok {
			return "", fmt.Errorf("TUI program closed before responding to gate")
		}
		return reply, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// AskContext is the context-aware Ask.
func (b *BubbleteaInterviewer) AskContext(ctx context.Context, prompt string, choices []string, defaultChoice string) (string, error) {
	if b.send == nil {
		return b.askMode1Choice(prompt, choices, defaultChoice)
	}
	ch := make(chan string, 1)
	b.send(MsgGateChoice{Prompt: prompt, Options: choices, ReplyCh: ch})
	return waitReply(ctx, ch)
}

// AskFreeformContext is the context-aware AskFreeform.
func (b *BubbleteaInterviewer) AskFreeformContext(ctx context.Context, prompt string) (string, error) {
	if b.send == nil {
		return b.askMode1Freeform(prompt)
	}
	ch := make(chan string, 1)
	b.send(MsgGateFreeform{Prompt: prompt, ReplyCh: ch})
	return waitReply(ctx, ch)
}

// AskFreeformWithLabelsContext is the context-aware AskFreeformWithLabels.
func (b *BubbleteaInterviewer) AskFreeformWithLabelsContext(ctx context.Context, prompt string, labels []string, defaultLabel string) (string, error) {
	if b.send == nil {
		// Mode 1 fallback: just use regular freeform.
		return b.askMode1Freeform(prompt)
	}
	ch := make(chan string, 1)
	b.send(MsgGateFreeform{Prompt: prompt, Labels: labels, Default: defaultLabel, ReplyCh: ch})
	return waitReply(ctx, ch)
}

// AskInterviewContext is the context-aware AskInterview. A canceled context
// yields a Canceled result, matching a TUI-closed interview.
func (b *BubbleteaInterviewer) AskInterviewContext(ctx context.Context, questions []handlers.Question, prev *handlers.InterviewResult) (*handlers.InterviewResult, error) {
	if b.send == nil {
		return b.askMode1Interview(questions, prev)
	}
	ch := make(chan string, 1)
	b.send(MsgGateInterview{Questions: questions, Previous: prev, ReplyCh: ch})
	select {
	case reply, ok := <-ch:
		if !ok {
			return &handlers.InterviewResult{Canceled: true}, nil
		}
		result, err := handlers.DeserializeInterviewResult(reply)
		if err != nil {
			return nil, fmt.Errorf("failed to deserialize interview reply: %w", err)
		}
		return &result, nil
	case <-ctx.Done():
		return &handlers.InterviewResult{Canceled: true}, nil
	}
}

// ── Mode 1: inline tea.Program per gate ─────────────────────────────────────

// modalRunner wraps a modal's content (ChoiceContent, FreeformContent or
// InterviewContent) in a tea.Model for inline Mode 1 programs.
type modalRunner struct {
	content   ModalContent
	replyCh   chan string
	result    string
	cancelled bool
}

func (r modalRunner) Init() tea.Cmd { return nil }

func (r modalRunner) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := r.content.Update(msg)
	select {
	case val, ok := <-r.replyCh:
		if !ok {
			r.cancelled = true
			return r, tea.Quit
		}
		r.result = val
		return r, tea.Quit
	default:
	}
	return r, cmd
}

func (r modalRunner) View() string { return r.content.View() }

// runModal runs content in an inline tea.Program until it answers or closes
// ch, and returns the runner's final state.
func runModal(content ModalContent, ch chan string) (modalRunner, error) {
	finalModel, err := tea.NewProgram(modalRunner{content: content, replyCh: ch}).Run()
	if err != nil {
		return modalRunner{}, err
	}
	return finalModel.(modalRunner), nil
}

func (b *BubbleteaInterviewer) askMode1Choice(prompt string, choices []string, defaultChoice string) (string, error) {
	if len(choices) == 0 {
		return "", fmt.Errorf("no choices available")
	}
	ch := make(chan string, 1)
	content := NewChoiceContent(prompt, choices, ch)
	setDefaultCursor(content, choices, defaultChoice)
	cr, err := runModal(content, ch)
	if err != nil {
		return "", fmt.Errorf("TUI choice gate failed: %w", err)
	}
	return resolveChoiceResult(cr.result, choices, defaultChoice), nil
}

// setDefaultCursor positions the cursor on the default choice.
func setDefaultCursor(content *ChoiceContent, choices []string, defaultChoice string) {
	if defaultChoice == "" {
		return
	}
	if i := slices.Index(choices, defaultChoice); i >= 0 {
		content.cursor = i
	}
}

// resolveChoiceResult returns the selected choice or the appropriate fallback.
func resolveChoiceResult(result string, choices []string, defaultChoice string) string {
	if result != "" {
		return result
	}
	if defaultChoice != "" {
		return defaultChoice
	}
	if len(choices) > 0 {
		return choices[0]
	}
	return result
}

func (b *BubbleteaInterviewer) askMode1Freeform(prompt string) (string, error) {
	ch := make(chan string, 1)
	fr, err := runModal(NewFreeformContent(prompt, ch), ch)
	if err != nil {
		return "", fmt.Errorf("TUI freeform gate failed: %w", err)
	}
	if fr.cancelled {
		return "", fmt.Errorf("gate cancelled by user")
	}
	return fr.result, nil
}

// ── Mode 1 interview ────────────────────────────────────────────────────────

func (b *BubbleteaInterviewer) askMode1Interview(questions []handlers.Question, prev *handlers.InterviewResult) (*handlers.InterviewResult, error) {
	ch := make(chan string, 1)
	width, height := 80, 24
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		width, height = w, h
	}
	ir, err := runModal(NewInterviewContent(questions, prev, ch, width, height), ch)
	if err != nil {
		return nil, fmt.Errorf("TUI interview failed: %w", err)
	}
	if ir.cancelled || ir.result == "" {
		return &handlers.InterviewResult{Canceled: true}, nil
	}
	result, err := handlers.DeserializeInterviewResult(ir.result)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize interview result: %w", err)
	}
	return &result, nil
}
