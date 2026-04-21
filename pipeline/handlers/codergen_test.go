// ABOUTME: Tests for the codergen handler that invokes Layer 2 agent sessions.
// ABOUTME: Uses a mock Completer to verify session creation, prompt passing, and result capture.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/agent"
	agentexec "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

type fakeCompleter struct {
	responseText string
	err          error
}

func (f *fakeCompleter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &llm.Response{
		Message:      llm.AssistantMessage(f.responseText),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
	}, nil
}

type scriptedCompleter struct {
	responses []*llm.Response
	index     int
}

func (s *scriptedCompleter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if s.index >= len(s.responses) {
		return nil, context.DeadlineExceeded
	}
	resp := s.responses[s.index]
	s.index++
	return resp, nil
}

func TestCodergenHandlerName(t *testing.T) {
	h := NewCodergenHandler(nil, "")
	if h.Name() != "codergen" {
		t.Errorf("expected 'codergen', got %q", h.Name())
	}
}

func TestTrackExternalBackendUsageTracksWhenTotalTokensMissing(t *testing.T) {
	tracker := llm.NewTokenTracker()
	h := &CodergenHandler{tokenTracker: tracker}

	h.trackExternalBackendUsage(&ACPBackend{}, llm.Usage{
		InputTokens:  12,
		OutputTokens: 8,
		TotalTokens:  0,
	})

	got := tracker.ProviderUsage("acp")
	if got.InputTokens != 12 || got.OutputTokens != 8 {
		t.Fatalf("acp usage = %+v, want input=12 output=8", got)
	}
}

func TestCodergenHandlerMissingPrompt(t *testing.T) {
	client := &fakeCompleter{responseText: "done"}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{}}
	pctx := pipeline.NewPipelineContext()
	_, err := h.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
}

func TestCodergenHandlerSuccess(t *testing.T) {
	client := &fakeCompleter{responseText: "Hello, World!"}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "Write hello world"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected 'success', got %q", outcome.Status)
	}
	// The handler returns last_response via ContextUpdates for the engine
	// to merge, rather than writing directly to pctx.
	lastResp := outcome.ContextUpdates[pipeline.ContextKeyLastResponse]
	if lastResp != "Hello, World!" {
		t.Errorf("expected 'Hello, World!', got %q", lastResp)
	}
}

func TestCodergenHandlerCapturesOutcomeInContext(t *testing.T) {
	client := &fakeCompleter{responseText: "completed task"}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "do something"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.ContextUpdates[pipeline.ContextKeyLastResponse] != "completed task" {
		t.Errorf("expected context update for last_response")
	}
}

func TestCodergenHandlerLLMError(t *testing.T) {
	client := &fakeCompleter{err: context.DeadlineExceeded}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "do something"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("handler should not return error on LLM failure, got: %v", err)
	}
	if outcome.Status != pipeline.OutcomeRetry {
		t.Errorf("expected 'retry', got %q", outcome.Status)
	}
}

func TestCodergenHandlerConfigurationErrorIsFatal(t *testing.T) {
	cfgErr := &llm.ConfigurationError{SDKError: llm.SDKError{Msg: `unknown provider: "gemini"`}}
	client := &fakeCompleter{err: cfgErr}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "do something"}}
	pctx := pipeline.NewPipelineContext()
	_, err := h.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected hard error for ConfigurationError, got nil")
	}
}

func TestCodergenHandlerAutoStatusSuccess(t *testing.T) {
	client := &fakeCompleter{responseText: "STATUS:success\nAll tests pass."}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "run tests", "auto_status": "true"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected 'success', got %q", outcome.Status)
	}
}

func TestCodergenHandlerAutoStatusFail(t *testing.T) {
	client := &fakeCompleter{responseText: "STATUS:fail\nTests failed."}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "run tests", "auto_status": "true"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected 'fail', got %q", outcome.Status)
	}
}

func TestCodergenHandlerAutoStatusRetry(t *testing.T) {
	client := &fakeCompleter{responseText: "STATUS:retry\nNeed more context."}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "analyze code", "auto_status": "true"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeRetry {
		t.Errorf("expected 'retry', got %q", outcome.Status)
	}
}

func TestCodergenHandlerAutoStatusMultiTurn(t *testing.T) {
	// Simulates a multi-turn agent session where the LLM emits conversational
	// text in early turns and the STATUS: line only in the final turn.
	client := &fakeCompleter{responseText: "I'll fix the failing tests now.\nLet me read the conformance output.\nSTATUS:retry\nSome tests still failing."}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "fix", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "fix failures", "auto_status": "true"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeRetry {
		t.Errorf("expected 'retry' from last STATUS line, got %q", outcome.Status)
	}
}

func TestCodergenHandlerAutoStatusLastWins(t *testing.T) {
	// When multiple STATUS: lines exist, the last one wins.
	client := &fakeCompleter{responseText: "STATUS:success\nFirst pass done.\nSTATUS:retry\nActually needs another pass."}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "fix", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "fix failures", "auto_status": "true"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeRetry {
		t.Errorf("expected 'retry' (last STATUS line wins), got %q", outcome.Status)
	}
}

func TestCodergenHandlerSystemPrompt(t *testing.T) {
	client := &fakeCompleter{responseText: "done"}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "do work", "system_prompt": "You are helpful."}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected 'success', got %q", outcome.Status)
	}
}

func TestCodergenHandlerWritesArtifacts(t *testing.T) {
	workdir := t.TempDir()
	client := &fakeCompleter{responseText: "Hello, World!"}
	h := NewCodergenHandler(client, workdir)
	node := &pipeline.Node{
		ID:      "gen",
		Shape:   "box",
		Handler: "codergen",
		Attrs:   map[string]string{"prompt": "Write hello world"},
	}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Fatalf("expected success, got %q", outcome.Status)
	}

	promptBytes, err := os.ReadFile(filepath.Join(workdir, "gen", "prompt.md"))
	if err != nil {
		t.Fatalf("expected prompt artifact: %v", err)
	}
	if string(promptBytes) != "Write hello world" {
		t.Fatalf("prompt artifact = %q", string(promptBytes))
	}

	responseBytes, err := os.ReadFile(filepath.Join(workdir, "gen", "response.md"))
	if err != nil {
		t.Fatalf("expected response artifact: %v", err)
	}
	response := string(responseBytes)
	if !containsAll(response, "TURN 1", "TEXT:", "Hello, World!") {
		t.Fatalf("response artifact = %q", response)
	}

	statusBytes, err := os.ReadFile(filepath.Join(workdir, "gen", "status.json"))
	if err != nil {
		t.Fatalf("expected status artifact: %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal(statusBytes, &status); err != nil {
		t.Fatalf("status artifact should be valid json: %v", err)
	}
	if status["outcome"] != pipeline.OutcomeSuccess {
		t.Fatalf("status outcome = %v", status["outcome"])
	}
}

func TestCodergenHandlerForwardsAgentEvents(t *testing.T) {
	workdir := t.TempDir()
	client := &scriptedCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{
						{
							Kind: llm.KindToolCall,
							ToolCall: &llm.ToolCallData{
								ID:        "call_1",
								Name:      "read",
								Arguments: json.RawMessage(`{"path":"go.mod"}`),
							},
						},
					},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}
	h := NewCodergenHandler(client, workdir)

	var got []agent.EventType
	h.eventHandler = agent.EventHandlerFunc(func(evt agent.Event) {
		got = append(got, evt.Type)
	})

	node := &pipeline.Node{
		ID:      "gen",
		Shape:   "box",
		Handler: "codergen",
		Attrs:   map[string]string{"prompt": "inspect repo"},
	}
	pctx := pipeline.NewPipelineContext()

	if _, err := h.Execute(context.Background(), node, pctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !containsAgentEvent(got, agent.EventToolCallStart) {
		t.Fatalf("expected forwarded tool call start event, got %v", got)
	}
	if !containsAgentEvent(got, agent.EventToolCallEnd) {
		t.Fatalf("expected forwarded tool call end event, got %v", got)
	}
}

func containsAgentEvent(events []agent.EventType, want agent.EventType) bool {
	for _, evt := range events {
		if evt == want {
			return true
		}
	}
	return false
}

func TestCodergenHandlerExpandsGoalFromContext(t *testing.T) {
	workdir := t.TempDir()
	client := &fakeCompleter{responseText: "ok"}
	h := NewCodergenHandler(client, workdir)
	node := &pipeline.Node{
		ID:      "gen",
		Shape:   "box",
		Handler: "codergen",
		Attrs:   map[string]string{"prompt": "Plan for $goal"},
	}
	pctx := pipeline.NewPipelineContext()
	pctx.Set(pipeline.ContextKeyGoal, "ship a hello world script")

	_, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	promptBytes, err := os.ReadFile(filepath.Join(workdir, "gen", "prompt.md"))
	if err != nil {
		t.Fatalf("expected prompt artifact: %v", err)
	}
	if string(promptBytes) != "Plan for ship a hello world script" {
		t.Fatalf("expanded prompt = %q", string(promptBytes))
	}
}

func TestCodergenHandlerWritesTranscriptForToolOnlyRun(t *testing.T) {
	workdir := t.TempDir()
	client := &scriptedCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "write",
							Arguments: json.RawMessage(`{"path":"note.txt","content":"hello from tool"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.Message{Role: llm.RoleAssistant},
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}
	h := NewCodergenHandler(client, workdir)
	h.env = agentexec.NewLocalEnvironment(workdir)
	node := &pipeline.Node{
		ID:      "gen",
		Shape:   "box",
		Handler: "codergen",
		Attrs:   map[string]string{"prompt": "create a note"},
	}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Fatalf("expected success, got %q", outcome.Status)
	}

	written, err := os.ReadFile(filepath.Join(workdir, "note.txt"))
	if err != nil {
		t.Fatalf("expected tool-created file: %v", err)
	}
	if string(written) != "hello from tool" {
		t.Fatalf("tool-created file = %q", string(written))
	}

	responseBytes, err := os.ReadFile(filepath.Join(workdir, "gen", "response.md"))
	if err != nil {
		t.Fatalf("expected response artifact: %v", err)
	}
	response := string(responseBytes)
	if response == "" {
		t.Fatal("expected non-empty response transcript")
	}
	if !containsAll(response,
		"TURN 1",
		"TOOL CALL: write",
		`{"path":"note.txt","content":"hello from tool"}`,
		"TOOL RESULT: write",
		"wrote 15 bytes to note.txt",
	) {
		t.Fatalf("response artifact missing tool transcript:\n%s", response)
	}

	if outcome.ContextUpdates[pipeline.ContextKeyLastResponse] != "" {
		t.Fatalf("expected empty last_response for tool-only run, got %q", outcome.ContextUpdates[pipeline.ContextKeyLastResponse])
	}
}

func TestCodergenHandlerFidelityCompactMode(t *testing.T) {
	workdir := t.TempDir()
	client := &fakeCompleter{responseText: "done"}
	h := NewCodergenHandler(client, workdir, WithGraphAttrs(map[string]string{}))
	node := &pipeline.Node{
		ID:      "gen",
		Shape:   "box",
		Handler: "codergen",
		Attrs:   map[string]string{"prompt": "do work", "fidelity": "compact"},
	}
	pctx := pipeline.NewPipelineContext()
	pctx.Set(pipeline.ContextKeyGoal, "build a widget")
	pctx.Set(pipeline.ContextKeyOutcome, "success")
	pctx.Set(pipeline.ContextKeyLastResponse, "long prior response that should be excluded")

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected success, got %q", outcome.Status)
	}

	// Verify prompt artifact was written with context summary prepended.
	promptBytes, err := os.ReadFile(filepath.Join(workdir, "gen", "prompt.md"))
	if err != nil {
		t.Fatalf("expected prompt artifact: %v", err)
	}
	promptStr := string(promptBytes)

	// Compact mode: should include goal and outcome but NOT last_response.
	if !strings.Contains(promptStr, "Context Summary") {
		t.Errorf("expected context summary header in prompt, got:\n%s", promptStr)
	}
	if !strings.Contains(promptStr, "build a widget") {
		t.Errorf("expected goal in context summary, got:\n%s", promptStr)
	}
	if strings.Contains(promptStr, "long prior response that should be excluded") {
		t.Errorf("compact mode should NOT include last_response in context summary")
	}
}

func TestCodergenHandlerFidelityFullUsesStandardInjection(t *testing.T) {
	workdir := t.TempDir()
	client := &fakeCompleter{responseText: "done"}
	h := NewCodergenHandler(client, workdir, WithGraphAttrs(map[string]string{}))
	node := &pipeline.Node{
		ID:      "gen",
		Shape:   "box",
		Handler: "codergen",
		Attrs:   map[string]string{"prompt": "do work", "fidelity": "full"},
	}
	pctx := pipeline.NewPipelineContext()
	pctx.Set(pipeline.ContextKeyLastResponse, "prior output")

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected success, got %q", outcome.Status)
	}

	// Full fidelity: should use standard InjectPipelineContext, not context summary.
	promptBytes, err := os.ReadFile(filepath.Join(workdir, "gen", "prompt.md"))
	if err != nil {
		t.Fatalf("expected prompt artifact: %v", err)
	}
	promptStr := string(promptBytes)

	if strings.Contains(promptStr, "Context Summary") {
		t.Errorf("full fidelity should NOT have Context Summary header")
	}
	if !strings.Contains(promptStr, "prior output") {
		t.Errorf("full fidelity should include prior output via standard injection")
	}
}

func TestBuildConfig_CacheToolResults_FromGraphAttrs(t *testing.T) {
	h := &CodergenHandler{
		graphAttrs: map[string]string{"cache_tool_results": "true"},
	}
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{},
	}
	config := h.buildConfig(node)
	if !config.CacheToolResults {
		t.Error("expected CacheToolResults=true from graph attrs")
	}
}

func TestBuildConfig_CacheToolResults_NodeOverridesGraph(t *testing.T) {
	h := &CodergenHandler{
		graphAttrs: map[string]string{"cache_tool_results": "true"},
	}
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{"cache_tool_results": "false"},
	}
	config := h.buildConfig(node)
	if config.CacheToolResults {
		t.Error("expected CacheToolResults=false (node override)")
	}
}

func TestBuildConfig_CacheToolResults_DefaultFalse(t *testing.T) {
	h := &CodergenHandler{}
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{},
	}
	config := h.buildConfig(node)
	if config.CacheToolResults {
		t.Error("expected CacheToolResults=false by default")
	}
}

func TestBuildConfig_PlanBeforeExecute_FromNodeAttr(t *testing.T) {
	h := &CodergenHandler{}
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{"plan_before_execute": "true"},
	}
	config := h.buildConfig(node)
	if !config.PlanBeforeExecute {
		t.Error("expected PlanBeforeExecute=true from node attr plan_before_execute")
	}
}

func TestBuildConfig_PlanBeforeExecute_FromPlanAlias(t *testing.T) {
	h := &CodergenHandler{}
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{"plan": "true"},
	}
	config := h.buildConfig(node)
	if !config.PlanBeforeExecute {
		t.Error("expected PlanBeforeExecute=true from node attr plan")
	}
}

func TestBuildConfig_PlanBeforeExecute_ExplicitKeyBeatsAlias(t *testing.T) {
	h := &CodergenHandler{}
	node := &pipeline.Node{
		ID: "test",
		Attrs: map[string]string{
			"plan_before_execute": "false",
			"plan":                "true",
		},
	}
	config := h.buildConfig(node)
	if config.PlanBeforeExecute {
		t.Error("expected plan_before_execute to take precedence over plan alias")
	}
}

func TestParseClaudeCodeToolAttrsAllowed(t *testing.T) {
	node := &pipeline.Node{
		ID: "test",
		Attrs: map[string]string{
			"allowed_tools": "Read,Write",
		},
	}
	ccCfg := &pipeline.ClaudeCodeConfig{}
	if err := parseClaudeCodeToolAttrs(node, ccCfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ccCfg.AllowedTools) != 2 {
		t.Errorf("expected 2 allowed tools, got %d", len(ccCfg.AllowedTools))
	}
}

func TestParseClaudeCodeToolAttrsBothFails(t *testing.T) {
	node := &pipeline.Node{
		ID: "test",
		Attrs: map[string]string{
			"allowed_tools":    "Read,Write",
			"disallowed_tools": "Bash",
		},
	}
	ccCfg := &pipeline.ClaudeCodeConfig{}
	err := parseClaudeCodeToolAttrs(node, ccCfg)
	if err == nil {
		t.Fatal("expected error when both allowed and disallowed tools are set")
	}
}

func TestParseClaudeCodeBudgetAttrs(t *testing.T) {
	node := &pipeline.Node{
		ID: "test",
		Attrs: map[string]string{
			"max_budget_usd":  "5.50",
			"permission_mode": "plan",
		},
	}
	ccCfg := &pipeline.ClaudeCodeConfig{}
	if err := parseClaudeCodeBudgetAttrs(node, ccCfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ccCfg.MaxBudgetUSD != 5.50 {
		t.Errorf("expected budget 5.50, got %f", ccCfg.MaxBudgetUSD)
	}
	if ccCfg.PermissionMode != pipeline.PermissionPlan {
		t.Errorf("expected plan mode, got %q", ccCfg.PermissionMode)
	}
}

func TestParseClaudeCodeBudgetAttrsInvalid(t *testing.T) {
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{"max_budget_usd": "not-a-number"},
	}
	ccCfg := &pipeline.ClaudeCodeConfig{}
	if err := parseClaudeCodeBudgetAttrs(node, ccCfg); err == nil {
		t.Fatal("expected error for invalid budget")
	}
}

func TestParseClaudeCodeBudgetAttrsInvalidPermission(t *testing.T) {
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{"permission_mode": "yolo"},
	}
	ccCfg := &pipeline.ClaudeCodeConfig{}
	if err := parseClaudeCodeBudgetAttrs(node, ccCfg); err == nil {
		t.Fatal("expected error for invalid permission mode")
	}
}

// TestBuildClaudeCodeConfig is in backend_claudecode_test.go (more comprehensive version).

func TestWithRegistryOptions(t *testing.T) {
	// Test that With* option functions set config fields correctly.
	cfg := &registryConfig{}

	WithLLMClient(nil, "/tmp")(cfg)
	if cfg.workingDir != "/tmp" {
		t.Errorf("expected workingDir /tmp, got %q", cfg.workingDir)
	}

	WithDefaultBackend("claude-code")(cfg)
	if cfg.defaultBackend != "claude-code" {
		t.Errorf("expected defaultBackend claude-code, got %q", cfg.defaultBackend)
	}

	WithSubgraphs(map[string]*pipeline.Graph{"sub": nil})(cfg)
	if len(cfg.subgraphs) != 1 {
		t.Errorf("expected 1 subgraph, got %d", len(cfg.subgraphs))
	}

	WithInterviewer(nil, nil)(cfg)
	// Just verifying it doesn't panic.

	WithAgentEventHandler(nil)(cfg)
	WithPipelineEventHandler(nil)(cfg)
	WithExecEnvironment(nil)(cfg)
}

func TestCodergenHandler_WritesPerNodeResponse(t *testing.T) {
	client := &fakeCompleter{responseText: "per-node output"}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "mynode", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "test"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.ContextUpdates[pipeline.ContextKeyLastResponse] != "per-node output" {
		t.Errorf("last_response = %q, want %q", outcome.ContextUpdates[pipeline.ContextKeyLastResponse], "per-node output")
	}
	perNodeKey := pipeline.ContextKeyResponsePrefix + "mynode"
	if outcome.ContextUpdates[perNodeKey] != "per-node output" {
		t.Errorf("%s = %q, want %q", perNodeKey, outcome.ContextUpdates[perNodeKey], "per-node output")
	}
}

func TestCodergenHandler_DeclaredWritesExtracted(t *testing.T) {
	client := &fakeCompleter{responseText: `{"milestone_id":"m1","files":["a.go"]}`}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{
		ID:      "planner",
		Shape:   "box",
		Handler: "codergen",
		Attrs: map[string]string{
			"prompt": "test",
			"writes": "milestone_id,files",
		},
	}

	outcome, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Fatalf("status = %q, want success", outcome.Status)
	}
	if got := outcome.ContextUpdates["milestone_id"]; got != "m1" {
		t.Fatalf("milestone_id = %q, want m1", got)
	}
	if got := outcome.ContextUpdates["files"]; got != `["a.go"]` {
		t.Fatalf("files = %q, want [\"a.go\"]", got)
	}
}

func TestCodergenHandler_DeclaredWritesMissingKeyFails(t *testing.T) {
	client := &fakeCompleter{responseText: `{"milestone_id":"m1"}`}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{
		ID:      "planner",
		Shape:   "box",
		Handler: "codergen",
		Attrs: map[string]string{
			"prompt": "test",
			"writes": "milestone_id,files",
		},
	}

	outcome, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Fatalf("status = %q, want fail", outcome.Status)
	}
	if outcome.ContextUpdates[contextKeyWritesError] == "" {
		t.Fatal("expected writes_error to be set")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func TestParseAutoStatus_CaseInsensitive(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"STATUS: Success\nDone.", pipeline.OutcomeSuccess},
		{"STATUS: FAIL\nBroken.", pipeline.OutcomeFail},
		{"STATUS: Retry\nNeed more.", pipeline.OutcomeRetry},
		{"STATUS: SUCCESS\nAll good.", pipeline.OutcomeSuccess},
	}
	for _, tt := range tests {
		got := parseAutoStatus(tt.input)
		if got != tt.want {
			t.Errorf("parseAutoStatus(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseAutoStatus_SkipsCodeBlock(t *testing.T) {
	input := "Here is how to set status:\n```\nSTATUS:fail\n```\nSTATUS:success\nDone."
	got := parseAutoStatus(input)
	if got != pipeline.OutcomeSuccess {
		t.Errorf("parseAutoStatus with code block = %q, want %q", got, pipeline.OutcomeSuccess)
	}
}

func TestParseAutoStatus_OnlyCodeBlockDefaultsToSuccess(t *testing.T) {
	input := "Some output.\n```\nSTATUS:fail\n```\nNo real status here."
	got := parseAutoStatus(input)
	if got != pipeline.OutcomeSuccess {
		t.Errorf("parseAutoStatus code-block-only = %q, want %q", got, pipeline.OutcomeSuccess)
	}
}

// alwaysToolCallCompleter returns a tool call on every turn, forcing the agent
// to exhaust its turn limit without stopping naturally.
type alwaysToolCallCompleter struct {
	turn int
}

func (c *alwaysToolCallCompleter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	c.turn++
	return &llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				{Kind: llm.KindText, Text: "I'm still working on it..."},
				{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCallData{
						ID:        fmt.Sprintf("call_%d", c.turn),
						Name:      "read",
						Arguments: json.RawMessage(`{"path":"go.mod"}`),
					},
				},
			},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
		Usage:        llm.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
	}, nil
}

func TestCodergenHandlerMaxTurnsExhaustedIsFail(t *testing.T) {
	workdir := t.TempDir()
	client := &alwaysToolCallCompleter{}
	h := NewCodergenHandler(client, workdir)

	node := &pipeline.Node{
		ID:      "busy_agent",
		Shape:   "box",
		Handler: "codergen",
		Attrs: map[string]string{
			"prompt":    "Build the auth screens",
			"max_turns": "3",
		},
	}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected outcome %q when agent exhausts turn limit, got %q",
			pipeline.OutcomeFail, outcome.Status)
	}

	// The turn_limit_msg context update should explain what happened.
	msg := outcome.ContextUpdates[pipeline.ContextKeyTurnLimitMsg]
	if !strings.Contains(msg, "turn limit") {
		t.Errorf("expected turn_limit_msg to mention turn limit, got %q", msg)
	}

	// last_response preserves whatever the collector captured (may be empty
	// when agent only produced tool calls with no standalone text output).

	// Diagnostic context should be present with correct value.
	if outcome.ContextUpdates["last_turns"] != "3" {
		t.Errorf("expected last_turns to be %q, got %q", "3", outcome.ContextUpdates["last_turns"])
	}

	// turn_limit_msg must NOT be present on normal success outcomes, only failures.
	// This test exercises the failure path; the success test below verifies absence.
}

func TestCodergenHandlerMaxTurnsWithAutoStatusSuccess(t *testing.T) {
	// When auto_status is true and the agent explicitly emitted STATUS:success
	// before hitting the turn limit, the outcome should be success. The
	// turn_limit_msg is still set (for diagnostics) but status is overridden.
	workdir := t.TempDir()

	// Both responses include tool calls, so with max_turns=2 the agent
	// exhausts its turn limit. But the second response includes STATUS:success.
	client := &scriptedCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{
						{Kind: llm.KindText, Text: "working..."},
						{Kind: llm.KindToolCall, ToolCall: &llm.ToolCallData{ID: "c1", Name: "read", Arguments: json.RawMessage(`{"path":"go.mod"}`)}},
					},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
				Usage:        llm.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{
						{Kind: llm.KindText, Text: "STATUS:success\nAll done."},
						{Kind: llm.KindToolCall, ToolCall: &llm.ToolCallData{ID: "c2", Name: "read", Arguments: json.RawMessage(`{"path":"go.mod"}`)}},
					},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
				Usage:        llm.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
			},
		},
	}
	h := NewCodergenHandler(client, workdir)
	node := &pipeline.Node{
		ID:      "agent_with_status",
		Shape:   "box",
		Handler: "codergen",
		Attrs: map[string]string{
			"prompt":      "Build the thing",
			"max_turns":   "2",
			"auto_status": "true",
		},
	}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected outcome %q when auto_status emits success despite turn limit, got %q",
			pipeline.OutcomeSuccess, outcome.Status)
	}

	// turn_limit_msg is still set (MaxTurnsUsed was true) even though
	// auto_status overrode the status to success. Diagnostics are preserved.
	if outcome.ContextUpdates[pipeline.ContextKeyTurnLimitMsg] == "" {
		t.Error("expected turn_limit_msg to be set for diagnostics even on auto_status success")
	}
}

func TestCodergenHandlerMaxTurnsWithAutoStatusFail(t *testing.T) {
	// When auto_status is true and the agent emitted STATUS:fail on its
	// final text-only response, the outcome should be OutcomeFail.
	//
	// Note: auto_status only sees text from EventTextDelta, which is only
	// emitted on turns without tool calls. When an agent exhausts turns
	// entirely via tool calls, auto_status can't see the STATUS line.
	// This test exercises the realistic case: agent does tool work, then
	// emits a final STATUS:fail text response that stops the session.
	workdir := t.TempDir()

	client := &scriptedCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{
						{Kind: llm.KindText, Text: "working on it..."},
						{Kind: llm.KindToolCall, ToolCall: &llm.ToolCallData{ID: "c1", Name: "read", Arguments: json.RawMessage(`{"path":"go.mod"}`)}},
					},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
				Usage:        llm.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
			},
			{
				Message: llm.Message{
					Role:    llm.RoleAssistant,
					Content: []llm.ContentPart{{Kind: llm.KindText, Text: "STATUS:fail\nCould not complete the task."}},
				},
				FinishReason: llm.FinishReason{Reason: "end_turn"},
				Usage:        llm.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
			},
		},
	}
	h := NewCodergenHandler(client, workdir)
	node := &pipeline.Node{
		ID:      "agent_fail_status",
		Shape:   "box",
		Handler: "codergen",
		Attrs: map[string]string{
			"prompt":      "Build the thing",
			"max_turns":   "3",
			"auto_status": "true",
		},
	}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected outcome %q when auto_status emits fail, got %q",
			pipeline.OutcomeFail, outcome.Status)
	}

	// Agent stopped naturally (text-only response), so no turn_limit_msg.
	if outcome.ContextUpdates[pipeline.ContextKeyTurnLimitMsg] != "" {
		t.Error("expected no turn_limit_msg when agent stops naturally with STATUS:fail")
	}
}

func TestCodergenHandlerLoopDetectedMessage(t *testing.T) {
	// When the agent enters a tool call loop, the turn_limit_msg should
	// mention "tool call loop" rather than "turn limit".
	workdir := t.TempDir()

	// Use alwaysToolCallCompleter with identical tool calls on every turn.
	// The agent's loop detection threshold defaults to 10, so max_turns=12
	// gives enough turns for the loop detector to trigger.
	client := &alwaysToolCallCompleter{}
	h := NewCodergenHandler(client, workdir)

	node := &pipeline.Node{
		ID:      "loop_agent",
		Shape:   "box",
		Handler: "codergen",
		Attrs: map[string]string{
			"prompt":    "Build something",
			"max_turns": "12",
		},
	}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected outcome %q for loop-detected agent, got %q",
			pipeline.OutcomeFail, outcome.Status)
	}

	msg := outcome.ContextUpdates[pipeline.ContextKeyTurnLimitMsg]
	if !strings.Contains(msg, "tool call loop") {
		t.Errorf("expected turn_limit_msg to mention 'tool call loop', got %q", msg)
	}
	if strings.Contains(msg, "turn limit") {
		t.Errorf("loop-detected message should NOT mention 'turn limit', got %q", msg)
	}
}
