// ABOUTME: Tests for the transcript collector and buildSessionStats helper.
// ABOUTME: Validates that agent session metrics including token usage are correctly mapped to pipeline stats.
package handlers

import (
	"testing"
	"time"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
)

func TestBuildSessionStatsIncludesTokenUsage(t *testing.T) {
	r := agent.SessionResult{
		SessionID:          "test-session",
		Duration:           5 * time.Second,
		Turns:              3,
		ToolCalls:          map[string]int{"bash": 10, "write": 5},
		FilesModified:      []string{"main.go"},
		FilesCreated:       []string{"new.go"},
		CompactionsApplied: 1,
		LongestTurn:        2 * time.Second,
		ToolCacheHits:      8,
		ToolCacheMisses:    3,
		Usage: llm.Usage{
			InputTokens:   4200,
			OutputTokens:  1800,
			TotalTokens:   6000,
			EstimatedCost: 0.075,
		},
	}

	stats := buildSessionStats(r)

	// Verify existing fields still work.
	if stats.Turns != 3 {
		t.Errorf("expected Turns=3, got %d", stats.Turns)
	}
	if stats.TotalToolCalls != 15 {
		t.Errorf("expected TotalToolCalls=15, got %d", stats.TotalToolCalls)
	}
	if stats.CacheHits != 8 {
		t.Errorf("expected CacheHits=8, got %d", stats.CacheHits)
	}

	// Verify token/cost fields.
	if stats.InputTokens != 4200 {
		t.Errorf("expected InputTokens=4200, got %d", stats.InputTokens)
	}
	if stats.OutputTokens != 1800 {
		t.Errorf("expected OutputTokens=1800, got %d", stats.OutputTokens)
	}
	if stats.TotalTokens != 6000 {
		t.Errorf("expected TotalTokens=6000, got %d", stats.TotalTokens)
	}
	if stats.CostUSD != 0.075 {
		t.Errorf("expected CostUSD=0.075, got %f", stats.CostUSD)
	}
}

func TestBuildSessionStatsZeroUsage(t *testing.T) {
	r := agent.SessionResult{
		Turns:     1,
		ToolCalls: map[string]int{"bash": 1},
		Usage:     llm.Usage{}, // zero-value usage
	}

	stats := buildSessionStats(r)

	if stats.InputTokens != 0 {
		t.Errorf("expected InputTokens=0, got %d", stats.InputTokens)
	}
	if stats.OutputTokens != 0 {
		t.Errorf("expected OutputTokens=0, got %d", stats.OutputTokens)
	}
	if stats.TotalTokens != 0 {
		t.Errorf("expected TotalTokens=0, got %d", stats.TotalTokens)
	}
	if stats.CostUSD != 0 {
		t.Errorf("expected CostUSD=0, got %f", stats.CostUSD)
	}
}
