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

	clk        clock // time source; defaults to realClock
	sleepAware bool  // opt-in: drive wall/stall off Mono() so suspend is excluded

	// mu guards the sleep-aware accounting fields below: they must move together
	// (a paused window and its accumulated span) for correctness. A slept-laptop
	// guard is not a hot path, so a mutex over atomics is acceptable.
	mu sync.Mutex
	// monoAnchor is the monotonic baseline for sleep-aware wall/stall accounting.
	// The engine anchors it at TRUE run start via AnchorRunStart — before the
	// first node executes — so the first node's runtime is counted (Check runs
	// only at node boundaries, so anchoring on the first Check would exclude the
	// entire first node). It is NOT anchored at construction: a guard built
	// before the run starts must not charge pre-run AWAKE idle against
	// MaxWallTime or the initial StallTimeout (this mirrors the default path,
	// which measures from `started`). For direct-Check callers that never call
	// AnchorRunStart (unit tests, embedded uses), the first Check anchors lazily
	// as a fallback.
	monoAnchor       time.Duration
	monoAnchored     bool          // monoAnchor has been set (by AnchorRunStart or the first Check)
	monoProgress     time.Duration // Mono() at last NotifyProgress (sleep-aware stall)
	pausedMono       time.Duration // Mono() at Pause(); -1 = not paused
	pausedDuration   time.Duration // accumulated explicit awake-idle span (since run start)
	pausedAtProgress time.Duration // pausedDuration snapshot at last NotifyProgress
}

// pauseSentinelNone marks "not currently paused". A zero value is a legitimate
// Mono() reading at run start, so 0 cannot mean "not paused"; use -1.
const pauseSentinelNone time.Duration = -1

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
	g.pausedMono = pauseSentinelNone
	// monoAnchor/monoProgress are left unset here and anchored at TRUE run start
	// by AnchorRunStart (or lazily on the first Check as a fallback), so pre-run
	// AWAKE idle is excluded (F1) while the first node's runtime is still counted.
	return g
}

// AnchorRunStart fixes the sleep-aware monotonic baseline at the TRUE run start
// — before the first node executes. The engine calls this once, right after the
// run begins, so the first node's runtime is charged against MaxWallTime and the
// initial StallTimeout. BudgetGuard.Check runs only at node boundaries (after a
// node completes), so relying on the first Check to anchor would silently exclude
// the entire first node's runtime (#426 review). Idempotent and safe on nil;
// no-op off the sleep-aware path.
func (g *BudgetGuard) AnchorRunStart() {
	if g == nil || !g.sleepAware {
		return
	}
	g.anchorMono()
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
	// Idempotent (F2): only record the pause baseline when not already paused, so a
	// double Pause does not overwrite (and under-count) the in-flight span.
	if g.pausedMono == pauseSentinelNone {
		g.pausedMono = mono
	}
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
	if g.pausedMono != pauseSentinelNone {
		g.pausedDuration += mono - g.pausedMono
		g.pausedMono = pauseSentinelNone
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
	if g.sleepAware {
		g.anchorMono()
	}
	if breach := g.checkUsage(usage); breach.Kind != BudgetOK {
		return breach
	}
	if breach := g.checkWall(started); breach.Kind != BudgetOK {
		return breach
	}
	return g.checkStall(started)
}

// anchorMono fixes the sleep-aware monotonic baseline once, on whichever comes
// first: AnchorRunStart (the engine's TRUE run start, before the first node) or
// the first Check (the fallback for direct-Check callers). Pre-run AWAKE idle
// between NewBudgetGuard and the anchor is excluded (F1). Idempotent; called only
// on the sleep-aware path.
func (g *BudgetGuard) anchorMono() {
	mono := g.clk.Mono()
	g.mu.Lock()
	if !g.monoAnchored {
		g.monoAnchor = mono
		g.monoAnchored = true
		// If no NotifyProgress has fired yet, anchor the stall baseline here too.
		// A NotifyProgress that already ran records its own (clamped at read time).
		if g.monoProgress < mono {
			g.monoProgress = mono
		}
	}
	g.mu.Unlock()
}

// pausedSpanLocked returns the accumulated paused duration plus any in-flight
// pause window (Pause without a matching Resume). Callers must hold g.mu.
func (g *BudgetGuard) pausedSpanLocked(now time.Duration) time.Duration {
	paused := g.pausedDuration
	if g.pausedMono != pauseSentinelNone {
		paused += now - g.pausedMono
	}
	return paused
}

// checkWall evaluates MaxWallTime. Default path uses wall elapsed (byte-identical
// to today). Sleep-aware path uses monotonic elapsed (from the run-start anchor)
// minus the paused span — including any in-flight pause window (F3): monotonic is
// frozen during OS suspend (so a slept laptop is excluded with no threshold) but
// advances during genuine work (so a long node correctly trips).
func (g *BudgetGuard) checkWall(started time.Time) BudgetBreach {
	if g.limits.MaxWallTime <= 0 {
		return BudgetBreach{Kind: BudgetOK}
	}
	var elapsed time.Duration
	if g.sleepAware {
		now := g.clk.Mono()
		g.mu.Lock()
		paused := g.pausedSpanLocked(now)
		anchor := g.monoAnchor
		g.mu.Unlock()
		elapsed = now - anchor - paused
		if elapsed < 0 {
			elapsed = 0
		}
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
// paused span — including any in-flight pause window (F4), and clamping the
// progress baseline to the run-start anchor so pre-run idle cannot consume the
// stall window (the default path's "clamp to started" semantics).
func (g *BudgetGuard) checkStall(started time.Time) BudgetBreach {
	if g.limits.StallTimeout <= 0 {
		return BudgetBreach{Kind: BudgetOK}
	}
	var stall time.Duration
	if g.sleepAware {
		stall = g.sleepAwareStall()
	} else {
		stall = g.defaultStall(started)
	}
	if stall > g.limits.StallTimeout {
		return BudgetBreach{Kind: BudgetStall, Message: "stall_timeout exceeded"}
	}
	return BudgetBreach{Kind: BudgetOK}
}

// sleepAwareStall computes monotonic-since-progress minus the paused span
// (including any in-flight pause window, F4), clamping the progress baseline to
// the run-start anchor so pre-run idle cannot consume the stall window.
func (g *BudgetGuard) sleepAwareStall() time.Duration {
	now := g.clk.Mono()
	g.mu.Lock()
	// Clamp the progress baseline to the run-start anchor: progress recorded
	// before run start (NotifyProgress called pre-Check) must not let pre-run
	// idle accrue against the stall window.
	lastProgress := g.monoProgress
	if lastProgress < g.monoAnchor {
		lastProgress = g.monoAnchor
	}
	pausedSinceProgress := g.pausedSpanLocked(now) - g.pausedAtProgress
	g.mu.Unlock()
	stall := now - lastProgress - pausedSinceProgress
	if stall < 0 {
		stall = 0
	}
	return stall
}

// defaultStall computes wall-clock since last progress, clamped to the run start
// (byte-identical to the original default path).
func (g *BudgetGuard) defaultStall(started time.Time) time.Duration {
	now := g.clk.Now()
	// Clamp to started so pre-run idle time (between NewBudgetGuard and Run())
	// does not consume the stall window before the first node fires.
	lastProgress := time.Unix(0, g.progressAt.Load())
	if lastProgress.Before(started) {
		lastProgress = started
	}
	return now.Sub(lastProgress)
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
