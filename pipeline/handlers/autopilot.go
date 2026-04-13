// ABOUTME: LLM-backed autopilot interviewer that replaces human gates with automated decisions.
// ABOUTME: Four personas (lax/mid/hard/mentor) encode different risk tolerances for unattended runs.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/2389-research/tracker/llm"
)

// Persona defines an autopilot decision-making style.
type Persona string

const (
	PersonaLax    Persona = "lax"
	PersonaMid    Persona = "mid"
	PersonaHard   Persona = "hard"
	PersonaMentor Persona = "mentor"
)

// ValidPersonas returns all valid persona names.
func ValidPersonas() []string {
	return []string{string(PersonaLax), string(PersonaMid), string(PersonaHard), string(PersonaMentor)}
}

// ParsePersona validates and returns a Persona, defaulting to mid.
func ParsePersona(s string) (Persona, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "mid":
		return PersonaMid, nil
	case "lax":
		return PersonaLax, nil
	case "hard":
		return PersonaHard, nil
	case "mentor":
		return PersonaMentor, nil
	default:
		return "", fmt.Errorf("unknown autopilot persona %q (valid: %s)", s, strings.Join(ValidPersonas(), ", "))
	}
}

var personaPrompts = map[Persona]string{
	PersonaLax: `You are an automated pipeline decision-maker with a LAX disposition.
Your bias: KEEP MOVING. Approve plans, mark milestones done, accept review findings.
Only reject or push back if something is completely broken or nonsensical.
Prefer "approve", "mark done", "accept" over "retry", "adjust", "reject".
When in doubt, choose forward progress.`,

	PersonaMid: `You are an automated pipeline decision-maker with BALANCED judgment.
Decide like a competent senior engineer would. Approve solid work, push back on
obvious gaps or incomplete implementations. Retry once if a milestone is close
but not passing. Accept reviews unless there are clearly blocking issues.
Use your best judgment — neither rubber-stamp nor nitpick.`,

	PersonaHard: `You are an automated pipeline decision-maker with a HIGH quality bar.
Scrutinize everything. Reject plans that skip edge cases or lack sufficient milestones.
Retry milestones that don't fully pass verification. Demand review fixes before shipping.
Only approve when the work genuinely meets the criteria. Prefer "retry", "adjust" over
"approve", "accept" when there are any quality concerns.`,

	PersonaMentor: `You are an automated pipeline decision-maker who acts as a MENTOR.
Your bias: approve forward progress, but provide detailed constructive feedback.
Always choose the option that continues the pipeline (approve, mark done, accept),
but write thorough notes about what could be improved, what risks exist, and what
a human reviewer should pay attention to. Your reasoning should be 3-5 sentences
of actionable, specific feedback — not generic praise.`,
}

// autopilotDecision is the structured response from the LLM judge.
type autopilotDecision struct {
	Choice    string `json:"choice"`
	Reasoning string `json:"reasoning"`
}

// Compile-time assertions: AutopilotInterviewer implements both LabeledFreeformInterviewer
// and InterviewInterviewer.
var _ LabeledFreeformInterviewer = (*AutopilotInterviewer)(nil)
var _ InterviewInterviewer = (*AutopilotInterviewer)(nil)

// AutopilotInterviewer implements LabeledFreeformInterviewer using an LLM
// to make gate decisions instead of a human.
type AutopilotInterviewer struct {
	client  *llm.Client
	persona Persona
	model   string // override model; empty = use default
}

// AutopilotOption configures an AutopilotInterviewer.
type AutopilotOption func(*AutopilotInterviewer)

// WithAutopilotModel overrides the model used for gate decisions.
func WithAutopilotModel(model string) AutopilotOption {
	return func(a *AutopilotInterviewer) {
		a.model = model
	}
}

// NewAutopilotInterviewer creates an LLM-backed interviewer with the given persona.
func NewAutopilotInterviewer(client *llm.Client, persona Persona, opts ...AutopilotOption) *AutopilotInterviewer {
	ai := &AutopilotInterviewer{
		client:  client,
		persona: persona,
	}
	for _, opt := range opts {
		opt(ai)
	}
	return ai
}

// Ask handles choice-mode gates by selecting from the given options.
func (a *AutopilotInterviewer) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	return a.decide(prompt, choices, defaultChoice)
}

// AskFreeform handles pure freeform gates by generating a text response.
// Provider errors hard-fail per CLAUDE.md — never silently swallow errors.
func (a *AutopilotInterviewer) AskFreeform(prompt string) (string, error) {
	decision, err := a.callLLM(prompt, nil, "")
	if err != nil {
		return "", fmt.Errorf("autopilot freeform gate failed: %w", err)
	}
	if decision.Reasoning != "" {
		return decision.Reasoning, nil
	}
	return decision.Choice, nil
}

// AskFreeformWithLabels handles hybrid gates with labeled edges.
func (a *AutopilotInterviewer) AskFreeformWithLabels(prompt string, labels []string, defaultLabel string) (string, error) {
	return a.decide(prompt, labels, defaultLabel)
}

// decide is the core decision-making logic shared by Ask and AskFreeformWithLabels.
func (a *AutopilotInterviewer) decide(prompt string, options []string, defaultOption string) (string, error) {
	decision, err := a.callLLM(prompt, options, defaultOption)
	if err != nil {
		return "", fmt.Errorf("autopilot gate decision failed: %w", err)
	}

	// Match the choice against available options (case-insensitive)
	choice := matchChoice(decision.Choice, options)
	if choice == "" {
		// Unmatchable choice is a parse issue, not a provider error — fall back.
		log.Printf("WARNING: autopilot chose %q which doesn't match any option, using default", decision.Choice)
		return a.fallback(options, defaultOption), nil
	}

	return choice, nil
}

// callLLM sends the gate context to the LLM and parses the structured response.
// Retries once on failure.
func (a *AutopilotInterviewer) callLLM(prompt string, options []string, defaultOption string) (*autopilotDecision, error) {
	systemPrompt := personaPrompts[a.persona]
	userPrompt := buildUserPrompt(prompt, options, defaultOption)

	req := &llm.Request{
		Model: a.resolveModel(),
		Messages: []llm.Message{
			llm.SystemMessage(systemPrompt),
			llm.UserMessage(userPrompt),
		},
		MaxTokens: intPtr(1024),
	}

	// Set low temperature for consistent decisions
	temp := 0.1
	req.Temperature = &temp

	// Provider errors (quota, auth, model not found) must hard-fail per CLAUDE.md.
	// The infra retry middleware already handles transient errors (502, 503, 429).
	// We only retry on parse failures (LLM returned non-JSON).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	resp, err := a.client.Complete(ctx, req)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("autopilot LLM call: %w", err)
	}

	decision, parseErr := parseDecision(resp.Message.Text())
	if parseErr != nil {
		// Retry once on parse failure — LLM may produce valid JSON on second try.
		ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
		resp2, err2 := a.client.Complete(ctx2, req)
		cancel2()
		if err2 != nil {
			return nil, fmt.Errorf("autopilot retry: %w", err2)
		}
		decision, parseErr = parseDecision(resp2.Message.Text())
		if parseErr != nil {
			return nil, fmt.Errorf("autopilot: %w", parseErr)
		}
	}
	return decision, nil
}

// autopilotModelDefaults maps provider names to cheap, fast models for gate decisions.
var autopilotModelDefaults = map[string]string{
	"anthropic": "claude-sonnet-4-6",
	"openai":    "gpt-4.1-mini",
	"gemini":    "gemini-2.5-flash",
}

// resolveModel picks the model to use for gate decisions.
// If no explicit model is set, picks the cheapest model from the default provider.
func (a *AutopilotInterviewer) resolveModel() string {
	if a.model != "" {
		return a.model
	}
	// Use the client's default provider to pick an appropriate model.
	if defaultProvider := a.client.DefaultProvider(); defaultProvider != "" {
		if model, ok := autopilotModelDefaults[defaultProvider]; ok {
			return model
		}
	}
	return "claude-sonnet-4-6"
}

// buildUserPrompt constructs the prompt with gate context and available options.
func buildUserPrompt(gatePrompt string, options []string, defaultOption string) string {
	var b strings.Builder
	b.WriteString("You are at a decision gate in a pipeline. Here is the context:\n\n")
	b.WriteString(gatePrompt)
	b.WriteString("\n\n")

	if len(options) > 0 {
		b.WriteString("Available options:\n")
		for i, opt := range options {
			marker := "  "
			if opt == defaultOption {
				marker = "* "
			}
			b.WriteString(fmt.Sprintf("%s%d. \"%s\"\n", marker, i+1, opt))
		}
		if defaultOption != "" {
			b.WriteString(fmt.Sprintf("\n(* = default option: \"%s\")\n", defaultOption))
		}
	} else {
		b.WriteString("This is a freeform gate — provide your response as text.\n")
	}

	b.WriteString("\nRespond with ONLY a JSON object:\n")
	b.WriteString(`{"choice": "<exact option text>", "reasoning": "<1-3 sentence explanation>"}`)
	b.WriteString("\n\nFor freeform gates, put your text response in the \"choice\" field.\n")

	return b.String()
}

// parseDecision extracts the structured decision from LLM response text.
func parseDecision(text string) (*autopilotDecision, error) {
	jsonText, err := extractJSONObject(text, "no JSON object found in response")
	if err != nil {
		return nil, err
	}
	var decision autopilotDecision
	if err := json.Unmarshal([]byte(jsonText), &decision); err != nil {
		return nil, fmt.Errorf("parse autopilot decision: %w", err)
	}
	if decision.Choice == "" {
		return nil, fmt.Errorf("autopilot returned empty choice")
	}
	return &decision, nil
}

// matchChoice finds the best match for a choice string against available options.
// Prefers exact match, then longest prefix/contains match to avoid ambiguity.
func matchChoice(choice string, options []string) string {
	normalized := strings.ToLower(strings.TrimSpace(choice))

	// Exact match (case-insensitive)
	for _, opt := range options {
		if strings.ToLower(opt) == normalized {
			return opt
		}
	}

	// Longest option that the choice contains (avoids "a" matching before "abandon")
	var bestMatch string
	for _, opt := range options {
		lower := strings.ToLower(opt)
		if strings.Contains(normalized, lower) && len(opt) > len(bestMatch) {
			bestMatch = opt
		}
	}
	return bestMatch
}

// fallback returns the default option, or the first option, or empty string.
func (a *AutopilotInterviewer) fallback(options []string, defaultOption string) string {
	if defaultOption != "" {
		return defaultOption
	}
	if len(options) > 0 {
		return options[0]
	}
	return ""
}

// AskInterview implements InterviewInterviewer by sending all questions to the
// LLM in a single prompt and parsing the structured JSON response.
// Provider errors hard-fail per CLAUDE.md. Only parse failures retry once.
func (a *AutopilotInterviewer) AskInterview(questions []Question, prev *InterviewResult) (*InterviewResult, error) {
	prompt := buildInterviewPrompt(questions)

	// Include previous answers so the LLM can build on them.
	if prev != nil && len(prev.Questions) > 0 {
		var prevSection strings.Builder
		prevSection.WriteString("\nPreviously answered:\n")
		for _, ans := range prev.Questions {
			if ans.Answer != "" {
				prevSection.WriteString(fmt.Sprintf("- %s: %s\n", ans.Text, ans.Answer))
			}
		}
		prompt += prevSection.String()
	}

	req := &llm.Request{
		Model: a.resolveModel(),
		Messages: []llm.Message{
			llm.SystemMessage(personaPrompts[a.persona]),
			llm.UserMessage(prompt),
		},
		MaxTokens: intPtr(2048),
	}
	temp := 0.1
	req.Temperature = &temp

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	resp, err := a.client.Complete(ctx, req)
	cancel()
	if err != nil {
		return nil, fmt.Errorf("autopilot interview LLM call: %w", err)
	}

	result, parseErr := parseInterviewResponse(resp.Message.Text(), questions)
	if parseErr != nil {
		// Retry once on parse failure — LLM may produce valid JSON on second try.
		ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
		resp2, err2 := a.client.Complete(ctx2, req)
		cancel2()
		if err2 != nil {
			return nil, fmt.Errorf("autopilot interview retry: %w", err2)
		}
		result, parseErr = parseInterviewResponse(resp2.Message.Text(), questions)
		if parseErr != nil {
			return nil, fmt.Errorf("autopilot interview: %w", parseErr)
		}
	}
	return result, nil
}

// buildInterviewPrompt formats all questions into a single LLM prompt requesting
// a JSON response with answers for each question.
//
// NOTE: Question text comes from upstream agent output and is embedded verbatim.
// A compromised upstream agent could craft question text that attempts to
// manipulate the autopilot's decision (prompt injection). This is an inherent
// limitation of using LLM output as LLM input. For option-constrained questions,
// the risk is mitigated by matchChoice validation. For open-ended questions,
// no validation is applied to the answer content.
func buildInterviewPrompt(questions []Question) string {
	var b strings.Builder
	b.WriteString("Answer each of the following questions. For questions with options in parentheses, pick one of the listed options.\n")
	b.WriteString("Respond with valid JSON in this exact format:\n")
	b.WriteString(`{"answers": [{"id": "q1", "answer": "your answer", "elaboration": "optional details"}, ...]}`)
	b.WriteString("\n\nQuestions:\n")
	for _, q := range questions {
		b.WriteString(fmt.Sprintf("%d. %s", q.Index, q.Text))
		if len(q.Options) > 0 {
			b.WriteString(fmt.Sprintf(" (%s)", strings.Join(q.Options, ", ")))
		} else if q.IsYesNo {
			b.WriteString(" (yes, no)")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// interviewResponseAnswer is the wire format for a single answer in the LLM response.
type interviewResponseAnswer struct {
	ID          string `json:"id"`
	Answer      string `json:"answer"`
	Elaboration string `json:"elaboration,omitempty"`
}

// interviewResponseEnvelope is the top-level wire format from the LLM.
type interviewResponseEnvelope struct {
	Answers []interviewResponseAnswer `json:"answers"`
}

// parseInterviewResponse parses the LLM's JSON response into an InterviewResult.
// It strips markdown code fences, extracts the JSON object, and maps answers back
// to InterviewAnswer structs with proper IDs, question text, and options.
func parseInterviewResponse(text string, questions []Question) (*InterviewResult, error) {
	jsonText, err := extractJSONObject(text, "no JSON object found in interview response")
	if err != nil {
		return nil, err
	}

	envelope, err := unmarshalInterviewEnvelope(jsonText)
	if err != nil {
		return nil, err
	}

	answers, incomplete := mapInterviewAnswers(envelope, questions)

	if !anyAnswered(answers) {
		return nil, fmt.Errorf("interview response had no matching answers for %d questions", len(questions))
	}

	return &InterviewResult{Questions: answers, Incomplete: incomplete}, nil
}

// extractJSONObject strips markdown code fences from text, then finds and returns
// the first {...} JSON object substring. errPrefix is prepended to the error message
// when no object is found.
func extractJSONObject(text, errPrefix string) (string, error) {
	text = strings.TrimSpace(text)

	// Strip markdown code fences if present
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		var jsonLines []string
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				continue
			}
			jsonLines = append(jsonLines, line)
		}
		text = strings.Join(jsonLines, "\n")
	}

	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end == -1 || end <= start {
		return "", fmt.Errorf("%s: %.100s", errPrefix, text)
	}
	return text[start : end+1], nil
}

// unmarshalInterviewEnvelope decodes the JSON envelope and validates it has answers.
func unmarshalInterviewEnvelope(jsonText string) (interviewResponseEnvelope, error) {
	var envelope interviewResponseEnvelope
	if err := json.Unmarshal([]byte(jsonText), &envelope); err != nil {
		return envelope, fmt.Errorf("parse interview response: %w", err)
	}
	if len(envelope.Answers) == 0 {
		return envelope, fmt.Errorf("interview response contains no answers")
	}
	return envelope, nil
}

// mapInterviewAnswers builds an []InterviewAnswer in question order by looking up
// LLM answers by ID and normalising option-constrained and yes/no answers.
// Returns the answers slice and whether any question was left unanswered.
func mapInterviewAnswers(envelope interviewResponseEnvelope, questions []Question) ([]InterviewAnswer, bool) {
	// Build a lookup map from LLM answer ID to the raw answer.
	answerByID := make(map[string]interviewResponseAnswer, len(envelope.Answers))
	for _, a := range envelope.Answers {
		answerByID[a.ID] = a
	}

	incomplete := false
	answers := make([]InterviewAnswer, len(questions))
	for i, q := range questions {
		id := fmt.Sprintf("q%d", q.Index)
		ans := InterviewAnswer{
			ID:      id,
			Text:    q.Text,
			Options: q.Options,
		}
		if raw, ok := answerByID[id]; ok {
			ans.Elaboration = raw.Elaboration
			ans.Answer = normaliseAnswer(raw.Answer, q)
		}
		if ans.Answer == "" {
			incomplete = true
		}
		answers[i] = ans
	}
	return answers, incomplete
}

// normaliseAnswer maps a raw LLM answer to the correct value for question q.
// For option-constrained questions it validates via matchChoice; for yes/no it
// canonicalises to "yes"/"no"; for open-ended questions it returns the raw value.
func normaliseAnswer(raw string, q Question) string {
	if len(q.Options) > 0 {
		return matchChoice(raw, q.Options)
	}
	if q.IsYesNo {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "yes", "y", "true":
			return "yes"
		case "no", "n", "false":
			return "no"
		}
		return ""
	}
	return raw
}

// anyAnswered returns true if at least one answer in the slice has a non-empty Answer.
func anyAnswered(answers []InterviewAnswer) bool {
	for _, a := range answers {
		if a.Answer != "" {
			return true
		}
	}
	return false
}

func intPtr(n int) *int { return &n }
