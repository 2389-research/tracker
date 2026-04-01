// ABOUTME: AutopilotTUIInterviewer bridges autopilot decisions to the TUI modal.
// ABOUTME: Gets the LLM decision, shows it briefly in the gate modal, then auto-closes.
package tui

import (
	"fmt"
	"os"
	"time"

	"github.com/2389-research/tracker/pipeline/handlers"
)

// Compile-time interface assertions.
var _ handlers.Interviewer = (*AutopilotTUIInterviewer)(nil)
var _ handlers.FreeformInterviewer = (*AutopilotTUIInterviewer)(nil)
var _ handlers.LabeledFreeformInterviewer = (*AutopilotTUIInterviewer)(nil)
var _ handlers.InterviewInterviewer = (*AutopilotTUIInterviewer)(nil)

// MsgGateAutopilot tells the TUI to show the autopilot decision in the modal
// for a brief moment, then auto-close by sending the reply.
type MsgGateAutopilot struct {
	NodeID    string
	Prompt    string
	Decision  string
	Reasoning string
	Labels    []string
	Default   string
	ReplyCh   chan<- string
}

// AutopilotTUIInterviewer wraps an AutopilotInterviewer and routes its
// decisions through the TUI modal for visual feedback before auto-replying.
type AutopilotTUIInterviewer struct {
	autopilot handlers.LabeledFreeformInterviewer
	send      SendFunc
}

// NewAutopilotTUIInterviewer creates an interviewer that shows autopilot
// decisions in the TUI modal before auto-closing.
func NewAutopilotTUIInterviewer(autopilot handlers.LabeledFreeformInterviewer, send SendFunc) *AutopilotTUIInterviewer {
	return &AutopilotTUIInterviewer{autopilot: autopilot, send: send}
}

func (a *AutopilotTUIInterviewer) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	decision, err := a.autopilot.Ask(prompt, choices, defaultChoice)
	if err != nil {
		return "", err
	}
	a.flashDecision(prompt, decision, "", choices, defaultChoice)
	return decision, nil
}

func (a *AutopilotTUIInterviewer) AskFreeform(prompt string) (string, error) {
	decision, err := a.autopilot.AskFreeform(prompt)
	if err != nil {
		return "", err
	}
	a.flashDecision(prompt, decision, "", nil, "")
	return decision, nil
}

func (a *AutopilotTUIInterviewer) AskFreeformWithLabels(prompt string, labels []string, defaultLabel string) (string, error) {
	decision, err := a.autopilot.AskFreeformWithLabels(prompt, labels, defaultLabel)
	if err != nil {
		return "", err
	}
	a.flashDecision(prompt, decision, "", labels, defaultLabel)
	return decision, nil
}

// AskInterview delegates interview questions to the inner autopilot (if it supports
// interviews), then flashes a brief summary in the TUI.
func (a *AutopilotTUIInterviewer) AskInterview(questions []handlers.Question, prev *handlers.InterviewResult) (*handlers.InterviewResult, error) {
	ii, ok := a.autopilot.(handlers.InterviewInterviewer)
	if !ok {
		return nil, fmt.Errorf("inner autopilot does not support interviews")
	}
	result, err := ii.AskInterview(questions, prev)
	if err != nil {
		return nil, err
	}
	summary := fmt.Sprintf("Autopilot answered %d questions", len(result.Questions))
	a.flashDecision("", summary, "", nil, "")
	return result, nil
}

// flashDecision sends the decision to the TUI for brief display, then
// auto-closes after a short delay. The pipeline handler already has the
// decision — this is purely visual feedback.
//
// Channel safety: ch is buffered(1), so the non-blocking send (select with
// default) in the goroutine is safe — the main goroutine is the only reader
// and blocks on <-ch. When the main goroutine receives from ch (either from
// the timer goroutine or a user action), it calls close(done) to signal the
// goroutine to exit. The goroutine's select on <-done ensures it never
// outlives the caller.
func (a *AutopilotTUIInterviewer) flashDecision(prompt, decision, reasoning string, labels []string, defaultLabel string) {
	ch := make(chan string, 1)
	done := make(chan struct{})
	a.send(MsgGateAutopilot{
		Prompt:    prompt,
		Decision:  decision,
		Reasoning: reasoning,
		Labels:    labels,
		Default:   defaultLabel,
		ReplyCh:   ch,
	})
	// Give the TUI time to render the decision, then auto-close.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "autopilot flash goroutine recovered: %v\n", r)
			}
		}()
		select {
		case <-time.After(2 * time.Second):
			select {
			case ch <- decision:
			default:
			}
		case <-done:
			// Caller unblocked (user pressed Enter or Ctrl+C) — exit immediately.
			return
		}
	}()
	// Wait for the reply (either from timer or user pressing Enter).
	<-ch
	close(done) // Signal the goroutine to exit.
	// Dismiss the modal.
	a.send(MsgModalDismiss{})
	// Small gap so the dismiss renders before the next node starts.
	time.Sleep(100 * time.Millisecond)
}

// DecisionString formats the autopilot decision for display.
func DecisionString(decision string) string {
	return fmt.Sprintf("Autopilot chose: %s", decision)
}
