// ABOUTME: Tests for the #347 runtime-facts block injected into every codergen prompt.
// ABOUTME: Verifies working dir, date, run/node identity, ordering, and codergen-only scope.
package handlers

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

func TestPrependRuntimeFacts_FullBlock(t *testing.T) {
	now := time.Date(2026, 6, 10, 15, 4, 5, 0, time.UTC)
	got := prependRuntimeFacts("do the task", "/home/clint/code/code-goblin", "b68b532619c3", "Implement", now)

	if !strings.HasPrefix(got, "# Runtime\n") {
		t.Fatalf("runtime block must be the outermost (first) section, got:\n%s", got)
	}
	for _, want := range []string{
		"/home/clint/code/code-goblin",
		"all commands already run here; never cd elsewhere",
		"A failed cd is a hard error, never evidence of completion",
		"Current date: 2026-06-10",
		"Run: b68b532619c3, node: Implement",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, "do the task") {
		t.Fatalf("original prompt must be preserved at the end, got:\n%s", got)
	}
}

func TestPrependRuntimeFacts_EmptyWorkingDirOmitsLine(t *testing.T) {
	now := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	got := prependRuntimeFacts("p", "", "run1", "N", now)

	if strings.Contains(got, "Working directory") {
		t.Fatalf("empty working dir must omit the line entirely, got:\n%s", got)
	}
	if !strings.Contains(got, "Current date: 2026-06-10") {
		t.Fatalf("date line must remain, got:\n%s", got)
	}
}

func TestPrependRuntimeFacts_EmptyRunIDOmitsRunKeepsNode(t *testing.T) {
	now := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	got := prependRuntimeFacts("p", "/tmp", "", "MyNode", now)

	if strings.Contains(got, "Run:") {
		t.Fatalf("empty run ID must omit the Run label, got:\n%s", got)
	}
	if !strings.Contains(got, "Node: MyNode") {
		t.Fatalf("node ID must still be present, got:\n%s", got)
	}
}

func TestPrependRuntimeFacts_RelativeWorkingDirMadeAbsolute(t *testing.T) {
	now := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	got := prependRuntimeFacts("p", ".", "", "N", now)

	abs, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, abs) {
		t.Fatalf("working dir must be printed absolute (%s), got:\n%s", abs, got)
	}
}

// capturePromptCompleter returns a completer that records the user prompt of
// the first request it sees.
func capturePromptCompleter(prompt *string) *scriptedCompleter {
	return &scriptedCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
		onComplete: func(req *llm.Request) {
			for _, m := range req.Messages {
				if m.Role == llm.RoleUser && *prompt == "" {
					*prompt = m.Text()
				}
			}
		},
	}
}

func TestCodergenHandler_InjectsRuntimeFacts(t *testing.T) {
	workdir := t.TempDir()
	var sent string
	h := NewCodergenHandler(capturePromptCompleter(&sent), workdir)
	node := &pipeline.Node{
		ID:      "Implement",
		Shape:   "box",
		Handler: "codergen",
		Attrs:   map[string]string{"prompt": "build the thing"},
	}
	pctx := pipeline.NewPipelineContext()
	pctx.SetInternal(pipeline.InternalKeyArtifactDir, filepath.Join(workdir, ".tracker", "runs", "run42"))

	if _, err := h.Execute(context.Background(), node, pctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(sent, "# Runtime\n") {
		t.Fatalf("prompt sent to backend must start with the runtime block, got:\n%s", sent)
	}
	if !strings.Contains(sent, workdir) {
		t.Errorf("runtime block must name the handler working dir %s, got:\n%s", workdir, sent)
	}
	if !regexp.MustCompile(`Current date: \d{4}-\d{2}-\d{2}\n`).MatchString(sent) {
		t.Errorf("runtime block must carry a YYYY-MM-DD date, got:\n%s", sent)
	}
	if !strings.Contains(sent, "Run: run42, node: Implement") {
		t.Errorf("runtime block must carry run and node identity, got:\n%s", sent)
	}
	if !strings.HasSuffix(strings.TrimSpace(sent), "build the thing") {
		t.Errorf("original prompt must survive at the end, got:\n%s", sent)
	}
}

func TestCodergenHandler_RuntimeFactsHonorPerNodeWorkingDir(t *testing.T) {
	workdir := t.TempDir()
	override := t.TempDir()
	var sent string
	h := NewCodergenHandler(capturePromptCompleter(&sent), workdir)
	node := &pipeline.Node{
		ID:      "gen",
		Shape:   "box",
		Handler: "codergen",
		Attrs: map[string]string{
			"prompt":      "build",
			"working_dir": override,
		},
	}

	if _, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sent, override) {
		t.Errorf("runtime block must reflect the per-node working_dir override %s, got:\n%s", override, sent)
	}
}

func TestResolvePrompt_DoesNotInjectRuntimeFacts(t *testing.T) {
	// Pins codergen-only scope (#347): the shared ResolvePrompt used by other
	// handlers must stay free of the runtime block — injection happens only in
	// CodergenHandler.Execute.
	node := &pipeline.Node{
		ID:    "n",
		Attrs: map[string]string{"prompt": "hello"},
	}
	got, err := ResolvePrompt(node, pipeline.NewPipelineContext(), nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(got, "# Runtime") {
		t.Fatalf("ResolvePrompt must not inject the runtime block, got:\n%s", got)
	}
}
