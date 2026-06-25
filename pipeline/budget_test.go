// ABOUTME: Tests for BudgetGuard — pipeline-level token, cost, and wall-time ceilings.
// ABOUTME: Verifies each limit dimension fires independently and nil-guard is a no-op.
package pipeline

import (
	"math"
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

func TestBudgetGuard_CostCeiling_RoundsBoundaryValue(t *testing.T) {
	// The largest float64 strictly less than 0.51, when scaled by 100, yields
	// 50.9999... which naive truncation would read as 50¢ (missing the breach
	// on a 50¢ ceiling). Math.Round correctly reads it as 51¢.
	justBelow := math.Nextafter(0.51, 0)
	g := NewBudgetGuard(BudgetLimits{MaxCostCents: 50})
	breach := g.Check(&UsageSummary{TotalCostUSD: justBelow}, time.Now())
	if breach.Kind != BudgetCost {
		t.Errorf("cost %.20f should breach 50¢ ceiling after rounding, got %v", justBelow, breach.Kind)
	}
}

func TestBudgetGuard_StallTimeout(t *testing.T) {
	g := NewBudgetGuard(BudgetLimits{StallTimeout: 10 * time.Millisecond})
	now := time.Now()
	// progressAt is 1s ago; started is 2s ago so clamping doesn't mask the stall.
	g.progressAt.Store(now.Add(-time.Second).UnixNano())
	breach := g.Check(nil, now.Add(-2*time.Second))
	if breach.Kind != BudgetStall {
		t.Errorf("got %v, want BudgetStall", breach.Kind)
	}
}

func TestBudgetGuard_NotifyProgress_ResetsStallClock(t *testing.T) {
	// Use a generous timeout so the test is not flaky under scheduler pressure.
	g := NewBudgetGuard(BudgetLimits{StallTimeout: time.Minute})
	now := time.Now()
	// Push progressAt into the past so it would stall without a reset.
	g.progressAt.Store(now.Add(-2 * time.Minute).UnixNano())
	// After NotifyProgress the timer is reset — no breach.
	g.NotifyProgress()
	breach := g.Check(nil, now.Add(-3*time.Minute))
	if breach.Kind != BudgetOK {
		t.Errorf("got %v after NotifyProgress, want BudgetOK", breach.Kind)
	}
}

func TestBudgetGuard_NotifyProgress_NilSafe(t *testing.T) {
	var g *BudgetGuard
	g.NotifyProgress() // must not panic
}

func TestBudgetGuard_StallTimeout_OnlyLimitIsStall(t *testing.T) {
	// A guard built with only StallTimeout should not be nil.
	g := NewBudgetGuard(BudgetLimits{StallTimeout: time.Minute})
	if g == nil {
		t.Fatal("NewBudgetGuard with StallTimeout should not return nil")
	}
}

// fakeClock is an injectable clock whose wall and monotonic readings advance
// INDEPENDENTLY. advanceWall() simulates an OS suspend (wall jumps, monotonic
// frozen — as CLOCK_MONOTONIC behaves on Linux). advanceWork() simulates
// genuine elapsed work (both advance together). This independence is what makes
// suspend-vs-work distinguishable and testable.
type fakeClock struct {
	wall time.Time
	mono time.Duration
}

func (f *fakeClock) Now() time.Time      { return f.wall }
func (f *fakeClock) Mono() time.Duration { return f.mono }

// advanceWork advances both wall and monotonic clocks — genuine elapsed time.
func (f *fakeClock) advanceWork(d time.Duration) {
	f.wall = f.wall.Add(d)
	f.mono += d
}

// advanceWall advances only the wall clock — an OS suspend, during which
// CLOCK_MONOTONIC does not tick.
func (f *fakeClock) advanceWall(d time.Duration) { f.wall = f.wall.Add(d) }

func newFakeClock() *fakeClock { return &fakeClock{wall: time.Unix(1_000_000, 0)} }

// T_SuspendExcludedWall: sleep-aware, MaxWallTime=1h; monotonic advances 10m
// while wall jumps 17h (suspend) -> BudgetOK (suspend excluded by Mono freeze).
func TestBudgetGuard_SleepAware_WallExcludesSuspend(t *testing.T) {
	fc := newFakeClock()
	started := fc.Now()
	g := newBudgetGuardWithClock(BudgetLimits{MaxWallTime: time.Hour, SleepAware: true}, fc)
	g.Check(nil, started) // anchor monotonic baseline at run start (F1)

	fc.advanceWork(10 * time.Minute)
	if b := g.Check(nil, started); b.Kind != BudgetOK {
		t.Fatalf("after 10m work: got %v, want BudgetOK", b.Kind)
	}
	// 17h suspend: wall jumps but monotonic is frozen — must be excluded.
	fc.advanceWall(17 * time.Hour)
	if b := g.Check(nil, started); b.Kind != BudgetOK {
		t.Fatalf("after 17h suspend: got %v, want BudgetOK (suspend excluded)", b.Kind)
	}
}

// T_GenuineLongWorkStillTripsWall: sleep-aware, MaxWallTime=1h; monotonic
// advances 70m of genuine work (the blocker — a long node must NOT be
// misclassified as suspend) -> BudgetWallTime. MUST trip.
func TestBudgetGuard_SleepAware_GenuineLongWorkTripsWall(t *testing.T) {
	fc := newFakeClock()
	started := fc.Now()
	g := newBudgetGuardWithClock(BudgetLimits{MaxWallTime: time.Hour, SleepAware: true}, fc)
	g.Check(nil, started) // anchor monotonic baseline at run start (F1)

	// One long node: 70m of genuine work with no intervening Check (Check runs
	// only at node boundaries). Monotonic advances the full 70m.
	fc.advanceWork(70 * time.Minute)
	if b := g.Check(nil, started); b.Kind != BudgetWallTime {
		t.Fatalf("after 70m genuine work: got %v, want BudgetWallTime", b.Kind)
	}
}

// T_GenuineLongWorkMultiSpan: same as above but several >5m node spans with a
// Check between each — the old >5m inter-Check threshold would have excluded
// every span; monotonic accounting correctly accrues them.
func TestBudgetGuard_SleepAware_GenuineLongWorkMultiSpanTripsWall(t *testing.T) {
	fc := newFakeClock()
	started := fc.Now()
	g := newBudgetGuardWithClock(BudgetLimits{MaxWallTime: time.Hour, SleepAware: true}, fc)
	g.Check(nil, started) // anchor monotonic baseline at run start (F1)

	tripped := false
	for i := 0; i < 10; i++ {
		fc.advanceWork(8 * time.Minute) // each node runs 8m (> old 5m threshold)
		if b := g.Check(nil, started); b.Kind == BudgetWallTime {
			tripped = true
			break
		}
	}
	if !tripped {
		t.Fatalf("80m of genuine 8m-node work never tripped MaxWallTime=1h")
	}
}

// T_GenuineHangStillTripsStall: sleep-aware, StallTimeout=30m; monotonic
// advances 90m with NO NotifyProgress (a genuine hang) -> BudgetStall. MUST
// trip — catching this hang is StallTimeout's whole purpose.
func TestBudgetGuard_SleepAware_GenuineHangTripsStall(t *testing.T) {
	fc := newFakeClock()
	started := fc.Now()
	g := newBudgetGuardWithClock(BudgetLimits{StallTimeout: 30 * time.Minute, SleepAware: true}, fc)
	g.NotifyProgress()
	g.Check(nil, started) // anchor monotonic baseline at run start (F1)

	fc.advanceWork(90 * time.Minute) // genuine no-progress hang
	if b := g.Check(nil, started); b.Kind != BudgetStall {
		t.Fatalf("after 90m genuine hang: got %v, want BudgetStall", b.Kind)
	}
}

// T_SuspendExcludedStall: sleep-aware, StallTimeout=30m; after NotifyProgress
// the wall jumps 17h but monotonic advances ~0 (suspend) -> BudgetOK.
func TestBudgetGuard_SleepAware_StallExcludesSuspend(t *testing.T) {
	fc := newFakeClock()
	started := fc.Now()
	g := newBudgetGuardWithClock(BudgetLimits{StallTimeout: 30 * time.Minute, SleepAware: true}, fc)
	g.NotifyProgress() // last progress = now (mono)

	fc.advanceWall(17 * time.Hour) // suspend: wall jumps, mono frozen
	if b := g.Check(nil, started); b.Kind != BudgetOK {
		t.Fatalf("sleep-aware stall after 17h suspend: got %v, want BudgetOK", b.Kind)
	}
}

// AC1 control: same suspend with sleep-awareness OFF must trip wall (proves
// the strict default still fires and the exclusion is load-bearing).
func TestBudgetGuard_SleepUnaware_WallTripsOnSuspend(t *testing.T) {
	fc := newFakeClock()
	started := fc.Now()
	g := newBudgetGuardWithClock(BudgetLimits{MaxWallTime: time.Hour}, fc)

	fc.advanceWall(17 * time.Hour)
	if b := g.Check(nil, started); b.Kind != BudgetWallTime {
		t.Fatalf("sleep-unaware after 17h: got %v, want BudgetWallTime", b.Kind)
	}
}

func TestBudgetGuard_SleepUnaware_StallTripsOnSuspend(t *testing.T) {
	fc := newFakeClock()
	started := fc.Now()
	g := newBudgetGuardWithClock(BudgetLimits{StallTimeout: 30 * time.Minute}, fc)
	g.NotifyProgress()

	fc.advanceWall(17 * time.Hour)
	if b := g.Check(nil, started); b.Kind != BudgetStall {
		t.Fatalf("sleep-unaware stall after 17h: got %v, want BudgetStall", b.Kind)
	}
}

// T_PauseResumeSubtractsSpan (AC2): an explicit Pause then Resume across a span
// of genuine awake-idle work subtracts that span from wall and stall accounting.
func TestBudgetGuard_PauseResume_SubtractsSpan(t *testing.T) {
	fc := newFakeClock()
	started := fc.Now()
	g := newBudgetGuardWithClock(BudgetLimits{MaxWallTime: time.Hour, SleepAware: true}, fc)
	g.Check(nil, started) // anchor monotonic baseline at run start (F1)

	fc.advanceWork(50 * time.Minute)
	g.Pause()
	fc.advanceWork(17 * time.Minute) // explicit awake-idle span (mono advances)
	g.Resume()
	fc.advanceWork(5 * time.Minute) // effective elapsed = 55m
	if b := g.Check(nil, started); b.Kind != BudgetOK {
		t.Fatalf("effective 55m: got %v, want BudgetOK", b.Kind)
	}
	fc.advanceWork(4 * time.Minute) // effective elapsed = 59m
	if b := g.Check(nil, started); b.Kind != BudgetOK {
		t.Fatalf("effective 59m: got %v, want BudgetOK", b.Kind)
	}
	fc.advanceWork(4 * time.Minute) // effective elapsed = 63m
	if b := g.Check(nil, started); b.Kind != BudgetWallTime {
		t.Fatalf("effective 63m: got %v, want BudgetWallTime", b.Kind)
	}
}

func TestBudgetGuard_PauseResume_NilSafe(t *testing.T) {
	var g *BudgetGuard
	g.Pause()  // must not panic
	g.Resume() // must not panic
}

// T_DefaultOffUnchanged (AC4): default constructor (no SleepAware) behaves
// exactly as today — a genuine elapsed beyond MaxWallTime still trips.
func TestBudgetGuard_DefaultOff_WallTripsOnElapsed(t *testing.T) {
	fc := newFakeClock()
	started := fc.Now()
	g := newBudgetGuardWithClock(BudgetLimits{MaxWallTime: time.Hour}, fc)

	fc.advanceWork(10 * time.Minute)
	if b := g.Check(nil, started); b.Kind != BudgetOK {
		t.Fatalf("after 10m: got %v, want BudgetOK", b.Kind)
	}
	fc.advanceWork(time.Hour) // genuine 70m elapsed
	if b := g.Check(nil, started); b.Kind != BudgetWallTime {
		t.Fatalf("after 70m genuine elapsed: got %v, want BudgetWallTime", b.Kind)
	}
}

// F1: a guard constructed BEFORE the run starts must not charge pre-run AWAKE
// idle against MaxWallTime — the monotonic baseline anchors at the first Check
// (run start), so 50m of awake idle before the run does not consume the hour.
func TestBudgetGuard_SleepAware_PreRunIdleNotChargedToWall(t *testing.T) {
	fc := newFakeClock()
	g := newBudgetGuardWithClock(BudgetLimits{MaxWallTime: time.Hour, SleepAware: true}, fc)

	// Construction-to-run-start gap: 50m of awake idle (mono advances) before the
	// run begins. The engine's first Check marks run start here.
	fc.advanceWork(50 * time.Minute)
	started := fc.Now()
	if b := g.Check(nil, started); b.Kind != BudgetOK {
		t.Fatalf("at run start with 50m pre-run idle: got %v, want BudgetOK", b.Kind)
	}
	// 30m of genuine in-run work — total since run start is 30m, well under 1h.
	fc.advanceWork(30 * time.Minute)
	if b := g.Check(nil, started); b.Kind != BudgetOK {
		t.Fatalf("30m in-run (80m since construction): got %v, want BudgetOK (pre-run idle excluded)", b.Kind)
	}
}

// F1: pre-run AWAKE idle must not consume the initial StallTimeout either — the
// stall baseline clamps to the run-start anchor.
func TestBudgetGuard_SleepAware_PreRunIdleNotChargedToStall(t *testing.T) {
	fc := newFakeClock()
	g := newBudgetGuardWithClock(BudgetLimits{StallTimeout: 30 * time.Minute, SleepAware: true}, fc)

	fc.advanceWork(50 * time.Minute) // pre-run awake idle, no NotifyProgress yet
	started := fc.Now()
	if b := g.Check(nil, started); b.Kind != BudgetOK {
		t.Fatalf("at run start with 50m pre-run idle: got %v, want BudgetOK (stall clamped to run start)", b.Kind)
	}
	fc.advanceWork(10 * time.Minute) // 10m since run start, under 30m
	if b := g.Check(nil, started); b.Kind != BudgetOK {
		t.Fatalf("10m in-run: got %v, want BudgetOK", b.Kind)
	}
}

// F2: Pause is idempotent. A double Pause before a single Resume must subtract
// the bracketed span exactly once (not zero, not double). pausedMono is recorded
// only on the first Pause; the second is a no-op.
func TestBudgetGuard_PauseResume_DoublePauseIdempotent(t *testing.T) {
	fc := newFakeClock()
	g := newBudgetGuardWithClock(BudgetLimits{MaxWallTime: time.Hour, SleepAware: true}, fc)
	g.Check(nil, fc.Now()) // anchor at run start

	fc.advanceWork(40 * time.Minute)
	g.Pause()                        // pause baseline = mono at 40m
	fc.advanceWork(2 * time.Minute)  // a second Pause arrives mid-window
	g.Pause()                        // must NOT overwrite the 40m baseline (F2)
	fc.advanceWork(28 * time.Minute) // total paused span = 30m (40m -> 70m)
	g.Resume()
	// Effective elapsed = 70m mono - 30m paused = 40m. Under 1h.
	fc.advanceWork(15 * time.Minute) // effective 55m
	if b := g.Check(nil, fc.Now()); b.Kind != BudgetOK {
		t.Fatalf("effective 55m after idempotent double-pause: got %v, want BudgetOK", b.Kind)
	}
	// Now advance 10m: correct accounting (30m subtracted once) -> effective 65m,
	// which must TRIP. The F2 bug (second Pause overwrites baseline at 42m, so
	// only 28m is subtracted) -> effective 67m, which also trips — so a trip here
	// does not distinguish. The distinguishing observation is the boundary BELOW:
	// with the span subtracted exactly once, effective 59m must still be OK; the
	// over-subtraction bug (if Pause had instead added spans) would read lower.
	fc.advanceWork(4 * time.Minute) // correct effective 59m, still under 1h
	if b := g.Check(nil, fc.Now()); b.Kind != BudgetOK {
		t.Fatalf("effective 59m: got %v, want BudgetOK (span subtracted exactly once)", b.Kind)
	}
	fc.advanceWork(2 * time.Minute) // correct effective 61m -> must trip
	if b := g.Check(nil, fc.Now()); b.Kind != BudgetWallTime {
		t.Fatalf("effective 61m: got %v, want BudgetWallTime", b.Kind)
	}
}

// F3: while currently paused (Pause without Resume), the in-flight pause window
// must be subtracted from wall accounting — MaxWallTime must NOT trip mid-pause
// even when the paused span alone exceeds the limit.
func TestBudgetGuard_PauseResume_WallNotTrippedWhilePaused(t *testing.T) {
	fc := newFakeClock()
	g := newBudgetGuardWithClock(BudgetLimits{MaxWallTime: time.Hour, SleepAware: true}, fc)
	g.Check(nil, fc.Now()) // anchor at run start

	fc.advanceWork(10 * time.Minute)
	g.Pause()
	fc.advanceWork(2 * time.Hour) // long awake idle while paused — exceeds 1h alone
	// Still paused (no Resume): the in-flight span must be excluded (F3).
	if b := g.Check(nil, fc.Now()); b.Kind != BudgetOK {
		t.Fatalf("mid-pause wall: got %v, want BudgetOK (in-flight pause span not subtracted, F3)", b.Kind)
	}
}

// F4: while currently paused, the in-flight pause window must be subtracted from
// stall accounting — StallTimeout must NOT trip mid-pause even when the paused
// span alone exceeds the timeout.
func TestBudgetGuard_PauseResume_StallNotTrippedWhilePaused(t *testing.T) {
	fc := newFakeClock()
	g := newBudgetGuardWithClock(BudgetLimits{StallTimeout: 30 * time.Minute, SleepAware: true}, fc)
	g.NotifyProgress()
	g.Check(nil, fc.Now()) // anchor at run start

	fc.advanceWork(5 * time.Minute)
	g.Pause()
	fc.advanceWork(2 * time.Hour) // long awake idle while paused — exceeds 30m alone
	if b := g.Check(nil, fc.Now()); b.Kind != BudgetOK {
		t.Fatalf("mid-pause stall: got %v, want BudgetOK (in-flight pause span not subtracted, F4)", b.Kind)
	}
}
