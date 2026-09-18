// ABOUTME: tracker-runner #900/#901 regression guards on the REAL build_product.dip graph:
// ABOUTME: a milestone-8 pick failure aborts (never Implement / MarkMilestoneDone), and a
// ABOUTME: CONTRACT-TEST-MISSING red from TestMilestone routes to FixMilestone, never the verifier.
package pipeline

import (
	"fmt"
	"testing"
)

// TestBuildProduct900PickFailureAbortsRun is a REGRESSION PIN for the
// routing half of tracker-runner #900 (run_072a9cb7: milestone 8 marked done
// but never built). The root cause of that run is not established; the
// probable mechanism in the runner's v0.73.2 build — an unguarded
// `PickNextMilestone -> Implement when ctx.tool_stdout not contains all-done`
// edge that ran Implement on an exit-1 pick whose extraction had left a
// 0-byte current.md, which MarkMilestoneDone then copied — was closed by
// #640 A1/B5 before this branch, so this test also passes on main. It pins
// that it stays closed at the depth the run reached: milestones 1..7 build
// and are marked done, the 8th pick fails, and the run must end `fail` at
// AbortRun with EXACTLY seven Implement and seven MarkMilestoneDone visits —
// no eighth milestone implemented, no eighth done marker possible.
func TestBuildProduct900PickFailureAbortsRun(t *testing.T) {
	g := loadBuildProduct(t)
	const built = 7
	sim := &bp640Sim{gate: "mark done", script: map[string]func(int) Outcome{
		"PickNextMilestone": func(n int) Outcome {
			if n < built {
				return bpOK(fmt.Sprintf("milestone %d (%d of 9 planned)\ncontract tests: 1 declared (.ai/milestones/contract-tests)\nmilestone-%d", n+1, n+1, n+1))
			}
			return bpFail("milestone 8 (8 of 9 planned)\nERROR: failed to extract milestone 8 from .ai/decisions/milestones.md (empty section, or .ai/milestones/ not writable)\nCheck that milestone headers match: ## Milestone N: ...")
		},
		"TestMilestone": func(int) Outcome { return bpOK("--- contract tests: 1/1 executed ---\ntests-pass") },
	}}
	res, err := sim.run(t, g)
	if err == nil || res == nil || res.Status != OutcomeFail || !sim.visited("AbortRun") {
		t.Fatalf("milestone-8 pick failure must end the run fail at AbortRun: err=%v status=%s visits=%v", err, statusOf(res), sim.visits)
	}
	if got := sim.count("PickNextMilestone"); got != built+1 {
		t.Errorf("PickNextMilestone ran %d times, want %d (7 picks + the failing 8th)", got, built+1)
	}
	if got := sim.count("Implement"); got != built {
		t.Errorf("Implement ran %d times, want exactly %d — milestone 8 must never be implemented after a failed pick", got, built)
	}
	if got := sim.count("MarkMilestoneDone"); got != built {
		t.Errorf("MarkMilestoneDone ran %d times, want exactly %d — a failed pick must never produce an 8th done marker", got, built)
	}
	if last := sim.visits[len(sim.visits)-1]; last != "AbortRun" {
		t.Errorf("last visited = %s, want AbortRun", last)
	}
	for _, id := range []string{"EscalateMilestone", "EscalateReview", "EscalateVerification", "CheckMilestoneOutputs", "Done"} {
		if sim.visited(id) {
			t.Errorf("%s ran after the failed pick: visits=%v", id, sim.visits)
		}
	}
}

// TestBuildProduct901ContractTestMissingRoutesToFix pins tracker-runner
// #901's routing: TestMilestone's CONTRACT-TEST-MISSING red (verify.sh was
// green but a declared contract test never executed) exits 1 with no
// terminal marker, so the `ctx.outcome = fail -> FixMilestone` edge fires —
// never VerifyMilestone (the verifier must not be asked to bless a milestone
// whose contract test does not exist) and never EscalateMilestone on a first
// attempt. After the fix session the re-run is green and the milestone
// proceeds to the verifier.
func TestBuildProduct901ContractTestMissingRoutesToFix(t *testing.T) {
	g := loadBuildProduct(t)
	sim := &bp640Sim{gate: "mark done", script: map[string]func(int) Outcome{
		"PickNextMilestone": oneMilestone(),
		"TestMilestone": func(n int) Outcome {
			if n == 0 {
				return bpFail("=== stack: cargo in . ===\n--- contract tests: 0/1 executed ---\n  MISSING: inspector::test_inspect_contract\nCONTRACT-TEST-MISSING: inspector::test_inspect_contract — declared in the milestone's **Contract tests** but absent from .ai/build/executed-tests.txt (the executed-test manifest). Write the named test so it runs, or correct the declared name to the exact executed one; `sh .ai/build/verify.sh` refreshes the manifest.\n--- attempt 1 of 3 ---")
			}
			return bpOK("--- contract tests: 1/1 executed ---\ntests-pass")
		},
	}}
	res, err := sim.run(t, g)
	if err != nil || res == nil || res.Status != OutcomeSuccess {
		t.Fatalf("run: err=%v status=%s visits=%v", err, statusOf(res), sim.visits)
	}
	if !sim.visited("FixMilestone") {
		t.Fatalf("CONTRACT-TEST-MISSING red must route to FixMilestone: visits=%v", sim.visits)
	}
	// Order: the first TestMilestone is followed by FixMilestone before any
	// VerifyMilestone runs.
	firstTest, firstFix, firstVerify := -1, -1, -1
	for i, v := range sim.visits {
		switch {
		case v == "TestMilestone" && firstTest == -1:
			firstTest = i
		case v == "FixMilestone" && firstFix == -1:
			firstFix = i
		case v == "VerifyMilestone" && firstVerify == -1:
			firstVerify = i
		}
	}
	if !(firstTest < firstFix && firstFix < firstVerify) {
		t.Errorf("want TestMilestone(red) -> FixMilestone -> ... -> VerifyMilestone; indexes test=%d fix=%d verify=%d visits=%v", firstTest, firstFix, firstVerify, sim.visits)
	}
	if sim.visited("EscalateMilestone") {
		t.Errorf("a first contract-test red must not escalate: visits=%v", sim.visits)
	}
	if sim.count("TestMilestone") != 2 || sim.count("MarkMilestoneDone") != 1 {
		t.Errorf("want 2 TestMilestone runs (red, then green) and 1 MarkMilestoneDone: test=%d mark=%d visits=%v", sim.count("TestMilestone"), sim.count("MarkMilestoneDone"), sim.visits)
	}
}
