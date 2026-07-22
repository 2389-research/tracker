// ABOUTME: Tests for intent resolution — grammar and LLM classification.
package chatops

import (
	"context"
	"errors"
	"strings"
	"testing"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/llm"
)

// scriptedCompleter returns a fixed assistant text (used as the classifier's JSON).
type scriptedCompleter struct{ text string }

func (s scriptedCompleter) Complete(context.Context, *llm.Request) (*llm.Response, error) {
	return &llm.Response{Message: llm.AssistantMessage(s.text), FinishReason: llm.FinishReason{Reason: "stop"}}, nil
}

// failCompleter errors if the model is ever called — proves the grammar
// fast-path bypasses the LLM.
type failCompleter struct{ t *testing.T }

func (f failCompleter) Complete(context.Context, *llm.Request) (*llm.Response, error) {
	f.t.Fatal("LLM should not have been called (grammar fast-path expected)")
	return nil, errors.New("unreachable")
}

func testCatalog() []tracker.WorkflowInfo {
	return []tracker.WorkflowInfo{
		{Name: "ask_and_execute", Goal: "explore and implement a request"},
		{Name: "build_product", Goal: "build a product from a spec"},
	}
}

func TestParseGrammar(t *testing.T) {
	in, err := parseGrammar("run build_product target=app mode=fast")
	if err != nil {
		t.Fatalf("parseGrammar: %v", err)
	}
	if in.Workflow != "build_product" || in.Params["target"] != "app" || in.Params["mode"] != "fast" {
		t.Fatalf("parsed = %+v", in)
	}
	if _, err := parseGrammar("run"); err == nil {
		t.Fatal("expected an error for a bare 'run'")
	}
}

func TestLLMIntent_ClassifiesFreeText(t *testing.T) {
	r := &llmIntentResolver{
		client:  scriptedCompleter{text: "```json\n{\"workflow\":\"ask_and_execute\",\"params\":{\"target\":\"a cli\"},\"reason\":\"impl request\"}\n```"},
		model:   "m",
		catalog: testCatalog(),
	}
	in, err := r.Resolve(context.Background(), "<@BOT> make me a cli that greets people")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if in.Workflow != "ask_and_execute" || in.Params["target"] != "a cli" {
		t.Fatalf("intent = %+v", in)
	}
}

func TestLLMIntent_GrammarFastPathBypassesLLM(t *testing.T) {
	r := &llmIntentResolver{
		client:  failCompleter{t: t}, // must not be called
		model:   "m",
		catalog: testCatalog(),
	}
	in, err := r.Resolve(context.Background(), "run build_product foo=bar")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if in.Workflow != "build_product" || in.Params["foo"] != "bar" {
		t.Fatalf("intent = %+v", in)
	}
}

func TestLLMIntent_NoMatchIsAnError(t *testing.T) {
	r := &llmIntentResolver{
		client:  scriptedCompleter{text: `{"workflow":"","reason":"nothing fits"}`},
		model:   "m",
		catalog: testCatalog(),
	}
	if _, err := r.Resolve(context.Background(), "tell me a joke"); err == nil {
		t.Fatal("expected an error when the model matches nothing")
	}
}

func TestLLMIntent_HallucinatedWorkflowRejected(t *testing.T) {
	r := &llmIntentResolver{
		client:  scriptedCompleter{text: `{"workflow":"not_a_real_workflow"}`},
		model:   "m",
		catalog: testCatalog(),
	}
	if _, err := r.Resolve(context.Background(), "do the thing"); err == nil {
		t.Fatal("expected a workflow outside the catalog to be rejected")
	}
}

func TestResolveIntentModel(t *testing.T) {
	// A known catalog id passes through unchanged.
	if got := resolveIntentModel("claude-haiku-4-5"); got != "claude-haiku-4-5" {
		t.Fatalf("catalog id = %q, want claude-haiku-4-5", got)
	}
	// An alias resolves to its canonical id.
	if got := resolveIntentModel("claude-haiku"); got != "claude-haiku-4-5" {
		t.Fatalf("alias = %q, want claude-haiku-4-5", got)
	}
	// A stale/mistyped id (the old dated snapshot) falls back to the default
	// instead of being handed to the provider as a 404.
	if got := resolveIntentModel("claude-haiku-4-5-20251001"); got != defaultIntentModel {
		t.Fatalf("unknown id = %q, want %s", got, defaultIntentModel)
	}
}

// errCompleter always errors, standing in for a provider outage / 404.
type errCompleter struct{}

func (errCompleter) Complete(context.Context, *llm.Request) (*llm.Response, error) {
	return nil, errors.New("model_not_found (404)")
}

func TestLLMIntent_ClassifierOutageDoesNotLeakProviderError(t *testing.T) {
	r := &llmIntentResolver{client: errCompleter{}, model: "m", catalog: testCatalog()}
	_, err := r.Resolve(context.Background(), "make me a cli that greets people")
	if err == nil {
		t.Fatal("expected an error when the classifier is unavailable")
	}
	// The user gets the grammar fallback guidance, not the raw provider error.
	if strings.Contains(err.Error(), "404") {
		t.Fatalf("provider error leaked to the user: %v", err)
	}
	if !strings.Contains(err.Error(), "run <workflow>") {
		t.Fatalf("expected grammar guidance, got: %v", err)
	}
}
