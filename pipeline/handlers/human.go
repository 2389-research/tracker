// ABOUTME: Human gate handler that pauses pipeline execution for human decision-making.
// ABOUTME: Uses an Interviewer interface to present choices derived from outgoing edge labels.
package handlers

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/2389-research/tracker/pipeline"
)

// Interviewer defines the interface for presenting choices to a human (or automated)
// decision-maker. Implementations control how the prompt and choices are displayed
// and how the response is collected.
type Interviewer interface {
	Ask(prompt string, choices []string, defaultChoice string) (string, error)
}

// FreeformInterviewer extends Interviewer with open-ended text input.
// Used by human gate nodes with mode="freeform" to capture arbitrary user input
// instead of presenting fixed choices.
type FreeformInterviewer interface {
	Interviewer
	AskFreeform(prompt string) (string, error)
}

// AutoApproveInterviewer always returns the default choice, or the first choice
// if no default is specified. Useful for testing and non-interactive pipelines.
type AutoApproveInterviewer struct{}

// Ask returns the default choice if set, otherwise returns the first choice.
// Returns an error if no choices are provided.
func (a *AutoApproveInterviewer) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	if len(choices) == 0 {
		return "", fmt.Errorf("no choices available")
	}
	if defaultChoice != "" {
		return defaultChoice, nil
	}
	return choices[0], nil
}

// AutoApproveFreeformInterviewer returns a canned response for freeform input.
// Useful for testing and non-interactive pipelines.
type AutoApproveFreeformInterviewer struct {
	AutoApproveInterviewer
}

// AskFreeform returns a fixed "auto-approved" string.
func (a *AutoApproveFreeformInterviewer) AskFreeform(prompt string) (string, error) {
	return "auto-approved", nil
}

// CallbackInterviewer delegates question handling to a callback.
type CallbackInterviewer struct {
	AskFunc func(prompt string, choices []string, defaultChoice string) (string, error)
}

func (c *CallbackInterviewer) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	if c == nil || c.AskFunc == nil {
		return "", fmt.Errorf("callback interviewer has no AskFunc")
	}
	return c.AskFunc(prompt, choices, defaultChoice)
}

// QueueInterviewer returns pre-seeded answers in order.
type QueueInterviewer struct {
	Answers []string
}

func (q *QueueInterviewer) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	if len(q.Answers) == 0 {
		return "", fmt.Errorf("queue interviewer has no queued answers")
	}
	answer := q.Answers[0]
	q.Answers = q.Answers[1:]
	return answer, nil
}

// ConsoleInterviewer presents choices to a human via a console (Reader/Writer)
// and collects their response. Supports selection by name or numeric index.
type ConsoleInterviewer struct {
	Reader  io.Reader
	Writer  io.Writer
	scanner *bufio.Scanner
}

// NewConsoleInterviewer creates a ConsoleInterviewer that reads from stdin and
// writes to stdout.
func NewConsoleInterviewer() *ConsoleInterviewer {
	return &ConsoleInterviewer{Reader: os.Stdin, Writer: os.Stdout}
}

// readLine reads a single line from the reader, lazily initializing a shared
// scanner so that buffered stdin data is not lost between calls.
func (c *ConsoleInterviewer) readLine() (string, error) {
	if c.scanner == nil {
		c.scanner = bufio.NewScanner(c.Reader)
	}
	if !c.scanner.Scan() {
		return "", fmt.Errorf("no input received")
	}
	return c.scanner.Text(), nil
}

// Ask displays the prompt and numbered choices, then reads a line of input.
// The user can type a choice name (case-insensitive) or its 1-based index number.
// If the input is empty and a default is set, the default is returned.
func (c *ConsoleInterviewer) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	if len(choices) == 0 {
		return "", fmt.Errorf("no choices available")
	}

	fmt.Fprintf(c.Writer, "\n%s\n", prompt)
	for i, choice := range choices {
		marker := "  "
		if choice == defaultChoice {
			marker = "* "
		}
		fmt.Fprintf(c.Writer, "%s%d) %s\n", marker, i+1, choice)
	}

	if defaultChoice != "" {
		fmt.Fprintf(c.Writer, "Enter choice [%s]: ", defaultChoice)
	} else {
		fmt.Fprintf(c.Writer, "Enter choice: ")
	}

	line, err := c.readLine()
	if err != nil {
		if defaultChoice != "" {
			return defaultChoice, nil
		}
		return "", err
	}

	input := strings.TrimSpace(line)
	if input == "" && defaultChoice != "" {
		return defaultChoice, nil
	}

	// Match by name (case-insensitive)
	for _, choice := range choices {
		if strings.EqualFold(input, choice) {
			return choice, nil
		}
	}

	// Match by numeric index (1-based)
	var idx int
	if _, err := fmt.Sscanf(input, "%d", &idx); err == nil {
		if idx >= 1 && idx <= len(choices) {
			return choices[idx-1], nil
		}
	}

	return "", fmt.Errorf("invalid choice: %q", input)
}

// AskFreeform displays the prompt and reads a line of freeform text input.
// Returns an error if the input is empty.
func (c *ConsoleInterviewer) AskFreeform(prompt string) (string, error) {
	fmt.Fprintf(c.Writer, "\n%s\n> ", prompt)

	line, err := c.readLine()
	if err != nil {
		return "", err
	}

	input := strings.TrimSpace(line)
	if input == "" {
		return "", fmt.Errorf("empty input")
	}

	return input, nil
}

// HumanHandler implements the pipeline.Handler interface for human gate nodes
// (hexagon shape). It collects outgoing edge labels as choices, presents them
// via the configured Interviewer, and returns the selected label as the
// PreferredLabel in the outcome.
type HumanHandler struct {
	interviewer Interviewer
	graph       *pipeline.Graph
}

// NewHumanHandler creates a HumanHandler with the given interviewer and graph.
// The graph is used to look up outgoing edges from the current node to derive choices.
func NewHumanHandler(interviewer Interviewer, graph *pipeline.Graph) *HumanHandler {
	return &HumanHandler{interviewer: interviewer, graph: graph}
}

// Name returns the handler name used for registry lookup.
func (h *HumanHandler) Name() string { return "wait.human" }

// Execute presents choices or collects freeform input via the interviewer.
// When the node has mode="freeform", it captures open-ended text and stores it
// in context as "human_response". Otherwise it presents outgoing edge labels as
// choices. Uses the node's Label as the prompt, falling back to the node ID.
// Respects the "default_choice" node attribute in choice mode.
func (h *HumanHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	prompt := node.Label
	if prompt == "" {
		prompt = fmt.Sprintf("Human gate: %s", node.ID)
	}

	// Surface the previous node's output so the human has context for their
	// decision. This is the complement of InjectPipelineContext which gives
	// LLM nodes the human_response — here we give human gates the LLM output.
	if lastResp, ok := pctx.Get(pipeline.ContextKeyLastResponse); ok && lastResp != "" {
		prompt = prompt + "\n\n---\n" + lastResp
	}

	// Freeform mode: capture open-ended text input.
	if node.Attrs["mode"] == "freeform" {
		fi, ok := h.interviewer.(FreeformInterviewer)
		if !ok {
			return pipeline.Outcome{}, fmt.Errorf("human gate node %q has mode=freeform but interviewer does not support freeform input", node.ID)
		}
		response, err := fi.AskFreeform(prompt)
		if err != nil {
			return pipeline.Outcome{}, fmt.Errorf("human gate freeform input failed for node %q: %w", node.ID, err)
		}
		return pipeline.Outcome{
			Status:         pipeline.OutcomeSuccess,
			ContextUpdates: map[string]string{pipeline.ContextKeyHumanResponse: response},
		}, nil
	}

	// Choice mode: present outgoing edge labels.
	edges := h.graph.OutgoingEdges(node.ID)
	if len(edges) == 0 {
		return pipeline.Outcome{}, fmt.Errorf("human gate node %q has no outgoing edges to derive choices from", node.ID)
	}

	var choices []string
	for _, e := range edges {
		label := e.Label
		if label == "" {
			label = e.To
		}
		choices = append(choices, label)
	}

	defaultChoice := node.Attrs["default_choice"]

	selected, err := h.interviewer.Ask(prompt, choices, defaultChoice)
	if err != nil {
		return pipeline.Outcome{}, fmt.Errorf("human gate interview failed for node %q: %w", node.ID, err)
	}

	return pipeline.Outcome{Status: pipeline.OutcomeSuccess, PreferredLabel: selected}, nil
}
