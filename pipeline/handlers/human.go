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
	"github.com/2389-research/tracker/tui/render"
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

// LabeledFreeformInterviewer extends FreeformInterviewer with label awareness.
// When outgoing edges have labels, the TUI can present them as selectable
// options alongside a freeform textarea for custom input.
type LabeledFreeformInterviewer interface {
	FreeformInterviewer
	AskFreeformWithLabels(prompt string, labels []string, defaultLabel string) (string, error)
}

// InterviewInterviewer extends FreeformInterviewer with structured interview support.
// Used by human gate nodes with mode="interview" to present parsed questions
// as individual form fields with inline options.
type InterviewInterviewer interface {
	FreeformInterviewer
	AskInterview(questions []Question, previousAnswers *InterviewResult) (*InterviewResult, error)
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

// AskInterview auto-approves all questions: picks the first option for select
// questions, "yes" for yes/no questions, and "auto-approved" for open-ended ones.
func (a *AutoApproveFreeformInterviewer) AskInterview(questions []Question, prev *InterviewResult) (*InterviewResult, error) {
	answers := make([]InterviewAnswer, len(questions))
	for i, q := range questions {
		ans := InterviewAnswer{
			ID:   fmt.Sprintf("q%d", q.Index),
			Text: q.Text,
		}
		if q.IsYesNo {
			ans.Answer = "yes"
		} else if len(q.Options) > 0 {
			ans.Answer = q.Options[0]
		} else {
			ans.Answer = "auto-approved"
		}
		answers[i] = ans
	}
	return &InterviewResult{Questions: answers}, nil
}

// Compile-time assertion: AutoApproveFreeformInterviewer implements InterviewInterviewer.
var _ InterviewInterviewer = (*AutoApproveFreeformInterviewer)(nil)

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

	fmt.Fprintf(c.Writer, "\n%s\n", render.PromptPlain(prompt, 76))
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
	fmt.Fprintf(c.Writer, "\n%s\n> ", render.PromptPlain(prompt, 76))

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

// AskInterview presents structured interview questions to the user via the console.
// For each question it prints the question text and, if applicable, numbered options.
// The user can respond by name (case-insensitive) or numeric index. A blank response
// skips the question. Previous answers are shown as a hint when provided.
func (c *ConsoleInterviewer) AskInterview(questions []Question, prev *InterviewResult) (*InterviewResult, error) {
	// Build ID-based lookup for previous answers.
	prevByID := make(map[string]InterviewAnswer)
	if prev != nil {
		for _, a := range prev.Questions {
			prevByID[a.ID] = a
		}
	}

	answers := make([]InterviewAnswer, len(questions))
	canceled := false
	for i, q := range questions {
		ans := InterviewAnswer{
			ID:   fmt.Sprintf("q%d", q.Index),
			Text: q.Text,
		}
		prevAns := prevByID[ans.ID]

		// Print the question
		fmt.Fprintf(c.Writer, "\nQ%d: %s\n", q.Index, q.Text)

		if q.IsYesNo {
			// Yes/no takes priority over options to stay consistent with TUI behavior.
			if prevAns.Answer != "" {
				fmt.Fprintf(c.Writer, "Previous: %s\n", prevAns.Answer)
			}
			fmt.Fprintf(c.Writer, "Enter (y/n, blank to skip): ")
			line, err := c.readLine()
			if err != nil {
				canceled = true
				answers[i] = ans
				for j := i + 1; j < len(questions); j++ {
					answers[j] = InterviewAnswer{
						ID:   fmt.Sprintf("q%d", questions[j].Index),
						Text: questions[j].Text,
					}
				}
				break
			}
			input := strings.TrimSpace(strings.ToLower(line))
			if input == "y" || input == "yes" {
				ans.Answer = "yes"
			} else if input == "n" || input == "no" {
				ans.Answer = "no"
			} else if input == "" && prevAns.Answer != "" {
				ans.Answer = prevAns.Answer
			}
		} else if len(q.Options) > 0 {
			// Print numbered options
			for j, opt := range q.Options {
				fmt.Fprintf(c.Writer, "  %d) %s\n", j+1, opt)
			}
			fmt.Fprintf(c.Writer, "  %d) Other\n", len(q.Options)+1)

			// Pre-fill hint
			if prevAns.Answer != "" {
				fmt.Fprintf(c.Writer, "Previous: %s\n", prevAns.Answer)
			}
			fmt.Fprintf(c.Writer, "Enter choice (name or number, blank to skip): ")

			line, err := c.readLine()
			if err != nil {
				canceled = true
				answers[i] = ans
				// Fill remaining questions with empty answers.
				for j := i + 1; j < len(questions); j++ {
					answers[j] = InterviewAnswer{
						ID:   fmt.Sprintf("q%d", questions[j].Index),
						Text: questions[j].Text,
					}
				}
				break
			}
			input := strings.TrimSpace(line)
			if input != "" {
				// Match by name (case-insensitive) or number
				matched := false
				for _, opt := range q.Options {
					if strings.EqualFold(input, opt) {
						ans.Answer = opt
						matched = true
						break
					}
				}
				if !matched {
					var idx int
					if _, err := fmt.Sscanf(input, "%d", &idx); err == nil && idx >= 1 && idx <= len(q.Options) {
						ans.Answer = q.Options[idx-1]
					} else if _, err := fmt.Sscanf(input, "%d", &idx); err == nil && idx == len(q.Options)+1 {
						// User selected "Other" by number — prompt for freeform text.
						fmt.Fprintf(c.Writer, "Enter your answer: ")
						otherLine, otherErr := c.readLine()
						if otherErr == nil {
							ans.Answer = strings.TrimSpace(otherLine)
						}
					} else {
						// Treat as "Other" freeform
						ans.Answer = input
					}
				}
			} else if prevAns.Answer != "" {
				// Blank input preserves the previous answer on retry.
				ans.Answer = prevAns.Answer
			}
		} else {
			if prevAns.Answer != "" {
				fmt.Fprintf(c.Writer, "Previous: %s\n", prevAns.Answer)
			}
			fmt.Fprintf(c.Writer, "> ")
			line, err := c.readLine()
			if err != nil {
				canceled = true
				answers[i] = ans
				for j := i + 1; j < len(questions); j++ {
					answers[j] = InterviewAnswer{
						ID:   fmt.Sprintf("q%d", questions[j].Index),
						Text: questions[j].Text,
					}
				}
				break
			}
			text := strings.TrimSpace(line)
			if text != "" {
				ans.Answer = text
			} else if prevAns.Answer != "" {
				// Blank input preserves the previous answer on retry.
				ans.Answer = prevAns.Answer
			}
		}

		answers[i] = ans
	}
	return &InterviewResult{Questions: answers, Canceled: canceled}, nil
}

// Compile-time assertion: ConsoleInterviewer implements InterviewInterviewer.
var _ InterviewInterviewer = (*ConsoleInterviewer)(nil)

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
	prompt := h.resolveHumanPrompt(node, pctx)

	if node.Attrs["mode"] == "interview" {
		return h.executeInterview(ctx, node, pctx)
	}
	if node.Attrs["mode"] == "freeform" {
		return h.executeFreeform(node, prompt)
	}
	return h.executeChoice(node, prompt)
}

// resolveHumanPrompt builds the full prompt with variable expansion and last response context.
func (h *HumanHandler) resolveHumanPrompt(node *pipeline.Node, pctx *pipeline.PipelineContext) string {
	prompt := node.Label
	if prompt == "" {
		prompt = fmt.Sprintf("Human gate: %s", node.ID)
	}

	var graphAttrs map[string]string
	if h.graph != nil {
		graphAttrs = h.graph.Attrs
	}
	if expanded, err := pipeline.ExpandVariables(prompt, pctx, nil, graphAttrs, false); err == nil && expanded != "" {
		prompt = expanded
	}

	if lastResp, ok := pctx.Get(pipeline.ContextKeyLastResponse); ok && lastResp != "" {
		prompt = prompt + "\n\n---\n" + lastResp
	}

	return prompt
}

// executeFreeform handles freeform mode: captures open-ended text and optionally routes by label.
func (h *HumanHandler) executeFreeform(node *pipeline.Node, prompt string) (pipeline.Outcome, error) {
	fi, ok := h.interviewer.(FreeformInterviewer)
	if !ok {
		return pipeline.Outcome{}, fmt.Errorf("human gate node %q has mode=freeform but interviewer does not support freeform input", node.ID)
	}

	// Collect edge labels for the labeled variant.
	var labels []string
	if h.graph != nil {
		for _, e := range h.graph.OutgoingEdges(node.ID) {
			if e.Label != "" {
				labels = append(labels, e.Label)
			}
		}
	}
	defaultLabel := node.Attrs["default"]

	// Use labeled variant if available and there are labels.
	var response string
	var err error
	if lfi, ok := fi.(LabeledFreeformInterviewer); ok && len(labels) > 0 {
		response, err = lfi.AskFreeformWithLabels(prompt, labels, defaultLabel)
	} else {
		response, err = fi.AskFreeform(prompt)
	}
	if err != nil {
		return pipeline.Outcome{}, fmt.Errorf("human gate freeform input failed for node %q: %w", node.ID, err)
	}

	outcome := pipeline.Outcome{
		Status: pipeline.OutcomeSuccess,
		ContextUpdates: map[string]string{
			pipeline.ContextKeyHumanResponse:            response,
			pipeline.ContextKeyResponsePrefix + node.ID: response,
		},
	}

	if h.graph != nil {
		outcome.PreferredLabel = matchFreeformLabel(h.graph, node, response)
	}

	return outcome, nil
}

// matchFreeformLabel tries to match freeform response text against outgoing edge labels.
func matchFreeformLabel(graph *pipeline.Graph, node *pipeline.Node, response string) string {
	normalized := strings.ToLower(strings.TrimSpace(response))
	for _, e := range graph.OutgoingEdges(node.ID) {
		if e.Label != "" && strings.ToLower(e.Label) == normalized {
			return e.Label
		}
	}
	if defLabel := node.Attrs["default"]; defLabel != "" {
		if strings.ToLower(defLabel) == normalized {
			return defLabel
		}
	}
	return ""
}

// executeInterview handles interview mode: parses questions from context and presents
// them as structured form fields via an InterviewInterviewer.
func (h *HumanHandler) executeInterview(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	ii, ok := h.interviewer.(InterviewInterviewer)
	if !ok {
		return pipeline.Outcome{}, fmt.Errorf("human gate node %q has mode=interview but interviewer does not support interviews", node.ID)
	}

	// Read config from node attrs (with defaults from pipeline constants)
	questionsKey := node.Attrs["questions_key"]
	if questionsKey == "" {
		questionsKey = pipeline.ContextKeyInterviewQuestions
	}
	answersKey := node.Attrs["answers_key"]
	if answersKey == "" {
		answersKey = pipeline.ContextKeyInterviewAnswers
	}

	// Read upstream agent output from context
	agentOutput, _ := pctx.Get(questionsKey)
	if agentOutput == "" {
		agentOutput, _ = pctx.Get(pipeline.ContextKeyLastResponse)
	}

	// Try structured JSON first, fall back to markdown heuristic parsing.
	questions, jsonErr := ParseStructuredQuestions(agentOutput)
	if jsonErr != nil {
		questions = ParseQuestions(agentOutput)
	}

	// 0 questions or malformed → fall back to freeform with prompt
	if len(questions) == 0 {
		prompt := node.Attrs["prompt"]
		if prompt == "" {
			prompt = node.Label
		}
		if prompt == "" {
			prompt = "No questions were generated. Please provide any input."
		}
		if agentOutput != "" {
			prompt = prompt + "\n\n---\n" + agentOutput
		}
		outcome, err := h.executeFreeform(node, prompt)
		if err != nil {
			return outcome, err
		}
		// Also persist the freeform response under answers_key so downstream
		// nodes that read the interview answers can find it.
		if outcome.ContextUpdates != nil {
			if resp, ok := outcome.ContextUpdates[pipeline.ContextKeyHumanResponse]; ok {
				outcome.ContextUpdates[answersKey] = resp
			}
		}
		return outcome, nil
	}

	// Check for previous answers (retry pre-fill)
	var previous *InterviewResult
	if prevJSON, ok := pctx.Get(answersKey); ok && prevJSON != "" {
		if prev, err := DeserializeInterviewResult(prevJSON); err == nil {
			previous = &prev
		}
	}

	// Present interview
	result, err := ii.AskInterview(questions, previous)
	if err != nil {
		return pipeline.Outcome{}, fmt.Errorf("interview failed for node %q: %w", node.ID, err)
	}

	// Store answers
	jsonStr := SerializeInterviewResult(*result)
	summary := BuildMarkdownSummary(*result)

	status := pipeline.OutcomeSuccess
	if result.Canceled {
		status = pipeline.OutcomeFail
	}

	return pipeline.Outcome{
		Status: status,
		ContextUpdates: map[string]string{
			answersKey:                                  jsonStr,
			pipeline.ContextKeyHumanResponse:            summary,
			pipeline.ContextKeyResponsePrefix + node.ID: summary,
		},
	}, nil
}

// executeChoice handles choice mode: presents outgoing edge labels as options.
func (h *HumanHandler) executeChoice(node *pipeline.Node, prompt string) (pipeline.Outcome, error) {
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

	selected, err := h.interviewer.Ask(prompt, choices, node.Attrs["default_choice"])
	if err != nil {
		return pipeline.Outcome{}, fmt.Errorf("human gate interview failed for node %q: %w", node.ID, err)
	}

	return pipeline.Outcome{Status: pipeline.OutcomeSuccess, PreferredLabel: selected}, nil
}
