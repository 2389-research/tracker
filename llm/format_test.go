// ABOUTME: Tests for Response and Usage pretty-print formatting.
// ABOUTME: Validates human-readable output including cache info, cost, and latency display.
package llm

import (
	"strings"
	"testing"
	"time"
)

func TestResponseString(t *testing.T) {
	resp := Response{
		Provider: "anthropic",
		Model:    "claude-opus-4-6",
		Usage: Usage{
			InputTokens:  245,
			OutputTokens: 89,
			EstimatedCost: 0.003,
		},
		Latency:      1200 * time.Millisecond,
		FinishReason: FinishReason{Reason: "stop"},
	}

	s := resp.String()
	if !strings.Contains(s, "[anthropic/claude-opus-4-6]") {
		t.Errorf("expected provider/model, got: %s", s)
	}
	if !strings.Contains(s, "245 tokens in, 89 out") {
		t.Errorf("expected token counts, got: %s", s)
	}
	if !strings.Contains(s, "Cost: $0.003") {
		t.Errorf("expected cost, got: %s", s)
	}
	if !strings.Contains(s, "Latency: 1.2s") {
		t.Errorf("expected latency, got: %s", s)
	}
	if !strings.Contains(s, "Finish: stop") {
		t.Errorf("expected finish reason, got: %s", s)
	}
}

func TestResponseStringWithCache(t *testing.T) {
	cacheRead := 200
	cacheWrite := 50
	resp := Response{
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
		Usage: Usage{
			InputTokens:     300,
			OutputTokens:    100,
			CacheReadTokens:  &cacheRead,
			CacheWriteTokens: &cacheWrite,
		},
		Latency:      500 * time.Millisecond,
		FinishReason: FinishReason{Reason: "stop"},
	}

	s := resp.String()
	if !strings.Contains(s, "(cache: 200 read, 50 write)") {
		t.Errorf("expected cache info, got: %s", s)
	}
}

func TestResponseStringSubSecondLatency(t *testing.T) {
	resp := Response{
		Provider:     "openai",
		Model:        "gpt-4.1",
		Usage:        Usage{InputTokens: 10, OutputTokens: 5},
		Latency:      450 * time.Millisecond,
		FinishReason: FinishReason{Reason: "stop"},
	}

	s := resp.String()
	if !strings.Contains(s, "Latency: 450ms") {
		t.Errorf("expected ms latency, got: %s", s)
	}
}

func TestUsageString(t *testing.T) {
	u := Usage{
		InputTokens:  1000,
		OutputTokens: 500,
		EstimatedCost: 0.045,
	}

	s := u.String()
	if s != "1000 in, 500 out | $0.045" {
		t.Errorf("unexpected usage string: %q", s)
	}
}

func TestUsageStringWithCache(t *testing.T) {
	cacheRead := 800
	u := Usage{
		InputTokens:     1000,
		OutputTokens:    500,
		CacheReadTokens: &cacheRead,
	}

	s := u.String()
	if !strings.Contains(s, "(cache: 800 read)") {
		t.Errorf("expected cache info in usage string: %q", s)
	}
}

func TestUsageStringNoCost(t *testing.T) {
	u := Usage{
		InputTokens:  100,
		OutputTokens: 50,
	}

	s := u.String()
	if strings.Contains(s, "$") {
		t.Errorf("expected no cost in usage string: %q", s)
	}
}
