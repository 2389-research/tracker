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
	Reader io.Reader
	Writer io.Writer
}

// NewConsoleInterviewer creates a ConsoleInterviewer that reads from stdin and
// writes to stdout.
func NewConsoleInterviewer() *ConsoleInterviewer {
	return &ConsoleInterviewer{Reader: os.Stdin, Writer: os.Stdout}
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

	scanner := bufio.NewScanner(c.Reader)
	if !scanner.Scan() {
		if defaultChoice != "" {
			return defaultChoice, nil
		}
		return "", fmt.Errorf("no input received")
	}

	input := strings.TrimSpace(scanner.Text())
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

// Execute presents the outgoing edge labels as choices via the interviewer and
// returns the selected label. Uses the node's Label as the prompt, falling back
// to the node ID. Respects the "default_choice" node attribute.
func (h *HumanHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
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

	prompt := node.Label
	if prompt == "" {
		prompt = fmt.Sprintf("Human gate: %s", node.ID)
	}

	defaultChoice := node.Attrs["default_choice"]

	selected, err := h.interviewer.Ask(prompt, choices, defaultChoice)
	if err != nil {
		return pipeline.Outcome{}, fmt.Errorf("human gate interview failed for node %q: %w", node.ID, err)
	}

	return pipeline.Outcome{Status: pipeline.OutcomeSuccess, PreferredLabel: selected}, nil
}
