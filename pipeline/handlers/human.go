// ABOUTME: Human gate handler that pauses pipeline execution for human decision-making.
// ABOUTME: Uses an Interviewer interface to present choices derived from outgoing edge labels.
package handlers

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/tui/render"
)

var errHumanTimeout = fmt.Errorf("human gate timed out waiting for input")

// withTimeout runs fn in a goroutine and returns its result, or errHumanTimeout
// if the duration elapses first. A zero timeout means no timeout.
//
// Note: on timeout, the goroutine running fn is NOT canceled (the Interviewer
// interface has no cancellation mechanism). The goroutine may leak until the
// underlying I/O unblocks. This is an accepted tradeoff to avoid changing the
// Interviewer interface.
func withTimeout(timeout time.Duration, fn func() (string, error)) (string, error) {
	if timeout <= 0 {
		return fn()
	}
	type result struct {
		val string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, e := fn()
		ch <- result{v, e}
	}()
	select {
	case r := <-ch:
		return r.val, r.err
	case <-time.After(timeout):
		return "", errHumanTimeout
	}
}

// withTimeoutOutcome is like withTimeout but for functions returning (Outcome, error).
func withTimeoutOutcome(timeout time.Duration, fn func() (pipeline.Outcome, error)) (pipeline.Outcome, error) {
	if timeout <= 0 {
		return fn()
	}
	type result struct {
		val pipeline.Outcome
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, e := fn()
		ch <- result{v, e}
	}()
	select {
	case r := <-ch:
		return r.val, r.err
	case <-time.After(timeout):
		return pipeline.Outcome{}, errHumanTimeout
	}
}

func parseHumanTimeout(node *pipeline.Node) time.Duration {
	if ts, ok := node.Attrs["timeout"]; ok {
		if d, err := time.ParseDuration(ts); err == nil {
			return d
		}
	}
	return 0
}

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

	c.printChoices(prompt, choices, defaultChoice)

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

	return matchConsoleChoice(input, choices)
}

// printChoices writes the numbered choice list to the writer.
func (c *ConsoleInterviewer) printChoices(prompt string, choices []string, defaultChoice string) {
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
}

// matchConsoleChoice finds a match for input in choices by exact name (case-insensitive)
// or 1-based numeric index. Returns an error if no match is found.
func matchConsoleChoice(input string, choices []string) (string, error) {
	for _, choice := range choices {
		if strings.EqualFold(input, choice) {
			return choice, nil
		}
	}
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
	prevByID := buildPrevAnswerIndex(prev)
	answers := make([]InterviewAnswer, len(questions))
	canceled := false

	for i, q := range questions {
		ans := InterviewAnswer{
			ID:   fmt.Sprintf("q%d", q.Index),
			Text: q.Text,
		}
		fmt.Fprintf(c.Writer, "\nQ%d: %s\n", q.Index, q.Text)

		if err := c.askQuestion(&ans, q, prevByID[ans.ID]); err != nil {
			canceled = true
			answers[i] = ans
			fillRemainingEmpty(answers, questions, i+1)
			break
		}
		answers[i] = ans
	}
	return &InterviewResult{Questions: answers, Canceled: canceled}, nil
}

// buildPrevAnswerIndex builds an ID-keyed lookup of previous interview answers.
func buildPrevAnswerIndex(prev *InterviewResult) map[string]InterviewAnswer {
	index := make(map[string]InterviewAnswer)
	if prev != nil {
		for _, a := range prev.Questions {
			index[a.ID] = a
		}
	}
	return index
}

// askQuestion dispatches to the appropriate question type handler.
func (c *ConsoleInterviewer) askQuestion(ans *InterviewAnswer, q Question, prevAns InterviewAnswer) error {
	if q.IsYesNo {
		// Yes/no takes priority over options to stay consistent with TUI behavior.
		return c.askYesNoQuestion(ans, prevAns)
	}
	if len(q.Options) > 0 {
		return c.askOptionQuestion(ans, q, prevAns)
	}
	return c.askFreeformQuestion(ans, prevAns)
}

// fillRemainingEmpty fills answers[start:] with empty InterviewAnswer structs for
// questions that were not reached due to cancellation.
func fillRemainingEmpty(answers []InterviewAnswer, questions []Question, start int) {
	for j := start; j < len(questions); j++ {
		answers[j] = InterviewAnswer{
			ID:   fmt.Sprintf("q%d", questions[j].Index),
			Text: questions[j].Text,
		}
	}
}

// askYesNoQuestion handles a yes/no question, reading input from the console.
// Returns an error only on I/O failure (treated as cancellation by the caller).
func (c *ConsoleInterviewer) askYesNoQuestion(ans *InterviewAnswer, prevAns InterviewAnswer) error {
	if prevAns.Answer != "" {
		fmt.Fprintf(c.Writer, "Previous: %s\n", prevAns.Answer)
	}
	fmt.Fprintf(c.Writer, "Enter (y/n, blank to skip): ")
	line, err := c.readLine()
	if err != nil {
		return err
	}
	ans.Answer = resolveYesNoInput(strings.TrimSpace(strings.ToLower(line)), prevAns.Answer)
	return nil
}

// resolveYesNoInput maps raw yes/no input to a canonical answer string.
// Returns prevAnswer when input is blank and a previous answer exists.
func resolveYesNoInput(input, prevAnswer string) string {
	switch input {
	case "y", "yes":
		return "yes"
	case "n", "no":
		return "no"
	case "":
		return prevAnswer
	}
	return ""
}

// askOptionQuestion handles a question with a fixed option list, reading input from
// the console. Returns an error only on I/O failure (treated as cancellation).
func (c *ConsoleInterviewer) askOptionQuestion(ans *InterviewAnswer, q Question, prevAns InterviewAnswer) error {
	for j, opt := range q.Options {
		fmt.Fprintf(c.Writer, "  %d) %s\n", j+1, opt)
	}
	fmt.Fprintf(c.Writer, "  %d) Other\n", len(q.Options)+1)

	if prevAns.Answer != "" {
		fmt.Fprintf(c.Writer, "Previous: %s\n", prevAns.Answer)
	}
	fmt.Fprintf(c.Writer, "Enter choice (name or number, blank to skip): ")

	line, err := c.readLine()
	if err != nil {
		return err
	}
	input := strings.TrimSpace(line)
	if input != "" {
		c.resolveOptionInput(ans, q, input)
	} else if prevAns.Answer != "" {
		// Blank input preserves the previous answer on retry.
		ans.Answer = prevAns.Answer
	}
	return nil
}

// resolveOptionInput maps a user-typed string to one of q's options (by name or
// 1-based index). Selecting the "Other" slot (index == len+1) prompts for freeform
// text. Unrecognised input is stored verbatim as a freeform answer.
func (c *ConsoleInterviewer) resolveOptionInput(ans *InterviewAnswer, q Question, input string) {
	// Match by name (case-insensitive)
	for _, opt := range q.Options {
		if strings.EqualFold(input, opt) {
			ans.Answer = opt
			return
		}
	}
	// Match by numeric index
	if c.resolveNumericInput(ans, q, input) {
		return
	}
	// Treat as "Other" freeform
	ans.Answer = input
}

// resolveNumericInput attempts to match input as a 1-based option index.
// Returns true if the input was handled (matched or "Other" selected).
func (c *ConsoleInterviewer) resolveNumericInput(ans *InterviewAnswer, q Question, input string) bool {
	var idx int
	if _, err := fmt.Sscanf(input, "%d", &idx); err != nil {
		return false
	}
	if idx >= 1 && idx <= len(q.Options) {
		ans.Answer = q.Options[idx-1]
		return true
	}
	if idx == len(q.Options)+1 {
		// User selected "Other" by number — prompt for freeform text.
		fmt.Fprintf(c.Writer, "Enter your answer: ")
		otherLine, otherErr := c.readLine()
		if otherErr == nil {
			ans.Answer = strings.TrimSpace(otherLine)
		}
		return true
	}
	return false
}

// askFreeformQuestion handles an open-ended question with no options.
// Returns an error only on I/O failure (treated as cancellation by the caller).
func (c *ConsoleInterviewer) askFreeformQuestion(ans *InterviewAnswer, prevAns InterviewAnswer) error {
	if prevAns.Answer != "" {
		fmt.Fprintf(c.Writer, "Previous: %s\n", prevAns.Answer)
	}
	fmt.Fprintf(c.Writer, "> ")
	line, err := c.readLine()
	if err != nil {
		return err
	}
	text := strings.TrimSpace(line)
	if text != "" {
		ans.Answer = text
	} else if prevAns.Answer != "" {
		// Blank input preserves the previous answer on retry.
		ans.Answer = prevAns.Answer
	}
	return nil
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

	outcome, err := h.dispatchHumanMode(ctx, node, pctx, prompt)

	if errors.Is(err, errHumanTimeout) {
		return h.handleHumanTimeout(node), nil
	}

	return outcome, err
}

// dispatchHumanMode routes to the appropriate human input handler based on the node mode.
func (h *HumanHandler) dispatchHumanMode(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext, prompt string) (pipeline.Outcome, error) {
	switch node.Attrs["mode"] {
	case "interview":
		timeout := parseHumanTimeout(node)
		return withTimeoutOutcome(timeout, func() (pipeline.Outcome, error) {
			return h.executeInterview(ctx, node, pctx)
		})
	case "freeform":
		return h.executeFreeform(node, prompt)
	case "yes_no":
		return h.executeYesNo(node, prompt)
	default:
		return h.executeChoice(node, prompt)
	}
}

// handleHumanTimeout returns the appropriate outcome when a human gate times out.
func (h *HumanHandler) handleHumanTimeout(node *pipeline.Node) pipeline.Outcome {
	action := node.Attrs["timeout_action"]
	if action == "" {
		action = "default"
	}
	if action == "fail" {
		return pipeline.Outcome{Status: pipeline.OutcomeFail, ContextUpdates: map[string]string{
			pipeline.ContextKeyHumanResponse: "timed out",
		}}
	}
	def := node.Attrs["default_choice"]
	if def == "" {
		def = node.Attrs["default"]
	}
	if def == "" {
		return pipeline.Outcome{Status: pipeline.OutcomeFail, ContextUpdates: map[string]string{
			pipeline.ContextKeyHumanResponse: "timed out (no default)",
		}}
	}
	return pipeline.Outcome{
		Status:         pipeline.OutcomeSuccess,
		PreferredLabel: def,
		ContextUpdates: map[string]string{
			pipeline.ContextKeyHumanResponse:            def,
			pipeline.ContextKeyResponsePrefix + node.ID: def,
		},
	}
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

	labels := collectEdgeLabels(h.graph, node.ID)
	defaultLabel := node.Attrs["default"]
	timeout := parseHumanTimeout(node)

	response, err := askFreeformWithTimeout(fi, prompt, labels, defaultLabel, timeout)
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

// collectEdgeLabels returns all non-empty labels from outgoing edges of nodeID.
func collectEdgeLabels(graph *pipeline.Graph, nodeID string) []string {
	if graph == nil {
		return nil
	}
	var labels []string
	for _, e := range graph.OutgoingEdges(nodeID) {
		if e.Label != "" {
			labels = append(labels, e.Label)
		}
	}
	return labels
}

// askFreeformWithTimeout dispatches to the labeled or plain freeform variant with a timeout.
func askFreeformWithTimeout(fi FreeformInterviewer, prompt string, labels []string, defaultLabel string, timeout time.Duration) (string, error) {
	if lfi, ok := fi.(LabeledFreeformInterviewer); ok && len(labels) > 0 {
		return withTimeout(timeout, func() (string, error) {
			return lfi.AskFreeformWithLabels(prompt, labels, defaultLabel)
		})
	}
	return withTimeout(timeout, func() (string, error) {
		return fi.AskFreeform(prompt)
	})
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

	questionsKey, answersKey := resolveInterviewKeys(node)
	agentOutput := resolveAgentOutput(pctx, questionsKey)
	questions := parseInterviewQuestions(agentOutput)

	// 0 questions or malformed → fall back to freeform with prompt
	if len(questions) == 0 {
		return h.executeInterviewFallback(node, pctx, agentOutput, answersKey)
	}

	return h.runInterview(node, pctx, ii, questions, answersKey)
}

// resolveInterviewKeys returns the context keys for questions and answers,
// using node attrs when set and falling back to pipeline constants.
func resolveInterviewKeys(node *pipeline.Node) (questionsKey, answersKey string) {
	questionsKey = node.Attrs["questions_key"]
	if questionsKey == "" {
		questionsKey = pipeline.ContextKeyInterviewQuestions
	}
	answersKey = node.Attrs["answers_key"]
	if answersKey == "" {
		answersKey = pipeline.ContextKeyInterviewAnswers
	}
	return
}

// resolveAgentOutput reads the agent's raw output from the pipeline context,
// preferring the dedicated questions key and falling back to last_response.
func resolveAgentOutput(pctx *pipeline.PipelineContext, questionsKey string) string {
	if v, ok := pctx.Get(questionsKey); ok && v != "" {
		return v
	}
	v, _ := pctx.Get(pipeline.ContextKeyLastResponse)
	return v
}

// parseInterviewQuestions tries structured JSON parsing first, then falls back
// to the markdown heuristic parser. Returns nil if no questions are found.
func parseInterviewQuestions(agentOutput string) []Question {
	questions, jsonErr := ParseStructuredQuestions(agentOutput)
	if jsonErr != nil {
		questions = ParseQuestions(agentOutput)
	}
	return questions
}

// executeInterviewFallback handles the zero-questions case by falling back to
// freeform input and also storing the response under answersKey.
func (h *HumanHandler) executeInterviewFallback(node *pipeline.Node, pctx *pipeline.PipelineContext, agentOutput, answersKey string) (pipeline.Outcome, error) {
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

// runInterview loads any previous answers for pre-fill, presents the interview,
// and returns the outcome with serialized answers stored in answersKey.
func (h *HumanHandler) runInterview(node *pipeline.Node, pctx *pipeline.PipelineContext, ii InterviewInterviewer, questions []Question, answersKey string) (pipeline.Outcome, error) {
	var previous *InterviewResult
	if prevJSON, ok := pctx.Get(answersKey); ok && prevJSON != "" {
		if prev, err := DeserializeInterviewResult(prevJSON); err == nil {
			previous = &prev
		}
	}

	result, err := ii.AskInterview(questions, previous)
	if err != nil {
		return pipeline.Outcome{}, fmt.Errorf("interview failed for node %q: %w", node.ID, err)
	}

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

	timeout := parseHumanTimeout(node)
	selected, err := withTimeout(timeout, func() (string, error) {
		return h.interviewer.Ask(prompt, choices, node.Attrs["default_choice"])
	})
	if err != nil {
		return pipeline.Outcome{}, fmt.Errorf("human gate choice selection failed for node %q: %w", node.ID, err)
	}

	return pipeline.Outcome{Status: pipeline.OutcomeSuccess, PreferredLabel: selected}, nil
}

// executeYesNo handles yes_no mode: presents Yes/No choices and maps them to
// OutcomeSuccess (Yes) or OutcomeFail (No) so pipelines can route with
// ctx.outcome = success / ctx.outcome = fail conditions.
func (h *HumanHandler) executeYesNo(node *pipeline.Node, prompt string) (pipeline.Outcome, error) {
	timeout := parseHumanTimeout(node)
	selected, err := withTimeout(timeout, func() (string, error) {
		return h.interviewer.Ask(prompt, []string{"Yes", "No"}, "")
	})
	if err != nil {
		return pipeline.Outcome{}, fmt.Errorf("human gate yes/no failed for node %q: %w", node.ID, err)
	}

	status := pipeline.OutcomeSuccess
	if selected == "No" {
		status = pipeline.OutcomeFail
	}
	return pipeline.Outcome{Status: status, PreferredLabel: selected}, nil
}
