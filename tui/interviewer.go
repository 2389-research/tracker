// ABOUTME: BubbleteaInterviewer bridges pipeline gate handlers to the TUI.
// ABOUTME: Mode 1 runs inline tea.Programs per gate; Mode 2 delegates via SendFunc to a running TUI.
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/2389-research/tracker/pipeline/handlers"
)

// Compile-time interface assertions.
var _ handlers.Interviewer = (*BubbleteaInterviewer)(nil)
var _ handlers.FreeformInterviewer = (*BubbleteaInterviewer)(nil)
var _ handlers.LabeledFreeformInterviewer = (*BubbleteaInterviewer)(nil)
var _ handlers.InterviewInterviewer = (*BubbleteaInterviewer)(nil)

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

// Ask presents a choice prompt and returns the selected option.
func (b *BubbleteaInterviewer) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	if b.send != nil {
		return b.askMode2Choice(prompt, choices, defaultChoice)
	}
	return b.askMode1Choice(prompt, choices, defaultChoice)
}

// AskFreeform presents a freeform text prompt and returns the user's input.
func (b *BubbleteaInterviewer) AskFreeform(prompt string) (string, error) {
	if b.send != nil {
		return b.askMode2Freeform(prompt)
	}
	return b.askMode1Freeform(prompt)
}

// AskFreeformWithLabels presents labeled options alongside a freeform textarea.
func (b *BubbleteaInterviewer) AskFreeformWithLabels(prompt string, labels []string, defaultLabel string) (string, error) {
	if b.send != nil {
		return b.askMode2FreeformWithLabels(prompt, labels, defaultLabel)
	}
	// Mode 1 fallback: just use regular freeform.
	return b.askMode1Freeform(prompt)
}

// AskInterview presents a multi-field interview form and returns the structured result.
func (b *BubbleteaInterviewer) AskInterview(questions []handlers.Question, prev *handlers.InterviewResult) (*handlers.InterviewResult, error) {
	if b.send != nil {
		return b.askMode2Interview(questions, prev)
	}
	return b.askMode1Interview(questions, prev)
}

// ── Mode 2: delegate to persistent TUI program ──────────────────────────────

func (b *BubbleteaInterviewer) askMode2Choice(prompt string, choices []string, defaultChoice string) (string, error) {
	ch := make(chan string, 1)
	b.send(MsgGateChoice{
		Prompt:  prompt,
		Options: choices,
		ReplyCh: ch,
	})
	reply, ok := <-ch
	if !ok {
		return "", fmt.Errorf("TUI program closed before responding to gate")
	}
	return reply, nil
}

func (b *BubbleteaInterviewer) askMode2Freeform(prompt string) (string, error) {
	ch := make(chan string, 1)
	b.send(MsgGateFreeform{
		Prompt:  prompt,
		ReplyCh: ch,
	})
	reply, ok := <-ch
	if !ok {
		return "", fmt.Errorf("TUI program closed before responding to gate")
	}
	return reply, nil
}

func (b *BubbleteaInterviewer) askMode2FreeformWithLabels(prompt string, labels []string, defaultLabel string) (string, error) {
	ch := make(chan string, 1)
	b.send(MsgGateFreeform{
		Prompt:  prompt,
		Labels:  labels,
		Default: defaultLabel,
		ReplyCh: ch,
	})
	reply, ok := <-ch
	if !ok {
		return "", fmt.Errorf("TUI program closed before responding to gate")
	}
	return reply, nil
}

// ── Mode 1: inline tea.Program per gate ─────────────────────────────────────

// choiceRunner wraps ChoiceContent in a tea.Model for inline Mode 1 programs.
type choiceRunner struct {
	content   *ChoiceContent
	replyCh   chan string
	result    string
	cancelled bool
}

func (r choiceRunner) Init() tea.Cmd { return nil }

func (r choiceRunner) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	r.content.Update(msg)
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
	return r, nil
}

func (r choiceRunner) View() string { return r.content.View() }

func (b *BubbleteaInterviewer) askMode1Choice(prompt string, choices []string, defaultChoice string) (string, error) {
	if len(choices) == 0 {
		return "", fmt.Errorf("no choices available")
	}
	ch := make(chan string, 1)
	content := NewChoiceContent(prompt, choices, ch)
	// Set cursor to default choice if provided.
	if defaultChoice != "" {
		for i, c := range choices {
			if c == defaultChoice {
				content.cursor = i
				break
			}
		}
	}
	runner := choiceRunner{content: content, replyCh: ch}
	p := tea.NewProgram(runner)
	finalModel, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("TUI choice gate failed: %w", err)
	}
	cr := finalModel.(choiceRunner)
	if cr.result == "" && len(choices) > 0 {
		if defaultChoice != "" {
			return defaultChoice, nil
		}
		return choices[0], nil
	}
	return cr.result, nil
}

// freeformRunner wraps FreeformContent in a tea.Model for inline Mode 1 programs.
type freeformRunner struct {
	content   *FreeformContent
	replyCh   chan string
	result    string
	cancelled bool
}

func (r freeformRunner) Init() tea.Cmd { return nil }

func (r freeformRunner) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	r.content.Update(msg)
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
	return r, nil
}

func (r freeformRunner) View() string { return r.content.View() }

func (b *BubbleteaInterviewer) askMode1Freeform(prompt string) (string, error) {
	ch := make(chan string, 1)
	content := NewFreeformContent(prompt, ch)
	runner := freeformRunner{content: content, replyCh: ch}
	p := tea.NewProgram(runner)
	finalModel, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("TUI freeform gate failed: %w", err)
	}
	fr := finalModel.(freeformRunner)
	if fr.cancelled {
		return "", fmt.Errorf("gate cancelled by user")
	}
	return fr.result, nil
}

// ── Interview: Mode 2 and Mode 1 ────────────────────────────────────────────

func (b *BubbleteaInterviewer) askMode2Interview(questions []handlers.Question, prev *handlers.InterviewResult) (*handlers.InterviewResult, error) {
	ch := make(chan string, 1)
	b.send(MsgGateInterview{Questions: questions, Previous: prev, ReplyCh: ch})
	reply, ok := <-ch
	if !ok {
		return &handlers.InterviewResult{Canceled: true}, nil
	}
	result, err := handlers.DeserializeInterviewResult(reply)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize interview reply: %w", err)
	}
	return &result, nil
}

// interviewRunner wraps InterviewContent in a tea.Model for inline Mode 1 programs.
type interviewRunner struct {
	content   *InterviewContent
	replyCh   chan string
	result    string
	cancelled bool
}

func (r interviewRunner) Init() tea.Cmd { return nil }

func (r interviewRunner) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

func (r interviewRunner) View() string { return r.content.View() }

func (b *BubbleteaInterviewer) askMode1Interview(questions []handlers.Question, prev *handlers.InterviewResult) (*handlers.InterviewResult, error) {
	ch := make(chan string, 1)
	content := NewInterviewContent(questions, prev, ch, 80, 24)
	runner := interviewRunner{content: content, replyCh: ch}
	p := tea.NewProgram(runner)
	finalModel, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("TUI interview failed: %w", err)
	}
	ir := finalModel.(interviewRunner)
	if ir.cancelled || ir.result == "" {
		return &handlers.InterviewResult{Canceled: true}, nil
	}
	result, err := handlers.DeserializeInterviewResult(ir.result)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize interview result: %w", err)
	}
	return &result, nil
}
