// ABOUTME: BubbleteaInterviewer implements the handlers.Interviewer and handlers.FreeformInterviewer
// ABOUTME: interfaces using Bubbletea TUI components. Mode 1 (default) runs inline tea.Programs
// ABOUTME: per gate. Mode 2 (dashboard) delegates to a running TUI program via channels.
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/2389-research/tracker/pipeline/handlers"
	"github.com/2389-research/tracker/tui/components"
	"github.com/2389-research/tracker/tui/dashboard"
)

// ─── Compile-time interface assertions ───────────────────────────────────────

var _ handlers.Interviewer = (*BubbleteaInterviewer)(nil)
var _ handlers.FreeformInterviewer = (*BubbleteaInterviewer)(nil)

// BubbleteaInterviewer implements handlers.Interviewer and handlers.FreeformInterviewer.
// In mode 1 (tuiProgram == nil), each gate spins up a short-lived inline tea.Program.
// In mode 2 (tuiProgram != nil), gates delegate via the program's Send channel and
// block until a response is received through the reply channel.
type BubbleteaInterviewer struct {
	tuiProgram *tea.Program // nil in mode 1, non-nil in mode 2
	replyCh    chan string  // used in mode 2 to receive answers from the TUI
}

// NewBubbleteaInterviewer creates a mode-1 BubbleteaInterviewer (inline tea.Programs).
// This is the default used by cmd/tracker/main.go.
func NewBubbleteaInterviewer() *BubbleteaInterviewer {
	return &BubbleteaInterviewer{}
}

// NewBubbleteaInterviewerMode2 creates a mode-2 BubbleteaInterviewer that delegates
// to the provided running tea.Program. The program must handle GateRequestMsg and
// send responses via the returned channel.
func NewBubbleteaInterviewerMode2(program *tea.Program) (*BubbleteaInterviewer, chan string) {
	ch := make(chan string, 1)
	return &BubbleteaInterviewer{tuiProgram: program, replyCh: ch}, ch
}

// Ask presents a choice prompt with the given options and returns the user's selection.
// In mode 1 it runs an inline tea.Program. In mode 2 it delegates via the TUI program.
func (b *BubbleteaInterviewer) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	if b.tuiProgram != nil {
		return b.askMode2Choice(prompt, choices, defaultChoice)
	}
	return b.askInlineChoice(prompt, choices, defaultChoice)
}

// AskFreeform presents a freeform text prompt and returns the user's input.
// In mode 1 it runs an inline tea.Program. In mode 2 it delegates via the TUI program.
func (b *BubbleteaInterviewer) AskFreeform(prompt string) (string, error) {
	if b.tuiProgram != nil {
		return b.askMode2Freeform(prompt)
	}
	return b.askInlineFreeform(prompt)
}

// ─── Mode 1: inline tea.Program per gate ────────────────────────────────────

// choiceRunner is a wrapper tea.Model that owns a ChoiceModel and emits tea.Quit
// when the user makes a selection or cancels. This bridges the message-based
// component design to the blocking tea.Program.Run() API used in mode 1.
type choiceRunner struct {
	inner     components.ChoiceModel
	result    string
	cancelled bool
}

func (r choiceRunner) Init() tea.Cmd { return r.inner.Init() }

func (r choiceRunner) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case components.ChoiceDoneMsg:
		dm := msg.(components.ChoiceDoneMsg)
		r.result = dm.Value
		return r, tea.Quit
	case components.ChoiceCancelMsg:
		r.cancelled = true
		return r, tea.Quit
	}
	inner, cmd := r.inner.Update(msg)
	r.inner = inner.(components.ChoiceModel)
	// Forward any commands; also fan-out sub-messages from the inner model.
	return r, cmd
}

func (r choiceRunner) View() string { return r.inner.View() }

// freeformRunner is a wrapper tea.Model that owns a FreeformModel and emits
// tea.Quit when the user submits or cancels.
type freeformRunner struct {
	inner     components.FreeformModel
	result    string
	cancelled bool
}

func (r freeformRunner) Init() tea.Cmd { return r.inner.Init() }

func (r freeformRunner) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case components.FreeformDoneMsg:
		dm := msg.(components.FreeformDoneMsg)
		r.result = dm.Value
		return r, tea.Quit
	case components.FreeformCancelMsg:
		r.cancelled = true
		return r, tea.Quit
	}
	inner, cmd := r.inner.Update(msg)
	r.inner = inner.(components.FreeformModel)
	return r, cmd
}

func (r freeformRunner) View() string { return r.inner.View() }

func (b *BubbleteaInterviewer) askInlineChoice(prompt string, choices []string, defaultChoice string) (string, error) {
	if len(choices) == 0 {
		return "", fmt.Errorf("no choices available")
	}
	runner := choiceRunner{inner: components.NewChoiceModel(prompt, choices, defaultChoice)}
	p := tea.NewProgram(runner)
	finalModel, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("TUI choice gate failed: %w", err)
	}
	cr := finalModel.(choiceRunner)
	if cr.cancelled {
		return "", fmt.Errorf("human gate cancelled by user")
	}
	if cr.result == "" && len(choices) > 0 {
		// Fallback: return default or first choice if result somehow empty
		if defaultChoice != "" {
			return defaultChoice, nil
		}
		return choices[0], nil
	}
	return cr.result, nil
}

func (b *BubbleteaInterviewer) askInlineFreeform(prompt string) (string, error) {
	runner := freeformRunner{inner: components.NewFreeformModel(prompt)}
	p := tea.NewProgram(runner)
	finalModel, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("TUI freeform gate failed: %w", err)
	}
	fr := finalModel.(freeformRunner)
	if fr.cancelled {
		return "", fmt.Errorf("human gate cancelled by user")
	}
	return fr.result, nil
}

// ─── Mode 2: delegate to persistent TUI program ──────────────────────────────

func (b *BubbleteaInterviewer) askMode2Choice(prompt string, choices []string, defaultChoice string) (string, error) {
	ch := make(chan string, 1)
	b.tuiProgram.Send(dashboard.GateChoiceMsg{
		Prompt:        prompt,
		Choices:       choices,
		DefaultChoice: defaultChoice,
		ReplyCh:       ch,
	})
	reply, ok := <-ch
	if !ok {
		return "", fmt.Errorf("TUI program closed before responding to gate")
	}
	return reply, nil
}

func (b *BubbleteaInterviewer) askMode2Freeform(prompt string) (string, error) {
	ch := make(chan string, 1)
	b.tuiProgram.Send(dashboard.GateFreeformMsg{
		Prompt:  prompt,
		ReplyCh: ch,
	})
	reply, ok := <-ch
	if !ok {
		return "", fmt.Errorf("TUI program closed before responding to gate")
	}
	return reply, nil
}
