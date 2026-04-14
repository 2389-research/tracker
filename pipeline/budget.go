// ABOUTME: Pipeline-level token, cost, and wall-time ceilings enforced between nodes.
// ABOUTME: Halts execution with OutcomeBudgetExceeded when any configured limit is breached.
package pipeline

import "time"

// BudgetLimits configures hard ceilings for a pipeline run.
// A zero-value field means "no limit" for that dimension.
type BudgetLimits struct {
	MaxTotalTokens int
	MaxCostCents   int
	MaxWallTime    time.Duration
}

// IsZero reports whether every limit is unset.
func (l BudgetLimits) IsZero() bool {
	return l.MaxTotalTokens == 0 && l.MaxCostCents == 0 && l.MaxWallTime == 0
}

// BudgetBreachKind classifies which limit was hit.
type BudgetBreachKind int

const (
	BudgetOK BudgetBreachKind = iota
	BudgetTokens
	BudgetCost
	BudgetWallTime
)

// String returns a human-readable label for a breach kind.
// Used as the halt reason in EngineResult.BudgetLimitsHit.
func (k BudgetBreachKind) String() string {
	switch k {
	case BudgetTokens:
		return "tokens"
	case BudgetCost:
		return "cost"
	case BudgetWallTime:
		return "wall_time"
	default:
		return "ok"
	}
}

// BudgetBreach describes the outcome of a guard check.
type BudgetBreach struct {
	Kind    BudgetBreachKind
	Message string
}

// BudgetGuard evaluates BudgetLimits against a UsageSummary snapshot and a run
// start time. The zero value is not usable; construct via NewBudgetGuard.
type BudgetGuard struct {
	limits BudgetLimits
}

// NewBudgetGuard constructs a BudgetGuard with the given limits. Returns nil
// when limits.IsZero() so callers can use the nil-guard pattern to skip checks
// when no limits are configured.
func NewBudgetGuard(limits BudgetLimits) *BudgetGuard {
	if limits.IsZero() {
		return nil
	}
	return &BudgetGuard{limits: limits}
}

// Check reports whether the given usage snapshot breaches any configured
// limit. A nil guard and a nil usage are both safe and return BudgetOK.
// Token and cost ceilings are inclusive — the exact limit is not a breach.
func (g *BudgetGuard) Check(usage *UsageSummary, started time.Time) BudgetBreach {
	if g == nil {
		return BudgetBreach{Kind: BudgetOK}
	}
	if breach := g.checkUsage(usage); breach.Kind != BudgetOK {
		return breach
	}
	if g.limits.MaxWallTime > 0 && time.Since(started) > g.limits.MaxWallTime {
		return BudgetBreach{Kind: BudgetWallTime, Message: "max_wall_time exceeded"}
	}
	return BudgetBreach{Kind: BudgetOK}
}

// checkUsage evaluates token and cost limits against a usage snapshot.
// Returns BudgetOK when usage is nil or no limit is breached.
func (g *BudgetGuard) checkUsage(usage *UsageSummary) BudgetBreach {
	if usage == nil {
		return BudgetBreach{Kind: BudgetOK}
	}
	if g.limits.MaxTotalTokens > 0 && usage.TotalTokens > g.limits.MaxTotalTokens {
		return BudgetBreach{Kind: BudgetTokens, Message: "max_total_tokens exceeded"}
	}
	if g.limits.MaxCostCents > 0 && int(usage.TotalCostUSD*100) > g.limits.MaxCostCents {
		return BudgetBreach{Kind: BudgetCost, Message: "max_cost_cents exceeded"}
	}
	return BudgetBreach{Kind: BudgetOK}
}
