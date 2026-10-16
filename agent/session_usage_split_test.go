// ABOUTME: Tests that a session files each response's usage under the provider label that served it
// ABOUTME: (#702): turns a failover lane served land under the lane's label, not the configured provider's.
package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// servedBy stamps resp with the usage label of the provider that served it,
// as *llm.Client does.
func servedBy(provider string, resp *llm.Response) *llm.Response {
	resp.Provider = provider
	return resp
}

// finalReply is a text reply that ends the session, served by provider.
func finalReply(provider string) *llm.Response {
	return &llm.Response{
		Message:      llm.AssistantMessage("all done"),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 7, OutputTokens: 1, TotalTokens: 8},
		Provider:     provider,
	}
}

// runServedTurns runs a session configured for anthropic over the given
// responses, with a "work" tool for their tool calls.
func runServedTurns(t *testing.T, responses ...*llm.Response) SessionResult {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Provider = "anthropic"
	cfg.MaxTurns = 10
	sess := mustNewSession(t, &mockCompleter{responses: responses}, cfg, WithTools(&stubTool{name: "work", output: "ok"}))
	result, err := sess.Run(context.Background(), "build it")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result
}

// assertSplitAddsUp fails the test unless the split's parts sum to the
// session's totals.
func assertSplitAddsUp(t *testing.T, result SessionResult) {
	t.Helper()
	var sum llm.Usage
	for _, part := range result.UsageByProvider {
		sum = sum.Add(part)
	}
	if sum.InputTokens != result.Usage.InputTokens || sum.OutputTokens != result.Usage.OutputTokens || sum.EstimatedCost != result.Usage.EstimatedCost {
		t.Errorf("split sums to %d in / %d out / $%f; session totals are %d / %d / $%f",
			sum.InputTokens, sum.OutputTokens, sum.EstimatedCost,
			result.Usage.InputTokens, result.Usage.OutputTokens, result.Usage.EstimatedCost)
	}
}

func TestSessionFilesFailoverUsageUnderTheLaneThatServedIt(t *testing.T) {
	result := runServedTurns(t,
		servedBy("anthropic", toolCallResponse("c1", "work")),
		servedBy("openai", toolCallResponse("c2", "work")),
		finalReply("openai"),
	)

	if result.Provider != "anthropic" {
		t.Errorf("result.Provider = %q, want the configured anthropic", result.Provider)
	}
	if len(result.UsageByProvider) != 2 {
		t.Fatalf("UsageByProvider = %+v, want anthropic and openai", result.UsageByProvider)
	}
	if got := result.UsageByProvider["anthropic"]; got.InputTokens != 10 || got.OutputTokens != 5 {
		t.Errorf("anthropic served %d in / %d out, want 10 / 5", got.InputTokens, got.OutputTokens)
	}
	if got := result.UsageByProvider["openai"]; got.InputTokens != 17 || got.OutputTokens != 6 {
		t.Errorf("openai served %d in / %d out, want 17 / 6", got.InputTokens, got.OutputTokens)
	}
	assertSplitAddsUp(t, result)
}

func TestSessionServedByOneProviderHasNoUsageSplit(t *testing.T) {
	// An unlabeled response counts as the session's own label.
	result := runServedTurns(t,
		servedBy("anthropic", toolCallResponse("c1", "work")),
		finalReply(""),
	)

	if result.UsageByProvider != nil {
		t.Errorf("UsageByProvider = %+v, want nil when one provider served every turn", result.UsageByProvider)
	}
}

func TestSessionFailoverOnFirstTurnSplitsWithoutAnEmptyEntry(t *testing.T) {
	result := runServedTurns(t, finalReply("openai"))

	if _, ok := result.UsageByProvider["anthropic"]; ok {
		t.Errorf("UsageByProvider = %+v has an entry for anthropic, which served nothing", result.UsageByProvider)
	}
	if got := result.UsageByProvider["openai"]; got.InputTokens != 7 {
		t.Errorf("openai served %d input tokens, want 7", got.InputTokens)
	}
	assertSplitAddsUp(t, result)
}

// TestResume_KeepsUsageSplit: a session that failed over before an interrupt
// resumes with its split, so the resumed node still files the earlier turns
// under the lane that served them.
func TestResume_KeepsUsageSplit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "turns", "Build.json")
	ctx, cancel := context.WithCancel(context.Background())
	cfg := DefaultConfig()
	cfg.Provider = "anthropic"
	cfg.MaxTurns = 10
	cfg.TurnCheckpointPath = path
	client1 := &mockCompleter{responses: []*llm.Response{
		servedBy("anthropic", toolCallResponse("c1", "work")),
		servedBy("openai", toolCallResponse("c2", "work")),
		servedBy("openai", toolCallResponse("c3", "work")),
	}}
	sess1 := mustNewSession(t, client1, cfg, WithTools(&cancelOnNthTool{name: "work", n: 2, cancel: cancel}))
	if _, err := sess1.Run(ctx, "build it"); err == nil {
		t.Fatal("expected interruption error")
	}

	client2 := &mockCompleter{responses: []*llm.Response{finalReply("openai")}}
	sess2 := mustNewSession(t, client2, cfg, WithTools(&stubTool{name: "work", output: "ok"}))
	result, err := sess2.Run(context.Background(), "build it")
	if err != nil {
		t.Fatalf("resume Run: %v", err)
	}

	if got := result.UsageByProvider["anthropic"]; got.InputTokens != 10 {
		t.Errorf("resumed split: anthropic served %d input tokens, want 10 from before the interrupt", got.InputTokens)
	}
	if got := result.UsageByProvider["openai"]; got.InputTokens != 17 {
		t.Errorf("resumed split: openai served %d input tokens, want 17 (10 before the interrupt + 7 after)", got.InputTokens)
	}
	assertSplitAddsUp(t, result)
}
