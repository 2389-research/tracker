// ABOUTME: Tests that core LLM types compile and construct correctly.
// ABOUTME: Validates Message, ContentPart, Request, Response, Usage types.
package llm

import (
	"testing"
)

func TestMessageConstruction(t *testing.T) {
	msg := SystemMessage("You are helpful.")
	if msg.Role != RoleSystem {
		t.Errorf("expected RoleSystem, got %v", msg.Role)
	}
	if msg.Text() != "You are helpful." {
		t.Errorf("expected text, got %q", msg.Text())
	}
}

func TestUserMessage(t *testing.T) {
	msg := UserMessage("Hello")
	if msg.Role != RoleUser {
		t.Errorf("expected RoleUser, got %v", msg.Role)
	}
	if msg.Text() != "Hello" {
		t.Errorf("expected Hello, got %q", msg.Text())
	}
}

func TestAssistantMessage(t *testing.T) {
	msg := AssistantMessage("Hi there")
	if msg.Role != RoleAssistant {
		t.Errorf("expected RoleAssistant, got %v", msg.Role)
	}
}

func TestToolResultMessage(t *testing.T) {
	msg := ToolResultMessage("call_123", "72F and sunny", false)
	if msg.Role != RoleTool {
		t.Errorf("expected RoleTool, got %v", msg.Role)
	}
	if msg.ToolCallID != "call_123" {
		t.Errorf("expected call_123, got %q", msg.ToolCallID)
	}
}

func TestUsageAddition(t *testing.T) {
	a := Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150}
	b := Usage{InputTokens: 200, OutputTokens: 100, TotalTokens: 300}
	c := a.Add(b)
	if c.InputTokens != 300 {
		t.Errorf("expected 300 input tokens, got %d", c.InputTokens)
	}
	if c.TotalTokens != 450 {
		t.Errorf("expected 450 total tokens, got %d", c.TotalTokens)
	}
}

// TestUsageFinalizeDerivesTotal pins the SIFT-SUB-09-01 invariant at its single
// source of truth: Finalize sets TotalTokens = fresh input + output, excluding
// cache-read tokens. Every provider translator funnels through this helper.
func TestUsageFinalizeDerivesTotal(t *testing.T) {
	cacheRead := 800
	u := Usage{InputTokens: 200, OutputTokens: 50, CacheReadTokens: &cacheRead}.Finalize()
	if u.TotalTokens != 250 {
		t.Errorf("Finalize TotalTokens = %d, want 250 (200 input + 50 output, 800 cache read excluded)", u.TotalTokens)
	}
}

// TestUsageAddDerivesTotalFromBuckets guards that Add derives the total from the
// summed priced buckets rather than trusting the operands' TotalTokens fields —
// so a stale or provider-inflated total on an input cannot leak into the
// aggregate that the budget guard compares (SIFT-SUB-09-01).
func TestUsageAddDerivesTotalFromBuckets(t *testing.T) {
	// Both operands carry a deliberately wrong TotalTokens (as a cache-inflated
	// provider total would). The result must still be input+output derived.
	a := Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 9999}
	b := Usage{InputTokens: 200, OutputTokens: 100, TotalTokens: 8888}
	c := a.Add(b)
	if c.TotalTokens != 450 {
		t.Errorf("Add TotalTokens = %d, want 450 derived from 300 input + 150 output, not the stale operand totals", c.TotalTokens)
	}
}

// TestUsageTotalIsCacheAndProviderNeutral proves the budget-facing property: two
// normalized usages representing the same fresh input + output but different
// cache states carry an identical total, so an identical --max-tokens budget is
// breached (or not) the same way regardless of provider or cache (SIFT-SUB-09-01).
func TestUsageTotalIsCacheAndProviderNeutral(t *testing.T) {
	heavyCache := 5000
	lightCache := 10
	cached := Usage{InputTokens: 300, OutputTokens: 120, CacheReadTokens: &heavyCache}.Finalize()
	barelyCached := Usage{InputTokens: 300, OutputTokens: 120, CacheReadTokens: &lightCache}.Finalize()
	noCache := Usage{InputTokens: 300, OutputTokens: 120}.Finalize()

	if cached.TotalTokens != noCache.TotalTokens || barelyCached.TotalTokens != noCache.TotalTokens {
		t.Errorf("totals diverged by cache state: heavy=%d light=%d none=%d — budgets would breach differently per provider",
			cached.TotalTokens, barelyCached.TotalTokens, noCache.TotalTokens)
	}
	if noCache.TotalTokens != 420 {
		t.Errorf("TotalTokens = %d, want 420 (300 input + 120 output)", noCache.TotalTokens)
	}
}
