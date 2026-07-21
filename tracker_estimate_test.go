// ABOUTME: Tests EstimateRun prices agent nodes and produces a sane Low..High range.
package tracker

import (
	"context"
	"testing"
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
