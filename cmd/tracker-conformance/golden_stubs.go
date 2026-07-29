// ABOUTME: Extra deterministic stub completers and subgraph loading for the
// ABOUTME: golden-trace harness — covers billing-pause and interview fixtures
// ABOUTME: plus filesystem resolution of subgraph_ref child pipelines.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// billingStubCompleter always returns a provider credit-exhaustion error. The
// message carries a billing signal (`llm.IsBillingError` matches "credit
// balance"/"too low to access"), so a codergen node classifies it as a
// recoverable billing pause — the deterministic way to pin the
// OutcomePausedBilling terminal (#487). The error is a plain error (no typed
// ProviderError), so `BillingHelp` attributes no provider or env-var key and
// the rendered help text stays independent of the host environment.
type billingStubCompleter struct{}

func (billingStubCompleter) Complete(_ context.Context, _ *llm.Request) (*llm.Response, error) {
	return nil, errors.New("stub: credit balance too low to access the API")
}

// interviewQuestionsJSON is the fixed structured-questions payload the interview
// stub emits as its assistant text. `ParseStructuredQuestions` turns it into two
// form fields (a select + a yes/no), which the deterministic auto-approve
// interviewer answers with the first option / "yes" — pinning the interview-mode
// handler contract without any live LLM or human.
const interviewQuestionsJSON = `{"questions":[` +
	`{"text":"Which storage backend?","context":"two candidates found","options":["postgres","sqlite"]},` +
	`{"text":"Proceed with the plan?","options":["yes","no"]}` +
	`]}`

// interviewStubCompleter returns a fixed structured-questions JSON document for
// every turn, so the codergen node feeding an interview gate produces a
// deterministic question set.
type interviewStubCompleter struct{}

func (interviewStubCompleter) Complete(_ context.Context, _ *llm.Request) (*llm.Response, error) {
	return &llm.Response{
		ID:           "stub-interview-response",
		Model:        "stub-model",
		Provider:     "stub",
		Message:      llm.AssistantMessage(interviewQuestionsJSON),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
	}, nil
}

// selectCompleter picks the deterministic completer for a fixture by its base
// name. The name-based dispatch mirrors the budget/retry conventions already in
// the harness: a fixture opts into a behavior by naming itself for it.
func selectCompleter(base string) agent.Completer {
	switch {
	case strings.Contains(base, "retry"):
		return emptyStubCompleter{}
	case strings.Contains(base, "billing"):
		return billingStubCompleter{}
	case strings.Contains(base, "interview"):
		return interviewStubCompleter{}
	default:
		return stubCompleter{}
	}
}

// loadFixtureSubgraphs resolves every subgraph_ref in graph (recursively) to a
// child *Graph, keyed by the raw ref so the handler's direct-lookup contract is
// satisfied. Refs resolve relative to the referencing file's directory. Returns
// nil for a flat pipeline so the caller can leave Config.Subgraphs unset.
func loadFixtureSubgraphs(graph *pipeline.Graph, fixturePath string) (map[string]*pipeline.Graph, error) {
	subgraphs := map[string]*pipeline.Graph{}
	if err := loadSubgraphsRec(graph, filepath.Dir(fixturePath), subgraphs); err != nil {
		return nil, err
	}
	if len(subgraphs) == 0 {
		return nil, nil
	}
	return subgraphs, nil
}

// loadSubgraphsRec walks graph's nodes, loading each unseen subgraph_ref child
// (relative to baseDir) and recursing into it relative to the child's own dir.
func loadSubgraphsRec(graph *pipeline.Graph, baseDir string, subgraphs map[string]*pipeline.Graph) error {
	for _, node := range graph.Nodes {
		ref := node.Attrs["subgraph_ref"]
		if ref == "" || subgraphs[ref] != nil {
			continue
		}
		resolved := filepath.Join(baseDir, ref)
		child, err := parseFixtureGraph(resolved)
		if err != nil {
			return fmt.Errorf("subgraph %q (node %q): %w", ref, node.ID, err)
		}
		subgraphs[ref] = child
		if err := loadSubgraphsRec(child, filepath.Dir(resolved), subgraphs); err != nil {
			return err
		}
	}
	return nil
}

// parseFixtureGraph reads and parses a .dip file into a Graph via the same
// dippin loader tracker.Run uses. Lint diagnostics are ignored (they go to the
// author's stderr in production); only a fatal parse/validate error is surfaced.
func parseFixtureGraph(path string) (*pipeline.Graph, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fixture: %w", err)
	}
	graph, _, err := pipeline.LoadDippinWorkflow(string(source), path)
	if err != nil {
		return nil, fmt.Errorf("parse fixture %s: %w", path, err)
	}
	return graph, nil
}
