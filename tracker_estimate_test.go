// ABOUTME: Tests EstimateRun prices agent nodes and produces a sane Low..High range.
package tracker

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/internal/chatgptauth"
)

func TestEstimateRun_PricesAgentNodes(t *testing.T) {
	src := `workflow x
  start: Start
  exit: Done
  agent Start
    label: Start
  agent Plan
    model: claude-opus-4-6
    max_turns: 30
    prompt: plan the work
  agent Build
    model: claude-sonnet-4-6
    max_turns: 12
    prompt: build it
  agent Done
    label: done
  edges
    Start -> Plan
    Plan -> Build
    Build -> Done
`
	est, err := EstimateRun(context.Background(), src)
	if err != nil {
		t.Fatalf("EstimateRun: %v", err)
	}
	if est.AgentNodes != 2 {
		t.Fatalf("AgentNodes = %d, want 2 (Plan, Build; Start/Done are passthrough)", est.AgentNodes)
	}
	if len(est.Models) != 2 || est.Models[0] != "claude-opus-4-6" || est.Models[1] != "claude-sonnet-4-6" {
		t.Fatalf("Models = %v", est.Models)
	}
	if !(est.LowUSD > 0 && est.LowUSD <= est.ExpectedUSD && est.ExpectedUSD <= est.HighUSD) {
		t.Fatalf("want 0 < Low <= Expected <= High, got %.4f / %.4f / %.4f", est.LowUSD, est.ExpectedUSD, est.HighUSD)
	}
	if est.HighUSD <= est.LowUSD*2 {
		t.Fatalf("High (%.4f) should dwarf Low (%.4f) given the turn caps", est.HighUSD, est.LowUSD)
	}
}

func TestEstimateRun_NoAgentsIsZero(t *testing.T) {
	src := `workflow x
  start: Start
  exit: Done
  tool Setup
    command:
      echo hi
  agent Start
    label: Start
  agent Done
    label: done
  edges
    Start -> Setup
    Setup -> Done
`
	est, err := EstimateRun(context.Background(), src)
	if err != nil {
		t.Fatalf("EstimateRun: %v", err)
	}
	if est.AgentNodes != 0 || est.HighUSD != 0 {
		t.Fatalf("no LLM agents → 0 cost; got agents=%d high=%.4f", est.AgentNodes, est.HighUSD)
	}
}

// TestEstimateSubscriptionNodesFree pins design §3 "Cost (D6)": under
// TRACKER_OPENAI_AUTH=chatgpt an openai agent node prices at $0 and counts
// toward SubscriptionNodes; an anthropic node in the same graph is unaffected.
// In api-key mode (today's default) nothing changes — SubscriptionNodes is 0
// and the openai node is priced normally.
func TestEstimateSubscriptionNodesFree(t *testing.T) {
	combinedSrc := `workflow x
  start: Start
  exit: Done
  agent Start
    label: Start
  agent OpenAIAgent
    model: gpt-5.6-sol
    provider: openai
    max_turns: 10
    prompt: openai work
  agent AnthropicAgent
    model: claude-opus-4-6
    max_turns: 10
    prompt: anthropic work
  agent Done
    label: done
  edges
    Start -> OpenAIAgent
    OpenAIAgent -> AnthropicAgent
    AnthropicAgent -> Done
`
	anthropicOnlySrc := `workflow x
  start: Start
  exit: Done
  agent Start
    label: Start
  agent AnthropicAgent
    model: claude-opus-4-6
    max_turns: 10
    prompt: anthropic work
  agent Done
    label: done
  edges
    Start -> AnthropicAgent
    AnthropicAgent -> Done
`
	anthropicOnly, err := EstimateRun(context.Background(), anthropicOnlySrc)
	if err != nil {
		t.Fatalf("EstimateRun(anthropic-only): %v", err)
	}
	if anthropicOnly.HighUSD <= 0 {
		t.Fatalf("anthropic-only HighUSD = %v, want > 0 (sanity: claude-opus-4-6 must be catalogued)", anthropicOnly.HighUSD)
	}

	t.Run("chatgpt mode prices the openai node at $0", func(t *testing.T) {
		t.Setenv(chatgptauth.EnvVar, string(chatgptauth.ModeChatGPT))
		got, err := EstimateRun(context.Background(), combinedSrc)
		if err != nil {
			t.Fatalf("EstimateRun: %v", err)
		}
		if got.SubscriptionNodes != 1 {
			t.Errorf("SubscriptionNodes = %d, want 1", got.SubscriptionNodes)
		}
		if got.HighUSD != anthropicOnly.HighUSD {
			t.Errorf("HighUSD = %v, want %v (the openai node must contribute $0 under the subscription)", got.HighUSD, anthropicOnly.HighUSD)
		}
		if got.LowUSD != anthropicOnly.LowUSD || got.ExpectedUSD != anthropicOnly.ExpectedUSD {
			t.Errorf("Low/Expected = %v/%v, want %v/%v", got.LowUSD, got.ExpectedUSD, anthropicOnly.LowUSD, anthropicOnly.ExpectedUSD)
		}
	})

	t.Run("api-key mode prices the openai node normally", func(t *testing.T) {
		t.Setenv(chatgptauth.EnvVar, string(chatgptauth.ModeAPIKey))
		got, err := EstimateRun(context.Background(), combinedSrc)
		if err != nil {
			t.Fatalf("EstimateRun: %v", err)
		}
		if got.SubscriptionNodes != 0 {
			t.Errorf("SubscriptionNodes = %d, want 0", got.SubscriptionNodes)
		}
		if got.HighUSD <= anthropicOnly.HighUSD {
			t.Errorf("HighUSD = %v, want > %v (the openai node should be priced normally, not $0)", got.HighUSD, anthropicOnly.HighUSD)
		}
	})
}
