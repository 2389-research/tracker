// ABOUTME: Human gate handler that pauses pipeline execution for human decision-making.
// ABOUTME: Uses an Interviewer interface to present choices derived from outgoing edge labels.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

var errHumanTimeout = fmt.Errorf("human gate timed out waiting for input")

// cancelInterviewer tears down an interviewer that supports cancellation. Called
// when a gate times out so the blocked Ask goroutine unblocks instead of leaking
// (#446). Non-cancellable interviewers are a documented no-op.
// Cancel() implementations MUST be idempotent and safe to call after Ask has
// already returned — a select-race can invoke this after fn completed.
func cancelInterviewer(i Interviewer) {
	if c, ok := i.(interface{ Cancel() }); ok {
		c.Cancel()
	}
}

// withTimeout runs fn in a goroutine and returns its result, or errHumanTimeout
// if the duration elapses first. A zero timeout means no timeout.
//
// On timeout, the goroutine running fn is canceled via cancelInterviewer when i
// implements Cancel(); otherwise it may leak until the underlying I/O unblocks.
func withTimeout(timeout time.Duration, i Interviewer, fn func() (string, error)) (string, error) {
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
		cancelInterviewer(i)
		return "", errHumanTimeout
	}
}

// withTimeoutOutcome is like withTimeout but for functions returning (Outcome, error).
func withTimeoutOutcome(timeout time.Duration, i Interviewer, fn func() (pipeline.Outcome, error)) (pipeline.Outcome, error) {
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
		cancelInterviewer(i)
		return pipeline.Outcome{}, errHumanTimeout
	}
}

func parseHumanTimeout(node *pipeline.Node) time.Duration {
	return node.HumanConfig().Timeout
}

// Interviewer defines the interface for presenting choices to a human (or automated)
// decision-maker. Implementations control how the prompt and choices are displayed
// and how the response is collected.
type Interviewer interface {
	Ask(prompt string, choices []string, defaultChoice string) (string, error)
}

// actorOf returns the Actor classification for an Interviewer by querying its
// optional Actor() method via interface assertion. This pattern avoids adding
// a method to the exported Interviewer interface (which would break third-party
// implementations); interviewers in the tracker codebase implement the method,
// third-party implementations default to ActorUnknown.
//
// Used by HumanHandler.Execute to populate Outcome.OverrideActor.
func actorOf(i Interviewer) pipeline.Actor {
	if i == nil {
		return pipeline.ActorUnknown
	}
	if a, ok := i.(interface{ Actor() pipeline.Actor }); ok {
		return a.Actor()
	}
	return pipeline.ActorUnknown
}

// ContextSetter is an optional interface for interviewers that can receive a
// pipeline context for cancellation and timeout propagation. The human handler
// calls SetPipelineContext via type assertion before invoking any interviewer
// methods, so that LLM-backed interviewers can respect pipeline cancellation.
type ContextSetter interface {
	SetPipelineContext(ctx context.Context)
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

// Actor returns ActorAutopilot — deterministic auto-accept, no human in the loop.
func (a *AutoApproveInterviewer) Actor() pipeline.Actor { return pipeline.ActorAutopilot }

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

// Actor returns ActorAutopilot — deterministic auto-accept, no human in the loop.
// Defined explicitly (rather than relying on embedding) so the mapping is grep-able.
func (a *AutoApproveFreeformInterviewer) Actor() pipeline.Actor { return pipeline.ActorAutopilot }

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

// AskFreeformWithLabels returns the defaultLabel if non-empty, otherwise the
// first label. Falls back to "auto-approved" when no labels are provided.
func (a *AutoApproveFreeformInterviewer) AskFreeformWithLabels(prompt string, labels []string, defaultLabel string) (string, error) {
	if defaultLabel != "" {
		return defaultLabel, nil
	}
	if len(labels) > 0 {
		return labels[0], nil
	}
	return "auto-approved", nil
}

// Compile-time assertion: AutoApproveFreeformInterviewer implements InterviewInterviewer.
var _ InterviewInterviewer = (*AutoApproveFreeformInterviewer)(nil)

// Compile-time assertion: AutoApproveFreeformInterviewer implements LabeledFreeformInterviewer.
var _ LabeledFreeformInterviewer = (*AutoApproveFreeformInterviewer)(nil)

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

// HumanHandler implements the pipeline.Handler interface for human gate nodes
// (hexagon shape). It collects outgoing edge labels as choices, presents them
// via the configured Interviewer, and returns the selected label as the
// PreferredLabel in the outcome.
type HumanHandler struct {
	interviewer Interviewer
	graph       *pipeline.Graph
	emitter     pipeline.PipelineEventHandler // #509: gate lifecycle events; nil disables them
}

// NewHumanHandler creates a HumanHandler with the given interviewer and graph.
// The graph is used to look up outgoing edges from the current node to derive choices.
func NewHumanHandler(interviewer Interviewer, graph *pipeline.Graph, opts ...HumanOption) *HumanHandler {
	h := &HumanHandler{interviewer: interviewer, graph: graph}
	for _, opt := range opts {
		opt(h)
	}
	return h
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

	// Capture the bound interviewer's Actor classification once. Every
	// outcome we return must carry it on Outcome.OverrideActor so the
	// engine's edge-selection flip-point (Chunk 5) can populate
	// OverrideDetail.Actor when an override edge is traversed. Setting
	// it on the zero-value outcome returned on error paths is harmless
	// — the field describes the bound interviewer, not the outcome's
	// success. See actorOf for the interface-assertion contract.
	actor := actorOf(h.interviewer)

	gate := h.emitGateOpened(node, pctx, prompt)

	outcome, err := h.dispatchHumanMode(ctx, node, pctx, prompt)

	if errors.Is(err, errHumanTimeout) {
		timeoutOutcome := h.handleHumanTimeout(node)
		timeoutOutcome.OverrideActor = actor
		h.emitGateResolved(node, pctx, gate, timeoutOutcome, actor, true, nil)
		return timeoutOutcome, nil
	}

	outcome.OverrideActor = actor
	h.emitGateResolved(node, pctx, gate, outcome, actor, false, err)
	return outcome, err
}

// dispatchHumanMode routes to the appropriate human input handler based on the node mode.
func (h *HumanHandler) dispatchHumanMode(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext, prompt string) (pipeline.Outcome, error) {
	// Propagate pipeline context to LLM-backed interviewers so that pipeline
	// cancellation (ctrl-C, budget breach) stops autopilot LLM calls promptly.
	if cs, ok := h.interviewer.(ContextSetter); ok {
		cs.SetPipelineContext(ctx)
	}

	// Parse the config once; reuse Mode and Timeout to avoid the double
	// map-walk / ParseDuration that was happening when parseHumanTimeout
	// called HumanConfig() a second time from the interview branch.
	cfg := node.HumanConfig()
	switch cfg.Mode {
	case "interview":
		return withTimeoutOutcome(cfg.Timeout, h.interviewer, func() (pipeline.Outcome, error) {
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
	cfg := node.HumanConfig()
	action := cfg.TimeoutAction
	if action == "" {
		action = "default"
	}
	if action == "fail" {
		return pipeline.Outcome{Status: pipeline.OutcomeFail, ContextUpdates: map[string]string{
			pipeline.ContextKeyHumanResponse: "timed out",
		}}
	}
	def := cfg.DefaultChoice
	if def == "" {
		return pipeline.Outcome{Status: pipeline.OutcomeFail, ContextUpdates: map[string]string{
			pipeline.ContextKeyHumanResponse: "timed out (no default)",
		}}
	}
	routing := mapSelectionToRoutingKey(h.graph.OutgoingEdges(node.ID), def)
	return pipeline.Outcome{
		Status:         pipeline.OutcomeSuccess,
		PreferredLabel: routing,
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

	// A human gate may author a full multi-line `prompt:` body (like an agent
	// node) in addition to the short `label:` title. Historically this body was
	// dropped for freeform/choice/yes_no modes — only Label was shown — so any
	// ${ctx.*} interpolation the gate relied on to surface live state (e.g.
	// EscalateMilestone's verify result, ApprovePlan's plan files) never
	// rendered. Append it to the label so authored prompt bodies are displayed;
	// label-only gates are unchanged. Expansion below covers both.
	if body := strings.TrimSpace(node.Attrs["prompt"]); body != "" {
		prompt = prompt + "\n\n" + body
	}

	var graphAttrs map[string]string
	if h.graph != nil {
		graphAttrs = h.graph.Attrs
	}
	params := pipeline.ExtractParamsFromGraphAttrs(graphAttrs)
	// Assign the expanded result unconditionally. Only-if-non-empty
	// would leave literal ${...} placeholders in the prompt when a
	// variable legitimately resolves to empty. If expansion errors,
	// leave the original prompt alone (lenient behaviour — a human
	// gate should still show something to the user).
	if expanded, err := pipeline.ExpandVariables(prompt, pctx, params, graphAttrs, false); err == nil {
		prompt = expanded
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = fmt.Sprintf("Human gate: %s", node.ID)
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
	cfg := node.HumanConfig()
	// Freeform mode specifically wants the bare "default" attr (not
	// default_choice) because freeform labels map to edge labels, not to
	// labeled-choice indices; keep the legacy semantic.
	defaultLabel := node.Attrs["default"]
	timeout := cfg.Timeout

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
		return withTimeout(timeout, lfi, func() (string, error) {
			return lfi.AskFreeformWithLabels(prompt, labels, defaultLabel)
		})
	}
	return withTimeout(timeout, fi, func() (string, error) {
		return fi.AskFreeform(prompt)
	})
}

// matchFreeformLabel tries to match freeform response text against outgoing edge labels.
// When an edge has a Choice key (DIP150), matching by Label returns Choice so the
// engine routes on the stable key rather than the display label. A direct match against
// the Choice key is also accepted when the responder sends the key directly.
func matchFreeformLabel(graph *pipeline.Graph, node *pipeline.Node, response string) string {
	normalized := strings.ToLower(strings.TrimSpace(response))
	for _, e := range graph.OutgoingEdges(node.ID) {
		if m, ok := matchEdgeByLabelOrChoice(e, normalized); ok {
			return m
		}
	}
	// matchFreeformLabel compares against the bare "default" attr (not
	// DefaultChoice) because this is only used for label matching in
	// freeform mode, which keys on edge labels.
	if defLabel := node.Attrs["default"]; defLabel != "" && strings.ToLower(defLabel) == normalized {
		return defLabel
	}
	return ""
}

// matchEdgeByLabelOrChoice reports whether the normalized response matches this
// edge's label or its Choice key. On a label match it returns the Choice key
// (DIP150) when present so the engine routes on the stable key, else the label.
func matchEdgeByLabelOrChoice(e *pipeline.Edge, normalized string) (string, bool) {
	if e.Label != "" && strings.ToLower(e.Label) == normalized {
		if e.Choice != "" {
			return e.Choice, true
		}
		return e.Label, true
	}
	// Also accept a direct match against the Choice key.
	if e.Choice != "" && strings.ToLower(e.Choice) == normalized {
		return e.Choice, true
	}
	return "", false
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
	cfg := node.HumanConfig()
	questionsKey = cfg.QuestionsKey
	if questionsKey == "" {
		questionsKey = pipeline.ContextKeyInterviewQuestions
	}
	answersKey = cfg.AnswersKey
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
	prompt := node.HumanConfig().Prompt
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

	jsonStr, err := SerializeInterviewResult(*result)
	if err != nil {
		return pipeline.Outcome{Status: pipeline.OutcomeFail}, fmt.Errorf("serialize interview result for node %q: %w", node.ID, err)
	}
	summary := BuildMarkdownSummary(*result)

	status := pipeline.OutcomeSuccess
	if result.Canceled {
		status = pipeline.OutcomeFail
	}

	outcome := pipeline.Outcome{
		Status: status,
		ContextUpdates: map[string]string{
			answersKey:                                  jsonStr,
			pipeline.ContextKeyHumanResponse:            summary,
			pipeline.ContextKeyResponsePrefix + node.ID: summary,
		},
	}
	if applyInterviewDeclaredWrites(node, outcome.ContextUpdates, result) {
		outcome.Status = pipeline.OutcomeFail
	}
	return outcome, nil
}

func applyInterviewDeclaredWrites(node *pipeline.Node, contextUpdates map[string]string, result *InterviewResult) bool {
	if result == nil {
		return false
	}
	if len(pipeline.ParseDeclaredKeys(node.HumanConfig().Writes)) == 0 {
		return false
	}
	raw, err := buildInterviewAnswersObjectJSON(result)
	if err != nil {
		contextUpdates[contextKeyWritesError] = fmt.Sprintf("node %q interview answer serialization failed: %v", node.ID, err)
		return true
	}
	return applyDeclaredWrites(node, contextUpdates, raw, "Interview answers JSON")
}

func buildInterviewAnswersObjectJSON(result *InterviewResult) (string, error) {
	obj := make(map[string]string, len(result.Questions)*2)
	for _, q := range result.Questions {
		answer := strings.TrimSpace(q.Answer)
		if answer == "" {
			continue
		}
		putAnswerIfAbsent(obj, strings.TrimSpace(q.ID), answer)
		putAnswerIfAbsent(obj, normalizeInterviewQuestionKey(q.Text), answer)
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// putAnswerIfAbsent stores answer under key (skipping empty keys) without
// clobbering an existing value, so the first question to claim a key wins.
func putAnswerIfAbsent(obj map[string]string, key, answer string) {
	if key == "" {
		return
	}
	if _, exists := obj[key]; !exists {
		obj[key] = answer
	}
}

func normalizeInterviewQuestionKey(text string) string {
	text = strings.TrimSpace(strings.ToLower(text))
	if text == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range text {
		if isKeyRune(r) {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

// isKeyRune reports whether r is a lowercase alphanumeric — the only runes kept
// verbatim when normalizing a question into a context key.
func isKeyRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// executeChoice handles choice mode: presents outgoing edge labels as options.
func (h *HumanHandler) executeChoice(node *pipeline.Node, prompt string) (pipeline.Outcome, error) {
	if h.graph == nil {
		return pipeline.Outcome{}, fmt.Errorf("human gate node %q: choice mode requires graph edges but handler has no graph", node.ID)
	}
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

	cfg := node.HumanConfig()
	selected, err := withTimeout(cfg.Timeout, h.interviewer, func() (string, error) {
		// Choice mode specifically uses the bare default_choice attr —
		// HumanConfig.DefaultChoice would fall back to "default", which
		// is wrong here because "default" means edge-label in freeform
		// mode. Keep the direct read to preserve that distinction.
		return h.interviewer.Ask(prompt, choices, node.Attrs["default_choice"])
	})
	if err != nil {
		return pipeline.Outcome{}, fmt.Errorf("human gate choice selection failed for node %q: %w", node.ID, err)
	}

	return pipeline.Outcome{Status: pipeline.OutcomeSuccess, PreferredLabel: mapSelectionToRoutingKey(edges, selected)}, nil
}

// mapSelectionToRoutingKey translates a human-selected display label to the
// edge's Choice routing key when one is set. Falls back to the label itself
// (or edge.To for unlabeled edges) when no Choice is present, preserving
// the behaviour of edgeRoutingKey for the same edge.
func mapSelectionToRoutingKey(edges []*pipeline.Edge, selected string) string {
	for _, e := range edges {
		label := e.Label
		if label == "" {
			label = e.To
		}
		if label == selected && e.Choice != "" {
			return e.Choice
		}
	}
	return selected
}

// executeYesNo handles yes_no mode: presents Yes/No choices and maps them to
// OutcomeSuccess (Yes) or OutcomeFail (No) so pipelines can route with
// ctx.outcome = success / ctx.outcome = fail conditions.
func (h *HumanHandler) executeYesNo(node *pipeline.Node, prompt string) (pipeline.Outcome, error) {
	timeout := parseHumanTimeout(node)
	selected, err := withTimeout(timeout, h.interviewer, func() (string, error) {
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
