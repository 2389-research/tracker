// ABOUTME: Tests for BudgetGuard — pipeline-level token, cost, and wall-time ceilings.
// ABOUTME: Verifies each limit dimension fires independently and nil-guard is a no-op.
package pipeline

import (
	"testing"
	"time"
)

func TestBudgetGuard_UnderLimits(t *testing.T) {
	g := NewBudgetGuard(BudgetLimits{MaxTotalTokens: 1000, MaxCostCents: 100, MaxWallTime: time.Minute})
	breach := g.Check(&UsageSummary{TotalTokens: 500, TotalCostUSD: 0.25}, time.Now())
	if breach.Kind != BudgetOK {
		t.Errorf("got breach %v, want BudgetOK", breach.Kind)
	}
}

func TestBudgetGuard_TokenCeiling(t *testing.T) {
	g := NewBudgetGuard(BudgetLimits{MaxTotalTokens: 1000})
	breach := g.Check(&UsageSummary{TotalTokens: 1001}, time.Now())
	if breach.Kind != BudgetTokens {
		t.Errorf("got %v, want BudgetTokens", breach.Kind)
	}
}

func TestBudgetGuard_ExactTokenCeilingInclusive(t *testing.T) {
	g := NewBudgetGuard(BudgetLimits{MaxTotalTokens: 1000})
	breach := g.Check(&UsageSummary{TotalTokens: 1000}, time.Now())
	if breach.Kind != BudgetOK {
		t.Errorf("exact limit should be OK (inclusive), got %v", breach.Kind)
	}
}

func TestBudgetGuard_CostCeiling(t *testing.T) {
	g := NewBudgetGuard(BudgetLimits{MaxCostCents: 50})
	breach := g.Check(&UsageSummary{TotalCostUSD: 0.51}, time.Now())
	if breach.Kind != BudgetCost {
		t.Errorf("got %v, want BudgetCost", breach.Kind)
	}
}

func TestBudgetGuard_WallTime(t *testing.T) {
	g := NewBudgetGuard(BudgetLimits{MaxWallTime: 10 * time.Millisecond})
	started := time.Now().Add(-time.Second)
	breach := g.Check(&UsageSummary{}, started)
	if breach.Kind != BudgetWallTime {
		t.Errorf("got %v, want BudgetWallTime", breach.Kind)
	}
}

func TestBudgetGuard_NilGuardNoOp(t *testing.T) {
	var g *BudgetGuard
	if g.Check(&UsageSummary{TotalTokens: 999_999}, time.Now()).Kind != BudgetOK {
		t.Errorf("nil guard should always return BudgetOK")
	}
}

func TestBudgetGuard_NilLimitsYieldsNilGuard(t *testing.T) {
	g := NewBudgetGuard(BudgetLimits{})
	if g != nil {
		t.Errorf("NewBudgetGuard with zero limits should return nil")
	}
}

func TestBudgetGuard_NilUsageHandled(t *testing.T) {
	g := NewBudgetGuard(BudgetLimits{MaxTotalTokens: 1000, MaxCostCents: 50})
	breach := g.Check(nil, time.Now())
	if breach.Kind != BudgetOK {
		t.Errorf("nil usage should be safe, got %v", breach.Kind)
	}
}
