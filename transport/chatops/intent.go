// ABOUTME: Turns @mention text into a workflow + params (decision D1).
// ABOUTME: An LLM classifier routes free text onto a built-in workflow; a grammar is the fast-path.
package chatops

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
)

// defaultIntentModel is the catalog id the classifier falls back to when the
// configured model isn't a known model — so a stale or mistyped
// TRACKERBOT_MODEL / TRACKERCHAT_MODEL can't 404 (model_not_found) the router.
const defaultIntentModel = "claude-haiku-4-5"

// Intent is a parsed request: which workflow to run and any param overrides.
type Intent struct {
	Workflow string
	Params   map[string]string
}

// IntentResolver turns the free text of an @mention into an Intent.
type IntentResolver interface {
	Resolve(ctx context.Context, text string) (Intent, error)
}

// GrammarResolver understands the explicit form "[run] <workflow> [k=v ...]".
type GrammarResolver struct{}

func (GrammarResolver) Resolve(_ context.Context, text string) (Intent, error) {
	return parseGrammar(text)
}

// parseGrammar parses "[run] <workflow> [key=value ...]".
func parseGrammar(text string) (Intent, error) {
	fields := strings.Fields(stripMention(text))
	if len(fields) > 0 && strings.EqualFold(fields[0], "run") {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return Intent{}, fmt.Errorf("which workflow should I run?")
	}
	in := Intent{Workflow: fields[0], Params: map[string]string{}}
	for _, f := range fields[1:] {
		if k, v, ok := strings.Cut(f, "="); ok {
			in.Params[k] = v
		}
	}
	return in, nil
}

// llmIntentResolver routes free text ("make me an app that …") onto the single
// best built-in workflow using an LLM, extracting any params. An explicit
// workflow name still short-circuits to the deterministic (free) grammar.
//
// DECISION POINT (D1) — this is the natural-language front door. Tune the system
// prompt, the model, or the fallback behavior to taste.
type llmIntentResolver struct {
	client  agent.Completer
	model   string
	catalog []tracker.WorkflowInfo
}

func NewLLMIntentResolver(client agent.Completer, model string) *llmIntentResolver {
	return &llmIntentResolver{client: client, model: resolveIntentModel(model), catalog: tracker.Workflows()}
}

// resolveIntentModel maps a configured model onto its canonical catalog id,
// resolving aliases and falling back to defaultIntentModel when the id isn't a
// known model. This keeps a bad model id from ever reaching the provider as a
// 404 (the classifier never hard-blocks routing on a misconfigured env var).
func resolveIntentModel(model string) string {
	if info := llm.GetModelInfo(model); info != nil {
		return info.ID
	}
	log.Printf("chatops: model %q is not in the catalog; routing with %q instead", model, defaultIntentModel)
	return defaultIntentModel
}

func (r *llmIntentResolver) Resolve(ctx context.Context, text string) (Intent, error) {
	clean := stripMention(text)
	// Grammar fast-path: an explicit, known workflow name wins — deterministic
	// and free, and lets power users bypass the model entirely.
	if in, ok := r.grammarExact(clean); ok {
		return in, nil
	}
	return r.classify(ctx, clean)
}

// grammarExact returns an Intent when the message clearly names a workflow.
func (r *llmIntentResolver) grammarExact(clean string) (Intent, bool) {
	in, err := parseGrammar(clean)
	if err != nil {
		return Intent{}, false
	}
	if !r.inCatalog(in.Workflow) {
		return Intent{}, false
	}
	return in, true
}

const intentSystemPrompt = `You route a user's request to exactly ONE workflow and extract any parameters.
Respond ONLY with JSON of the form:
{"workflow": "<name>", "params": {"key": "value"}, "reason": "<one line>"}
Choose the single best workflow by its exact name from the provided list. If none
fits, return an empty string for "workflow".

Routing bias: when the user only describes an idea of what to build (no written
spec) and asks to build/make/create it, prefer a workflow that discovers and
writes the spec itself (e.g. one whose goal is to "ask what to build … and spec
it") over one that requires reading a pre-existing SPEC.md. The user has not
written a spec, so a spec-requiring workflow would dead-stop.`

func (r *llmIntentResolver) classify(ctx context.Context, text string) (Intent, error) {
	var b strings.Builder
	b.WriteString("Available workflows:\n")
	for _, w := range r.catalog {
		fmt.Fprintf(&b, "- %s: %s\n", w.Name, w.Goal)
	}
	fmt.Fprintf(&b, "\nUser request:\n%s", text)

	resp, err := r.client.Complete(ctx, &llm.Request{
		Model:          r.model,
		Messages:       []llm.Message{llm.SystemMessage(intentSystemPrompt), llm.UserMessage(b.String())},
		ResponseFormat: &llm.ResponseFormat{Type: "json_object"},
	})
	if err != nil {
		// Grammar fallback: a classifier outage (e.g. a provider 404) must never
		// hard-block routing. Log the real error and point the user at the
		// deterministic `run <workflow>` grammar instead of leaking a raw
		// provider error into the thread.
		log.Printf("chatops: intent classification failed (%v); falling back to the grammar", err)
		return Intent{}, fmt.Errorf("natural-language routing is unavailable right now — try `run <workflow>` (options: %s)", r.catalogNames())
	}

	var out struct {
		Workflow string            `json:"workflow"`
		Params   map[string]string `json:"params"`
		Reason   string            `json:"reason"`
	}
	if uerr := json.Unmarshal([]byte(extractJSON(resp.Text())), &out); uerr != nil {
		return Intent{}, fmt.Errorf("couldn't parse the classification (%v)", uerr)
	}
	if out.Workflow == "" || !r.inCatalog(out.Workflow) {
		return Intent{}, fmt.Errorf("I couldn't match that to a workflow. Try `run <workflow>` — options: %s", r.catalogNames())
	}
	if out.Params == nil {
		out.Params = map[string]string{}
	}
	return Intent{Workflow: out.Workflow, Params: out.Params}, nil
}

func (r *llmIntentResolver) inCatalog(name string) bool {
	for _, w := range r.catalog {
		if w.Name == name {
			return true
		}
	}
	return false
}

func (r *llmIntentResolver) catalogNames() string {
	names := make([]string, 0, len(r.catalog))
	for _, w := range r.catalog {
		names = append(names, w.Name)
	}
	return strings.Join(names, ", ")
}

// stripMention drops a leading Slack <@BOTID> mention token if present.
func stripMention(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "<@") {
		if i := strings.Index(text, ">"); i >= 0 {
			return strings.TrimSpace(text[i+1:])
		}
	}
	return text
}

// extractJSON returns the outermost {…} object in s, tolerating code fences or
// prose the model may wrap around the JSON.
func extractJSON(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return s
	}
	return s[start : end+1]
}
