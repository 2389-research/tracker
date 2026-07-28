// ABOUTME: ConsoleInterviewer — the stdin/stdout reference implementation of the interviewer family.
// ABOUTME: Supports choice, freeform, and interview modes with name-or-index selection.
package handlers

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/2389-research/tracker/pipeline"
)

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

// Actor returns ActorHuman — a real human at stdin.
func (c *ConsoleInterviewer) Actor() pipeline.Actor { return pipeline.ActorHuman }

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
	fmt.Fprintf(c.Writer, "\n%s\n", PromptPlain(prompt, 76))
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
	fmt.Fprintf(c.Writer, "\n%s\n> ", PromptPlain(prompt, 76))

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
