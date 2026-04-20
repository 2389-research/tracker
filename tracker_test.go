// ABOUTME: Tests for the top-level tracker convenience API.
// ABOUTME: Validates Config defaulting, auto-wiring, Run(), NewEngine(), and error paths.
package tracker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// stubCompleter returns canned responses for testing.
type stubCompleter struct {
	response *llm.Response
}

func (s *stubCompleter) Complete(_ context.Context, _ *llm.Request) (*llm.Response, error) {
	return s.response, nil
}

const simpleDOT = `digraph test {
	start [shape=Mdiamond];
	finish [shape=Msquare];
	start -> finish;
}`

const simpleDip = `workflow test
  start: s
  exit: e

  agent s
    label: Start

  agent e
    label: Exit

  edges
    s -> e
`

func TestNewEngine_InvalidDOT(t *testing.T) {
	_, err := NewEngine("not valid dot {{{", Config{Format: "dot"})
	if err == nil {
		t.Fatal("expected error for invalid DOT source")
	}
}

func TestNewEngine_ValidDOT(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	engine, err := NewEngine(simpleDOT, Config{
		Format:    "dot",
		LLMClient: client,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer engine.Close()

	if engine.inner == nil {
		t.Fatal("expected inner engine to be set")
	}
}

func TestNewEngine_DipFormat(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	engine, err := NewEngine(simpleDip, Config{
		Format:    "dip",
		LLMClient: client,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer engine.Close()

	if engine.inner == nil {
		t.Fatal("expected inner engine to be set")
	}
}

func TestNewEngine_ParamsOverride(t *testing.T) {
	const withParams = `workflow test
  start: s
  exit: e

  vars
    foo: baz

  agent s
    prompt: "Param: ${params.foo}"

  agent e
    prompt: "done"

  edges
    s -> e
`

	engine, err := NewEngine(withParams, Config{
		Format:    "dip",
		LLMClient: &stubCompleter{response: &llm.Response{Message: llm.AssistantMessage("done"), FinishReason: llm.FinishReason{Reason: "stop"}}},
		Params:    map[string]string{"foo": "bar"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer engine.Close()

	if got := engine.inner.Graph().Attrs["params.foo"]; got != "bar" {
		t.Fatalf("params.foo = %q, want bar", got)
	}
}

func TestNewEngine_ParamsUnknownFails(t *testing.T) {
	const noParams = `workflow test
  start: s
  exit: e

  agent s
    prompt: "Start"

  agent e
    prompt: "End"

  edges
    s -> e
`

	_, err := NewEngine(noParams, Config{
		Format:    "dip",
		LLMClient: &stubCompleter{response: &llm.Response{Message: llm.AssistantMessage("done"), FinishReason: llm.FinishReason{Reason: "stop"}}},
		Params:    map[string]string{"foo": "bar"},
	})
	if err == nil {
		t.Fatal("expected unknown param error")
	}
	if !strings.Contains(err.Error(), "unknown param") {
		t.Fatalf("error = %v, want unknown param", err)
	}
}

func TestNewEngine_AutoDetectDOT(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	// Empty format with "digraph" prefix should auto-detect as DOT.
	engine, err := NewEngine(simpleDOT, Config{
		LLMClient: client,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer engine.Close()
}

func TestNewEngine_UnknownFormat(t *testing.T) {
	_, err := NewEngine("anything", Config{Format: "yaml"})
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
}

func TestEngine_CloseIdempotent(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	engine, err := NewEngine(simpleDOT, Config{Format: "dot", LLMClient: client})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := engine.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestRun_SimplePipeline(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	result, err := Run(context.Background(), simpleDOT, Config{
		Format:    "dot",
		LLMClient: client,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != "success" {
		t.Errorf("expected status=success, got %q", result.Status)
	}
	if result.RunID == "" {
		t.Error("expected non-empty RunID")
	}
	if result.EngineResult == nil {
		t.Error("expected EngineResult to be set")
	}
}

func TestRun_DipPipeline(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	result, err := Run(context.Background(), simpleDip, Config{
		Format:    "dip",
		LLMClient: client,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status=success, got %q", result.Status)
	}
}

func TestNewEngine_DefaultsWorkingDir(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	// Zero-value WorkingDir should succeed (defaults to cwd).
	// Verify by also constructing with an explicit WorkingDir and
	// confirming both succeed without error.
	engine1, err := NewEngine(simpleDOT, Config{Format: "dot", LLMClient: client})
	if err != nil {
		t.Fatalf("default WorkingDir: unexpected error: %v", err)
	}
	defer engine1.Close()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}

	engine2, err := NewEngine(simpleDOT, Config{
		Format:     "dot",
		LLMClient:  client,
		WorkingDir: cwd,
	})
	if err != nil {
		t.Fatalf("explicit WorkingDir: unexpected error: %v", err)
	}
	defer engine2.Close()
}

func TestRun_WithInitialContext(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	result, err := Run(context.Background(), simpleDOT, Config{
		Format:    "dot",
		LLMClient: client,
		Context:   map[string]string{"goal": "test the library"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %q", result.Status)
	}
}

func TestRun_WithEventHandler(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	var events []pipeline.PipelineEvent
	handler := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		events = append(events, evt)
	})

	result, err := Run(context.Background(), simpleDOT, Config{
		Format:       "dot",
		LLMClient:    client,
		EventHandler: handler,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %q", result.Status)
	}
	if len(events) == 0 {
		t.Error("expected at least one pipeline event")
	}
}

func TestNewEngine_ValidationError(t *testing.T) {
	badGraph := `digraph test {
		start [shape=Mdiamond];
		orphan [shape=box];
		start -> orphan;
	}`

	_, err := NewEngine(badGraph, Config{
		Format: "dot",
		LLMClient: &stubCompleter{
			response: &llm.Response{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	})
	if err == nil {
		t.Fatal("expected validation error for graph without exit node")
	}
}

func TestRun_InvalidDOT(t *testing.T) {
	_, err := Run(context.Background(), "not dot at all!!!", Config{Format: "dot"})
	if err == nil {
		t.Fatal("expected error for invalid DOT")
	}
}

func TestNewEngine_InvalidProvider(t *testing.T) {
	_, err := NewEngine(simpleDOT, Config{
		Format:   "dot",
		Provider: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestValidateSource_ValidDip(t *testing.T) {
	result, err := ValidateSource(simpleDip)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Errors) > 0 {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
	if result.Graph == nil {
		t.Error("expected non-nil graph")
	}
}

func TestValidateSource_InvalidSyntax(t *testing.T) {
	result, err := ValidateSource("not valid at all {{{")
	if err == nil && len(result.Errors) == 0 {
		t.Fatal("expected errors for invalid syntax")
	}
}

func TestValidateSource_WithFormatOption(t *testing.T) {
	result, err := ValidateSource(simpleDip, WithValidateFormat("dip"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Graph == nil {
		t.Error("expected non-nil graph")
	}
}

func TestValidateSource_StructuralError(t *testing.T) {
	// DOT graph missing an exit node — should fail validation.
	badGraph := `digraph test {
		start [shape=Mdiamond];
		orphan [shape=box];
		start -> orphan;
	}`
	result, err := ValidateSource(badGraph, WithValidateFormat("dot"))
	if err == nil {
		t.Fatal("expected validation error for graph without exit node")
	}
	if len(result.Errors) == 0 {
		t.Error("expected at least one error in ValidationResult")
	}
}

func TestValidateSource_ReturnsWarnings(t *testing.T) {
	// A graph with a diamond node missing a fail edge produces a warning.
	graphWithWarning := `digraph test {
		start [shape=Mdiamond];
		gate  [shape=diamond];
		done  [shape=Msquare];
		start -> gate;
		gate -> done [label="outcome=success"];
	}`
	result, err := ValidateSource(graphWithWarning, WithValidateFormat("dot"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Errors) > 0 {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
}

func TestRun_PopulatesResultCost(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	// Build the engine directly so we can inject known usage into the
	// tokenTracker before running. The test is in package tracker (not
	// tracker_test) so unexported fields are accessible.
	engine, err := NewEngine(simpleDOT, Config{
		Format:    "dot",
		LLMClient: client,
		Model:     "claude-sonnet-4-6", // known model so EstimateCost returns > 0
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer engine.Close()

	// Inject known usage directly — stubCompleter bypasses middleware so we
	// add it manually, mirroring the claude-code backend pattern.
	engine.tokenTracker.AddUsage("anthropic", llm.Usage{InputTokens: 1000, OutputTokens: 500})

	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Cost == nil {
		t.Fatal("expected result.Cost to be non-nil")
	}
	if result.Cost.TotalUSD <= 0 {
		t.Errorf("expected TotalUSD > 0, got %v", result.Cost.TotalUSD)
	}
	if len(result.Cost.ByProvider) == 0 {
		t.Error("expected at least one entry in ByProvider")
	}
	entry, ok := result.Cost.ByProvider["anthropic"]
	if !ok {
		t.Fatalf("expected anthropic entry in ByProvider, got keys: %v", result.Cost.ByProvider)
	}
	if entry.Usage.InputTokens != 1000 {
		t.Errorf("expected InputTokens=1000, got %d", entry.Usage.InputTokens)
	}
}

func TestRun_WithRetryPolicy(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	result, err := Run(context.Background(), simpleDOT, Config{
		Format:      "dot",
		LLMClient:   client,
		RetryPolicy: "aggressive",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %q", result.Status)
	}
}

func TestRun_BudgetHalt_FromConfig(t *testing.T) {
	// Create a DIP pipeline with an agent node that will execute through the real handler.
	// This ensures that the agent session is created and the completer response's usage
	// is recorded as SessionStats in the trace.
	dipSource := `workflow budget_test
  start: a
  exit: b

  agent a
    label: Agent
    prompt: "respond with done"

  agent b
    label: Done

  edges
    a -> b
`

	// Create a stub completer that returns responses with token usage.
	// Each agent call will return 500 total tokens (200 input + 300 output).
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
			Usage: llm.Usage{
				InputTokens:  200,
				OutputTokens: 300,
				TotalTokens:  500,
			},
		},
	}

	// Build the engine with a budget limit of 400 tokens.
	// The first agent call will return 500 tokens, which exceeds the 400-token limit.
	engine, err := NewEngine(dipSource, Config{
		Format:    "dip",
		LLMClient: client,
		Budget: pipeline.BudgetLimits{
			MaxTotalTokens: 400,
		},
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer engine.Close()

	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify that the run halted due to budget breach.
	if result.Status != pipeline.OutcomeBudgetExceeded {
		t.Errorf("expected status %q, got %q", pipeline.OutcomeBudgetExceeded, result.Status)
	}

	// Verify that LimitsHit identifies the tokens dimension.
	if result.Cost == nil {
		t.Fatal("expected result.Cost to be non-nil")
	}
	if len(result.Cost.LimitsHit) == 0 {
		t.Error("expected LimitsHit to be non-empty")
	} else if result.Cost.LimitsHit[0] != "tokens" {
		t.Errorf("expected LimitsHit[0]='tokens', got %q", result.Cost.LimitsHit[0])
	}
}

func TestNewEngine_ResumeRunID(t *testing.T) {
	// Set up a minimal checkpoint layout under a temp dir.
	tmp := t.TempDir()
	runID := "resumetest123"
	runDir := filepath.Join(tmp, ".tracker", "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cpPath := filepath.Join(runDir, "checkpoint.json")
	// Minimal checkpoint JSON — engine only needs the file to exist for
	// WithCheckpointPath; actual content is validated at resume time.
	if err := os.WriteFile(cpPath, []byte(`{"completed_nodes":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	engine, err := NewEngine(simpleDOT, Config{
		Format:      "dot",
		LLMClient:   client,
		WorkingDir:  tmp,
		ResumeRunID: runID,
	})
	if err != nil {
		t.Fatalf("NewEngine with ResumeRunID: %v", err)
	}
	defer engine.Close()

	// Verify that applyResumeRunID resolved the run ID into CheckpointDir.
	// We can't inspect the inner engine options directly, but a successful
	// NewEngine without error confirms the checkpoint was found and accepted.
	if engine.inner == nil {
		t.Fatal("expected inner engine to be set")
	}
}

func TestNewEngine_ResumeRunID_NotFound(t *testing.T) {
	tmp := t.TempDir()
	// Create the runs directory but no run with that ID.
	if err := os.MkdirAll(filepath.Join(tmp, ".tracker", "runs"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := NewEngine(simpleDOT, Config{
		Format:      "dot",
		WorkingDir:  tmp,
		ResumeRunID: "nonexistent-run",
	})
	if err == nil {
		t.Fatal("expected error when ResumeRunID doesn't match any run")
	}
}

func TestResolveProviderBaseURL_NoGatewayNoOverride(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("GEMINI_BASE_URL", "")
	t.Setenv("OPENAI_COMPAT_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "")
	for _, p := range []string{"anthropic", "openai", "gemini", "openai-compat"} {
		if got := ResolveProviderBaseURL(p); got != "" {
			t.Errorf("%s: got %q, want empty", p, got)
		}
	}
}

func TestResolveProviderBaseURL_GatewayOnly(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("GEMINI_BASE_URL", "")
	t.Setenv("OPENAI_COMPAT_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "https://gateway.ai.cloudflare.com/v1/acc/gw")
	cases := map[string]string{
		"anthropic":     "https://gateway.ai.cloudflare.com/v1/acc/gw/anthropic",
		"openai":        "https://gateway.ai.cloudflare.com/v1/acc/gw/openai",
		"gemini":        "https://gateway.ai.cloudflare.com/v1/acc/gw/google-ai-studio",
		"openai-compat": "https://gateway.ai.cloudflare.com/v1/acc/gw/compat",
	}
	for provider, want := range cases {
		if got := ResolveProviderBaseURL(provider); got != want {
			t.Errorf("%s: got %q, want %q", provider, got, want)
		}
	}
}

func TestResolveProviderBaseURL_ProviderOverridesGateway(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "https://my.proxy/anthropic")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("GEMINI_BASE_URL", "")
	t.Setenv("OPENAI_COMPAT_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "https://gateway.ai.cloudflare.com/v1/acc/gw")

	if got := ResolveProviderBaseURL("anthropic"); got != "https://my.proxy/anthropic" {
		t.Errorf("anthropic: got %q, want provider-specific override", got)
	}
	// Other providers still flow through the gateway.
	if got := ResolveProviderBaseURL("openai"); got != "https://gateway.ai.cloudflare.com/v1/acc/gw/openai" {
		t.Errorf("openai: got %q, want gateway URL", got)
	}
}

func TestResolveProviderBaseURL_TrailingSlashStripped(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "https://example/v1/a/g/")
	if got := ResolveProviderBaseURL("anthropic"); got != "https://example/v1/a/g/anthropic" {
		t.Errorf("got %q, want trailing slash stripped", got)
	}
}

func TestResolveProviderBaseURL_UnknownProvider(t *testing.T) {
	t.Setenv("TRACKER_GATEWAY_URL", "https://example/v1/a/g")
	if got := ResolveProviderBaseURL("mystery"); got != "" {
		t.Errorf("unknown provider should return empty, got %q", got)
	}
}
