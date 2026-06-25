// ABOUTME: Pipeline-level token, cost, and wall-time ceilings enforced between nodes.
// ABOUTME: Halts execution with OutcomeBudgetExceeded when any configured limit is breached.
package pipeline

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// BudgetLimits configures hard ceilings for a pipeline run.
// A zero-value field means "no limit" for that dimension.
type BudgetLimits struct {
	MaxTotalTokens int
	MaxCostCents   int
	MaxWallTime    time.Duration
	StallTimeout   time.Duration // abort when no node completes for this wall-clock span
	// SleepAware, when true, excludes detected wall-clock discontinuities (e.g.
	// a suspended laptop) from MaxWallTime and StallTimeout accounting. Opt-in;
	// the zero value preserves today's strict semantics (suspend counted).
	SleepAware bool
}

// IsZero reports whether every limit is unset. SleepAware is intentionally
// excluded: a SleepAware-only config has no ceiling to enforce, so it must
// still yield a nil guard (sleep-awareness without a budget is a no-op).
func (l BudgetLimits) IsZero() bool {
	return l.MaxTotalTokens == 0 && l.MaxCostCents == 0 && l.MaxWallTime == 0 && l.StallTimeout == 0
}

// BudgetBreachKind classifies which limit was hit.
type BudgetBreachKind int

const (
	BudgetOK BudgetBreachKind = iota
	BudgetTokens
	BudgetCost
	BudgetWallTime
	BudgetStall
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
	case BudgetStall:
		return "stall"
	default:
		return "ok"
	}
}

// BudgetBreach describes the outcome of a guard check.
type BudgetBreach struct {
	Kind    BudgetBreachKind
	Message string
}

// clock abstracts the system clock. Now() is the wall clock (timestamps and the
// default-path elapsed/stall accounting). Mono() is a monotonic elapsed reading
// since clock creation: on Linux it is CLOCK_MONOTONIC, which does NOT advance
// during OS suspend but DOES advance during genuine work — the only reliable
// suspend discriminator. Tests inject a clock whose Now() and Mono() advance
// independently to simulate suspend (wall jumps, mono frozen) versus work.
type clock interface {
	Now() time.Time      // wall clock
	Mono() time.Duration // monotonic elapsed since creation; frozen during OS suspend
}

type realClock struct{ start time.Time }

// newRealClock captures a start instant whose monotonic reading anchors Mono().
func newRealClock() *realClock { return &realClock{start: time.Now()} }

func (c *realClock) Now() time.Time { return time.Now() }

// Mono returns monotonic elapsed since construction. time.Since uses the
// monotonic reading embedded in the start time.Time, so OS suspend is excluded
// for free on Linux.
func (c *realClock) Mono() time.Duration { return time.Since(c.start) }

// BudgetGuard evaluates BudgetLimits against a UsageSummary snapshot and a run
// start time. The zero value is not usable; construct via NewBudgetGuard.
type BudgetGuard struct {
	limits     BudgetLimits
	progressAt atomic.Int64 // UnixNano of last node completion (default-path stall)

	clk        clock         // time source; defaults to realClock
	sleepAware bool          // opt-in: drive wall/stall off Mono() so suspend is excluded
	monoStart  time.Duration // Mono() at construction (sleep-aware elapsed baseline)

	// mu guards the sleep-aware accounting fields below: they must move together
	// (a paused window and its accumulated span) for correctness. A slept-laptop
	// guard is not a hot path, so a mutex over atomics is acceptable.
	mu               sync.Mutex
	monoProgress     time.Duration // Mono() at last NotifyProgress (sleep-aware stall)
	pausedMono       time.Duration // Mono() at Pause(); 0 = not paused
	pausedDuration   time.Duration // accumulated explicit awake-idle span (since run start)
	pausedAtProgress time.Duration // pausedDuration snapshot at last NotifyProgress
}

// NewBudgetGuard constructs a BudgetGuard with the given limits. Returns nil
// when limits.IsZero() so callers can use the nil-guard pattern to skip checks
// when no limits are configured.
func NewBudgetGuard(limits BudgetLimits) *BudgetGuard {
	return newBudgetGuardWithClock(limits, newRealClock())
}

// newBudgetGuardWithClock is the clock-injecting constructor used by tests to
// simulate suspend gaps. Production code goes through NewBudgetGuard (realClock).
func newBudgetGuardWithClock(limits BudgetLimits, clk clock) *BudgetGuard {
	if limits.IsZero() {
		return nil
	}
	g := &BudgetGuard{limits: limits, clk: clk, sleepAware: limits.SleepAware}
	g.progressAt.Store(g.clk.Now().UnixNano())
	mono := g.clk.Mono()
	g.monoStart = mono
	g.monoProgress = mono
	return g
}

// NotifyProgress resets the stall clock. Call after each node completes
// (success or fail — both count as forward progress). Safe to call on nil
// and from concurrent goroutines.
func (g *BudgetGuard) NotifyProgress() {
	if g == nil {
		return
	}
	g.progressAt.Store(g.clk.Now().UnixNano())
	if g.sleepAware {
		mono := g.clk.Mono()
		g.mu.Lock()
		g.monoProgress = mono
		g.pausedAtProgress = g.pausedDuration
		g.mu.Unlock()
	}
}

// Pause records the start of an explicit awake-idle window. Resume subtracts the
// bracketed span from sleep-aware wall and stall accounting. This is the opt-in
// API for awake idle the operator does not want counted (e.g. a blocking human
// gate); OS suspend is handled automatically by the monotonic clock and needs no
// Pause. Engine wiring (around blocking human-gate waits) is a follow-up. Safe
// on nil and from concurrent goroutines.
func (g *BudgetGuard) Pause() {
	if g == nil {
		return
	}
	mono := g.clk.Mono()
	g.mu.Lock()
	g.pausedMono = mono
	g.mu.Unlock()
}

// Resume closes the window opened by Pause, folding the bracketed monotonic span
// into the accumulated paused duration. Safe on nil; a Resume without a prior
// Pause is a no-op.
func (g *BudgetGuard) Resume() {
	if g == nil {
		return
	}
	mono := g.clk.Mono()
	g.mu.Lock()
	if g.pausedMono != 0 {
		g.pausedDuration += mono - g.pausedMono
		g.pausedMono = 0
	}
	g.mu.Unlock()
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
	if breach := g.checkWall(started); breach.Kind != BudgetOK {
		return breach
	}
	return g.checkStall(started)
}

// checkWall evaluates MaxWallTime. Default path uses wall elapsed (byte-identical
// to today). Sleep-aware path uses monotonic elapsed minus the explicit paused
// span: monotonic is frozen during OS suspend (so a slept laptop is excluded
// with no threshold) but advances during genuine work (so a long node correctly
// trips).
func (g *BudgetGuard) checkWall(started time.Time) BudgetBreach {
	if g.limits.MaxWallTime <= 0 {
		return BudgetBreach{Kind: BudgetOK}
	}
	var elapsed time.Duration
	if g.sleepAware {
		g.mu.Lock()
		paused := g.pausedDuration
		g.mu.Unlock()
		elapsed = g.clk.Mono() - g.monoStart - paused
	} else {
		elapsed = g.clk.Now().Sub(started)
	}
	if elapsed > g.limits.MaxWallTime {
		return BudgetBreach{Kind: BudgetWallTime, Message: "max_wall_time exceeded"}
	}
	return BudgetBreach{Kind: BudgetOK}
}

// checkStall evaluates StallTimeout against the time since last progress. Default
// path uses wall clock; sleep-aware path uses monotonic-since-progress minus the
// explicit paused span (suspend excluded, genuine hangs still trip).
func (g *BudgetGuard) checkStall(started time.Time) BudgetBreach {
	if g.limits.StallTimeout <= 0 {
		return BudgetBreach{Kind: BudgetOK}
	}
	var stall time.Duration
	if g.sleepAware {
		g.mu.Lock()
		sinceProgress := g.clk.Mono() - g.monoProgress
		pausedSinceProgress := g.pausedDuration - g.pausedAtProgress
		g.mu.Unlock()
		stall = sinceProgress - pausedSinceProgress
	} else {
		now := g.clk.Now()
		// Clamp to started so pre-run idle time (between NewBudgetGuard and Run())
		// does not consume the stall window before the first node fires.
		lastProgress := time.Unix(0, g.progressAt.Load())
		if lastProgress.Before(started) {
			lastProgress = started
		}
		stall = now.Sub(lastProgress)
	}
	if stall > g.limits.StallTimeout {
		return BudgetBreach{Kind: BudgetStall, Message: "stall_timeout exceeded"}
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
	if g.limits.MaxCostCents > 0 && int(math.Round(usage.TotalCostUSD*100)) > g.limits.MaxCostCents {
		return BudgetBreach{Kind: BudgetCost, Message: "max_cost_cents exceeded"}
	}
	return BudgetBreach{Kind: BudgetOK}
}
