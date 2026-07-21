// ABOUTME: Tests the Slack Block Kit rendering of the live status card.
package main

import (
	"strings"
	"testing"
	"time"
)

func TestProgressBar(t *testing.T) {
	if got := progressBar(0, 4); got != strings.Repeat("░", 12) {
		t.Fatalf("empty bar = %q", got)
	}
	if got := progressBar(4, 4); got != strings.Repeat("▓", 12) {
		t.Fatalf("full bar = %q", got)
	}
	if got := progressBar(2, 4); strings.Count(got, "▓") != 6 {
		t.Fatalf("half bar = %q (want 6 filled)", got)
	}
	if got := progressBar(1, 0); len(got) == 0 { // no divide-by-zero
		t.Fatalf("zero total should still render, got %q", got)
	}
}

func TestFmtDuration(t *testing.T) {
	if got := fmtDuration(45 * time.Second); got != "45s" {
		t.Fatalf("45s = %q", got)
	}
	if got := fmtDuration(125 * time.Second); got != "2m 05s" {
		t.Fatalf("125s = %q", got)
	}
}

func TestStatusBlocks_Renders(t *testing.T) {
	blocks := statusBlocks(StatusCard{
		Workflow: "build_product", State: "running",
		DoneCount: 2, TotalCount: 5, CurrentNode: "Implement",
		CostUSD: 1.87, BudgetUSD: 5.00, Elapsed: 90 * time.Second,
	})
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks (header, progress, meta), got %d", len(blocks))
	}
}
