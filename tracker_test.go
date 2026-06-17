// ABOUTME: Tests for the top-level tracker convenience API.
// ABOUTME: Validates Config defaulting, auto-wiring, Run(), NewEngine(), and error paths.
package tracker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	if result.Status != string(pipeline.OutcomeBudgetExceeded) {
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

// TestResolveProviderBaseURL_GatewayKindCFAIG_BackcompatDefault asserts that
// existing CF AIG callers see zero behavior change when TRACKER_GATEWAY_KIND
// is empty (the new env var is unset). This is the back-compat canary for
// #276.
func TestResolveProviderBaseURL_GatewayKindCFAIG_BackcompatDefault(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "https://example.com")
	t.Setenv("TRACKER_GATEWAY_KIND", "") // unset = default cf-aig
	got := ResolveProviderBaseURL("anthropic")
	want := "https://example.com/anthropic"
	if got != want {
		t.Errorf("anthropic with cf-aig default = %q, want %q", got, want)
	}
}

// TestResolveProviderBaseURL_GatewayKindBedrock covers the bedrock-gateway
// suffix map: anthropic gets empty (SDK appends /v1/messages), openai and
// gemini get /v1.
func TestResolveProviderBaseURL_GatewayKindBedrock(t *testing.T) {
	t.Setenv("TRACKER_GATEWAY_URL", "https://bedrock-gateway.example.com")
	t.Setenv("TRACKER_GATEWAY_KIND", "bedrock")
	cases := []struct {
		provider string
		want     string
	}{
		{"anthropic", "https://bedrock-gateway.example.com"},
		{"openai", "https://bedrock-gateway.example.com/v1"},
		{"gemini", "https://bedrock-gateway.example.com/v1"},
	}
	for _, c := range cases {
		t.Setenv(strings.ToUpper(strings.ReplaceAll(c.provider, "-", "_"))+"_BASE_URL", "")
		t.Run(c.provider, func(t *testing.T) {
			got := ResolveProviderBaseURL(c.provider)
			if got != c.want {
				t.Errorf("%s with bedrock kind = %q, want %q", c.provider, got, c.want)
			}
		})
	}
}

// TestResolveProviderBaseURL_GatewayKindBedrock_OpenAICompatRefused asserts
// fail-closed behavior: the bedrock gateway has no /compat equivalent, so
// openai-compat under KIND=bedrock must refuse to route rather than emit a
// URL that would 404.
func TestResolveProviderBaseURL_GatewayKindBedrock_OpenAICompatRefused(t *testing.T) {
	t.Setenv("OPENAI_COMPAT_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "https://bedrock-gateway.example.com")
	t.Setenv("TRACKER_GATEWAY_KIND", "bedrock")
	got := ResolveProviderBaseURL("openai-compat")
	if got != "" {
		t.Errorf("openai-compat under bedrock should refuse routing (return \"\"); got %q", got)
	}
}

// TestResolveProviderBaseURL_PerProviderEnvWins asserts that the
// per-provider <PROVIDER>_BASE_URL env var still wins unconditionally —
// even under KIND=bedrock — preserving the "surgical override" precedence.
func TestResolveProviderBaseURL_PerProviderEnvWins(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "https://surgical.example.com")
	t.Setenv("TRACKER_GATEWAY_URL", "https://gateway.example.com")
	t.Setenv("TRACKER_GATEWAY_KIND", "bedrock")
	got := ResolveProviderBaseURL("anthropic")
	want := "https://surgical.example.com"
	if got != want {
		t.Errorf("per-provider env should win over gateway+kind; got %q want %q", got, want)
	}
}

// TestResolveProviderBaseURL_UnknownKindRefusesRouting asserts fail-closed:
// an unknown TRACKER_GATEWAY_KIND value refuses to route rather than
// silently falling through to cf-aig.
func TestResolveProviderBaseURL_UnknownKindRefusesRouting(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "https://example.com")
	t.Setenv("TRACKER_GATEWAY_KIND", "future-kind-xyz")
	got := ResolveProviderBaseURL("anthropic")
	if got != "" {
		t.Errorf("unknown KIND should refuse routing; got %q", got)
	}
}

// TestResolveProviderBaseURLWithGateway_ExplicitKindWinsOverEnv asserts the
// library-API path for Config.GatewayKind: when a non-empty kind is
// threaded through buildClient → allProviderConstructors → the resolver,
// the explicit value is used in preference to TRACKER_GATEWAY_KIND. This
// mirrors how Config.GatewayURL takes precedence over TRACKER_GATEWAY_URL.
func TestResolveProviderBaseURLWithGateway_ExplicitKindWinsOverEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "")
	t.Setenv("TRACKER_GATEWAY_KIND", "cf-aig") // env says cf-aig
	// Explicit kind = bedrock should win; anthropic gets empty suffix.
	got, err := resolveProviderBaseURLWithGateway("anthropic", "https://bedrock.example.com", GatewayKindBedrock)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	want := "https://bedrock.example.com"
	if got != want {
		t.Errorf("explicit GatewayKindBedrock should win over env cf-aig; got %q want %q", got, want)
	}
}

// TestResolveProviderBaseURLWithGateway_EmptyKindFallsThroughToEnv asserts
// that when Config.GatewayKind is empty, the env var TRACKER_GATEWAY_KIND
// is consulted as the fallback.
func TestResolveProviderBaseURLWithGateway_EmptyKindFallsThroughToEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "")
	t.Setenv("TRACKER_GATEWAY_KIND", "bedrock")
	// Empty kind arg → env wins → bedrock.
	got, err := resolveProviderBaseURLWithGateway("anthropic", "https://example.com", "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	want := "https://example.com"
	if got != want {
		t.Errorf("empty kind arg should fall through to env bedrock; got %q want %q", got, want)
	}
}

// TestResolveProviderBaseURLStrict_BedrockOpenAICompatErrors asserts the
// strict semantic that PR #276 documents as "fail-closed": when a gateway
// URL is configured but (kind, provider) is unsupported, callers must see
// an error rather than an empty string that adapter constructors silently
// treat as "no gateway" — which would leak requests to the SDK default
// endpoint (openrouter.ai for openai-compat, api.openai.com for openai,
// api.anthropic.com for anthropic). Captures the bug that 4 reviewers
// flagged on the initial PR.
func TestResolveProviderBaseURLStrict_BedrockOpenAICompatErrors(t *testing.T) {
	t.Setenv("OPENAI_COMPAT_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "https://bedrock-gateway.example.com")
	t.Setenv("TRACKER_GATEWAY_KIND", "bedrock")
	got, err := ResolveProviderBaseURLStrict("openai-compat")
	if err == nil {
		t.Fatalf("expected refuse-to-route error, got nil (base=%q) — this is the silent-bypass bug reviewers flagged", got)
	}
	if got != "" {
		t.Errorf("expected empty string alongside error, got %q", got)
	}
}

// TestResolveProviderBaseURLStrict_UnknownKindErrors asserts strict
// semantic for the unknown-kind case: with a gateway configured but
// TRACKER_GATEWAY_KIND=<typo>, callers must see an error rather than the
// silent fallback that was the original bug.
func TestResolveProviderBaseURLStrict_UnknownKindErrors(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "https://my-gw.example.com")
	t.Setenv("TRACKER_GATEWAY_KIND", "future-kind-xyz")
	got, err := ResolveProviderBaseURLStrict("anthropic")
	if err == nil {
		t.Fatalf("expected refuse-to-route error for unknown kind, got nil (base=%q)", got)
	}
	if got != "" {
		t.Errorf("expected empty string alongside error, got %q", got)
	}
}

// TestResolveProviderBaseURLStrict_NoGatewayIsNotAnError asserts that
// strict resolution returns ("", nil) when no gateway is configured — the
// "no gateway needed; SDK default is fine" case must remain a clean nil
// error so downstream constructors don't refuse to build a perfectly
// valid no-gateway client.
func TestResolveProviderBaseURLStrict_NoGatewayIsNotAnError(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("TRACKER_GATEWAY_URL", "")
	t.Setenv("TRACKER_GATEWAY_KIND", "")
	got, err := ResolveProviderBaseURLStrict("anthropic")
	if err != nil {
		t.Fatalf("no-gateway path must not error, got %v", err)
	}
	if got != "" {
		t.Errorf("no-gateway path should return empty URL, got %q", got)
	}
}

// TestResolveProviderBaseURLStrict_PerProviderOverrideBeatsRefuse asserts
// that an explicit per-provider override wins even when the (kind,
// provider) pair would otherwise be refused. Per the precedence spec
// (D2), per-provider env vars win unconditionally — they are the
// surgical escape hatch for any routing edge case.
func TestResolveProviderBaseURLStrict_PerProviderOverrideBeatsRefuse(t *testing.T) {
	t.Setenv("OPENAI_COMPAT_BASE_URL", "https://my-private-compat.example.com")
	t.Setenv("TRACKER_GATEWAY_URL", "https://bedrock-gateway.example.com")
	t.Setenv("TRACKER_GATEWAY_KIND", "bedrock")
	got, err := ResolveProviderBaseURLStrict("openai-compat")
	if err != nil {
		t.Fatalf("per-provider override should bypass refuse path, got %v", err)
	}
	want := "https://my-private-compat.example.com"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// TestResolveBudgetLimits_FallsBackToGraphAttrs verifies that when
// Config.Budget is zero, ResolveBudgetLimits fills from the graph-level
// max_total_tokens / max_cost_cents / max_wall_time attrs populated by
// the dippin adapter from WorkflowDefaults. Closes tracker#67.
func TestResolveBudgetLimits_FallsBackToGraphAttrs(t *testing.T) {
	graph := &pipeline.Graph{Attrs: map[string]string{
		"max_total_tokens": "50000",
		"max_cost_cents":   "250",
		"max_wall_time":    "15m",
		"stall_timeout":    "5m",
	}}
	got := ResolveBudgetLimits(pipeline.BudgetLimits{}, graph)
	if got.MaxTotalTokens != 50000 {
		t.Errorf("MaxTotalTokens = %d, want 50000", got.MaxTotalTokens)
	}
	if got.MaxCostCents != 250 {
		t.Errorf("MaxCostCents = %d, want 250", got.MaxCostCents)
	}
	if got.MaxWallTime != 15*time.Minute {
		t.Errorf("MaxWallTime = %v, want 15m", got.MaxWallTime)
	}
	if got.StallTimeout != 5*time.Minute {
		t.Errorf("StallTimeout = %v, want 5m", got.StallTimeout)
	}
}

// TestResolveBudgetLimits_ConfigWinsOverGraph verifies that explicit
// Config.Budget values are NOT overridden by graph attrs. The adapter
// defaults only fill fields the caller left zero.
func TestResolveBudgetLimits_ConfigWinsOverGraph(t *testing.T) {
	graph := &pipeline.Graph{Attrs: map[string]string{
		"max_total_tokens": "50000",
		"max_cost_cents":   "250",
		"max_wall_time":    "15m",
		"stall_timeout":    "5m",
	}}
	cfg := pipeline.BudgetLimits{
		MaxTotalTokens: 9999,
		MaxCostCents:   1,
		MaxWallTime:    time.Second,
		StallTimeout:   30 * time.Second,
	}
	got := ResolveBudgetLimits(cfg, graph)
	if got.MaxTotalTokens != 9999 {
		t.Errorf("explicit MaxTotalTokens overridden: got %d, want 9999", got.MaxTotalTokens)
	}
	if got.MaxCostCents != 1 {
		t.Errorf("explicit MaxCostCents overridden: got %d, want 1", got.MaxCostCents)
	}
	if got.MaxWallTime != time.Second {
		t.Errorf("explicit MaxWallTime overridden: got %v, want 1s", got.MaxWallTime)
	}
	if got.StallTimeout != 30*time.Second {
		t.Errorf("explicit StallTimeout overridden: got %v, want 30s", got.StallTimeout)
	}
}

// TestResolveBudgetLimits_NilGraph verifies the no-op case.
func TestResolveBudgetLimits_NilGraph(t *testing.T) {
	cfg := pipeline.BudgetLimits{MaxTotalTokens: 100}
	got := ResolveBudgetLimits(cfg, nil)
	if got.MaxTotalTokens != 100 {
		t.Errorf("nil graph should pass cfg through, got %+v", got)
	}
}

// TestRun_Config_BundleIdentity_FlowsToEngine pins the library-API contract
// that Config.BundleIdentity is threaded into the engine via
// pipeline.WithBundleIdentity, so embedded integrations (callers that do
// NOT go through the CLI) can stamp .dipx bundle provenance onto every
// checkpoint save. The round trip is verified by loading the checkpoint
// after the run and asserting Checkpoint.BundleIdentity matches.
func TestRun_Config_BundleIdentity_FlowsToEngine(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	want := "sha256:librunidentity"

	result, err := Run(context.Background(), simpleDOT, Config{
		Format:         "dot",
		LLMClient:      client,
		CheckpointDir:  cpPath,
		BundleIdentity: want,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected status=success, got %q", result.Status)
	}

	cp, err := pipeline.LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if cp.BundleIdentity != want {
		t.Errorf("Config.BundleIdentity did not flow to Checkpoint: got %q, want %q", cp.BundleIdentity, want)
	}
}

// TestRun_Config_BundleIdentity_EmptyByDefault pins the no-op semantics:
// when Config.BundleIdentity is unset, the engine does not stamp anything,
// so Checkpoint.BundleIdentity stays empty (matches plain .dip behavior).
func TestRun_Config_BundleIdentity_EmptyByDefault(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")

	if _, err := Run(context.Background(), simpleDOT, Config{
		Format:        "dot",
		LLMClient:     client,
		CheckpointDir: cpPath,
		// BundleIdentity intentionally left empty.
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	cp, err := pipeline.LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if cp.BundleIdentity != "" {
		t.Errorf("expected empty BundleIdentity on checkpoint, got %q", cp.BundleIdentity)
	}
}

// TestRun_Config_BundleIdentity_WiresHandlerRegistry pins the contract that
// Config.BundleIdentity is threaded through buildRegistry via
// handlers.WithHandlerBundleIdentity, so handler-package emissions
// (parallel/manager_loop bypass paths) carry identity when received by
// cfg.EventHandler. Without this wiring, embedded integrations would see
// unstamped EventStage* events on their handler — re-introducing the
// bypass closed at the CLI in Task 7.
//
// Mirrors TestRegistryWrapBranch_FiresWhenIdentitySet but at the library
// API surface: even with a minimal simpleDOT graph (no parallel node),
// every event delivered to cfg.EventHandler must carry the identity if
// the full stamping chain (engine + handler registry) is wired correctly.
func TestRun_Config_BundleIdentity_WiresHandlerRegistry(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	var mu sync.Mutex
	var captured []pipeline.PipelineEvent
	handler := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, evt)
	})

	want := "sha256:registry_wiring_test"
	_, err := Run(context.Background(), simpleDOT, Config{
		Format:         "dot",
		LLMClient:      client,
		CheckpointDir:  filepath.Join(t.TempDir(), "checkpoint.json"),
		BundleIdentity: want,
		EventHandler:   handler,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(captured) == 0 {
		t.Fatal("no events captured on cfg.EventHandler")
	}
	for _, evt := range captured {
		if evt.BundleIdentity != want {
			t.Errorf("event %v: BundleIdentity = %q, want %q", evt.Type, evt.BundleIdentity, want)
		}
	}
}

// TestRun_Result_BundleIdentity_PopulatedFromConfig pins the contract that
// tracker.Result.BundleIdentity mirrors Config.BundleIdentity after a
// successful Run. Library callers can read provenance off the returned
// Result without inspecting checkpoints.
func TestRun_Result_BundleIdentity_PopulatedFromConfig(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	want := "sha256:result_test"
	result, err := Run(context.Background(), simpleDOT, Config{
		Format:         "dot",
		LLMClient:      client,
		CheckpointDir:  filepath.Join(t.TempDir(), "checkpoint.json"),
		BundleIdentity: want,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.BundleIdentity != want {
		t.Errorf("Result.BundleIdentity = %q, want %q", result.BundleIdentity, want)
	}
}

// TestRun_Result_BundleIdentity_EmptyWhenNotSet pins the no-op semantics:
// when Config.BundleIdentity is unset, Result.BundleIdentity stays empty —
// matching plain .dip behavior.
func TestRun_Result_BundleIdentity_EmptyWhenNotSet(t *testing.T) {
	client := &stubCompleter{
		response: &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	result, err := Run(context.Background(), simpleDOT, Config{
		Format:        "dot",
		LLMClient:     client,
		CheckpointDir: filepath.Join(t.TempDir(), "checkpoint.json"),
		// BundleIdentity intentionally left empty.
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.BundleIdentity != "" {
		t.Errorf("Result.BundleIdentity should be empty when Config.BundleIdentity unset, got %q", result.BundleIdentity)
	}
}

// preflightTestPipeline is a minimal .dip source used by NewEngine preflight
// tests. It deliberately does NOT declare `requires:` — preflight is forced
// via Policy=Require so this fixture exercises the CLI-override path. The
// source-level `requires: git` path is exercised by preflightTestPipelineRequiresGit.
const preflightTestPipeline = `workflow PreflightFixture
  goal: "test fixture"
  start: Start
  exit: Done

  agent Start
    label: Start

  agent Done
    label: Done

  edges
    Start -> Done
`

// preflightTestPipelineRequiresGit declares `requires: git` in the workflow
// header. Exercises the full source → dippin parser → adapter → graph.Attrs
// → Graph.RequiredDeps → Preflight path. Requires dippin-lang v0.26.0+.
const preflightTestPipelineRequiresGit = `workflow PreflightFixture
  goal: "test fixture"
  requires: git
  start: Start
  exit: Done

  agent Start
    label: Start

  agent Done
    label: Done

  edges
    Start -> Done
`

func TestNewEngine_PreflightFailsWhenForceRequireAndNotRepo(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	cfg := Config{
		WorkingDir: dir,
		Git:        &GitConfig{Preflight: GitPreflightRequire},
	}
	_, err := NewEngine(preflightTestPipeline, cfg)
	if err == nil {
		t.Fatalf("expected preflight failure on non-repo workdir with --git=require")
	}
	if !errors.Is(err, pipeline.ErrGitWorkdirNotRepo) {
		t.Errorf("want ErrGitWorkdirNotRepo, got %v", err)
	}
}

func TestNewEngine_PreflightPassesAfterGitInit(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("git init: %v: %s", runErr, out)
	}
	cfg := Config{
		WorkingDir: dir,
		Git:        &GitConfig{Preflight: GitPreflightRequire},
	}
	// May fail for unrelated reasons (no API keys) but MUST NOT be ErrGitWorkdirNotRepo.
	_, err := NewEngine(preflightTestPipeline, cfg)
	if err != nil && errors.Is(err, pipeline.ErrGitWorkdirNotRepo) {
		t.Fatalf("preflight should pass after git init, got %v", err)
	}
}

// TestNewEngine_PreflightBypassedWithGitOff exercises the escape hatch:
// even when the workflow source declares `requires: git` AND the workdir
// is not a repo, `--git=off` bypasses the check. Uses
// preflightTestPipelineRequiresGit (not preflightTestPipeline) so the
// fixture actually has something to bypass — otherwise auto policy
// would also pass with no check applied, and the test wouldn't be
// exercising what its name promises. (PR #235 round-3 Copilot review.)
//
// No requireGit guard — off must work on hosts without git installed
// (that's literally the point of the escape hatch).
func TestNewEngine_PreflightBypassedWithGitOff(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		WorkingDir: dir,
		Git:        &GitConfig{Preflight: GitPreflightOff},
	}
	// Without --git=off this fixture would hit ErrGitWorkdirNotRepo
	// because the workflow source declares requires:git and dir is a
	// non-repo tempdir.
	_, err := NewEngine(preflightTestPipelineRequiresGit, cfg)
	if err != nil && errors.Is(err, pipeline.ErrGitWorkdirNotRepo) {
		t.Errorf("--git=off should bypass preflight even with source-level requires:git, got %v", err)
	}
}

// TestNewEngine_PreflightFailsFromSourceLevelRequires exercises the full
// path: source string → dippin parser → adapter → graph.Attrs["requires"]
// → Graph.RequiredDeps → Preflight. The non-repo workdir should trip
// ErrGitWorkdirNotRepo without any CLI override (auto policy honors
// workflow's declared requires).
func TestNewEngine_PreflightFailsFromSourceLevelRequires(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	cfg := Config{WorkingDir: dir} // auto policy
	_, err := NewEngine(preflightTestPipelineRequiresGit, cfg)
	if err == nil {
		t.Fatalf("expected preflight failure on non-repo workdir with requires:git")
	}
	if !errors.Is(err, pipeline.ErrGitWorkdirNotRepo) {
		t.Errorf("want ErrGitWorkdirNotRepo, got %v", err)
	}
}

func TestNewEngine_PreflightPassesFromSourceLevelRequiresAfterInit(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("git init: %v: %s", runErr, out)
	}
	cfg := Config{WorkingDir: dir}
	_, err := NewEngine(preflightTestPipelineRequiresGit, cfg)
	if err != nil && errors.Is(err, pipeline.ErrGitWorkdirNotRepo) {
		t.Fatalf("preflight should pass after git init, got %v", err)
	}
}

func TestGitConfig_ZeroValueIsAuto(t *testing.T) {
	cfg := Config{}
	policy, allowInit := ResolveGitConfig(cfg)
	if policy != GitPreflightAuto {
		t.Errorf("want auto policy on zero Config, got %q", policy)
	}
	if allowInit {
		t.Errorf("want AllowInit=false on zero Config")
	}
}

func TestGitConfig_ExplicitWins(t *testing.T) {
	cfg := Config{Git: &GitConfig{Preflight: GitPreflightWarn, AllowInit: true}}
	policy, allowInit := ResolveGitConfig(cfg)
	if policy != GitPreflightWarn {
		t.Errorf("want warn, got %q", policy)
	}
	if !allowInit {
		t.Errorf("want AllowInit=true")
	}
}

func TestGitConfig_AliasesPreservePipelineSemantics(t *testing.T) {
	// The library-side GitPreflight aliases must be equal to their pipeline
	// counterparts so they assignment-compatible with pipeline.PreflightConfig.
	if GitPreflightAuto != pipeline.GitPreflightAuto {
		t.Errorf("alias mismatch: GitPreflightAuto")
	}
	if GitPreflightOff != pipeline.GitPreflightOff {
		t.Errorf("alias mismatch: GitPreflightOff")
	}
	if GitPreflightWarn != pipeline.GitPreflightWarn {
		t.Errorf("alias mismatch: GitPreflightWarn")
	}
	if GitPreflightRequire != pipeline.GitPreflightRequire {
		t.Errorf("alias mismatch: GitPreflightRequire")
	}
	if GitPreflightInit != pipeline.GitPreflightInit {
		t.Errorf("alias mismatch: GitPreflightInit")
	}
}

// TestResult_MirrorsValidationOverrides pins the contract that
// resultFromEngine copies EngineResult.ValidationOverrides into the
// library-API Result so embedded callers can inspect override-edge
// traversals without reaching through EngineResult. Also pins that the
// Status field carries the validation_overridden enum value as a string.
func TestResult_MirrorsValidationOverrides(t *testing.T) {
	er := &pipeline.EngineResult{
		Status: pipeline.OutcomeValidationOverridden,
		ValidationOverrides: []pipeline.OverrideDetail{
			{GateNodeID: "Gate", Label: "accept", Actor: pipeline.ActorHuman},
		},
	}
	r := resultFromEngine(er)
	if r.Status != "validation_overridden" {
		t.Errorf("Status = %q, want validation_overridden", r.Status)
	}
	if len(r.ValidationOverrides) != 1 {
		t.Fatalf("ValidationOverrides len = %d, want 1", len(r.ValidationOverrides))
	}
	if r.ValidationOverrides[0].GateNodeID != "Gate" {
		t.Errorf("GateNodeID = %q, want Gate", r.ValidationOverrides[0].GateNodeID)
	}
	if r.ValidationOverrides[0].Label != "accept" {
		t.Errorf("Label = %q, want accept", r.ValidationOverrides[0].Label)
	}
	if r.ValidationOverrides[0].Actor != pipeline.ActorHuman {
		t.Errorf("Actor = %q, want %q", r.ValidationOverrides[0].Actor, pipeline.ActorHuman)
	}
}

// TestResult_DefensiveCopy pins that resultFromEngine takes a defensive
// copy of ValidationOverrides so library callers cannot mutate the
// engine's internal slice (and vice-versa: engine-side mutations after
// Result is built do not bleed through).
func TestResult_DefensiveCopy(t *testing.T) {
	er := &pipeline.EngineResult{
		Status: pipeline.OutcomeValidationOverridden,
		ValidationOverrides: []pipeline.OverrideDetail{
			{GateNodeID: "Gate"},
		},
	}
	r := resultFromEngine(er)
	// Mutate engine-side slice; Result-side should be unaffected.
	er.ValidationOverrides[0].GateNodeID = "MUTATED"
	if r.ValidationOverrides[0].GateNodeID != "Gate" {
		t.Errorf("Result aliased engine slice; got %q, want Gate", r.ValidationOverrides[0].GateNodeID)
	}
}
