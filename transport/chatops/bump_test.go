// ABOUTME: Tests budget-bump recovery — the breach nudge, the ceiling suggestion,
// ABOUTME: and command-argument parsing for `bump <dollars>`.
package chatops

import (
	"strings"
	"testing"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline"
)

func TestRecoveryHint_BudgetBreachOffersBump(t *testing.T) {
	res := &tracker.Result{
		Status: string(pipeline.OutcomeBudgetExceeded),
		Cost:   &tracker.CostReport{TotalUSD: 5.02},
	}
	got := recoveryHint(res)
	if !strings.Contains(got, "bump") {
		t.Errorf("budget breach should nudge `bump`, got %q", got)
	}
	if strings.Contains(got, "retry") {
		t.Errorf("budget breach should not offer plain retry, got %q", got)
	}
}

func TestRecoveryHint_PlainFailureOffersRetry(t *testing.T) {
	got := recoveryHint(&tracker.Result{Status: string(pipeline.OutcomeFail)})
	if !strings.Contains(got, "retry") {
		t.Errorf("plain failure should offer retry, got %q", got)
	}
	if strings.Contains(got, "bump") {
		t.Errorf("plain failure should not mention bump, got %q", got)
	}
}

func TestSuggestBumpDollars(t *testing.T) {
	cases := []struct {
		spent float64
		want  int
	}{
		{spent: 5.02, want: 11}, // ~2× spend, rounded up
		{spent: 0.10, want: 2},  // tiny run floored at $2
		{spent: 0, want: 2},     // no cost data → floor
	}
	for _, tc := range cases {
		got := suggestBumpDollars(&tracker.Result{Cost: &tracker.CostReport{TotalUSD: tc.spent}})
		if got != tc.want {
			t.Errorf("suggestBumpDollars($%.2f)=%d, want %d", tc.spent, got, tc.want)
		}
	}
	if got := suggestBumpDollars(&tracker.Result{}); got != 2 {
		t.Errorf("nil Cost should floor at $2, got %d", got)
	}
}

func TestCommandArg(t *testing.T) {
	cases := []struct {
		cmd, verb, wantArg string
		wantOK             bool
	}{
		{"bump 10", "bump", "10", true},
		{"BUMP $12", "bump", "$12", true},
		{"bump", "bump", "", true}, // bare verb → empty arg, still matched
		{"status", "bump", "", false},
		{"", "bump", "", false},
	}
	for _, tc := range cases {
		arg, ok := commandArg(tc.cmd, tc.verb)
		if ok != tc.wantOK || arg != tc.wantArg {
			t.Errorf("commandArg(%q,%q)=(%q,%v), want (%q,%v)", tc.cmd, tc.verb, arg, ok, tc.wantArg, tc.wantOK)
		}
	}
}

func TestBumpBudget_NoPriorRun(t *testing.T) {
	r, _, uis := newTestRunner(t, t.TempDir())
	ui := uis.newUI("C1", "T1")
	r.bumpBudget(t.Context(), ui, "T1", "10")
	posts := strings.Join(fakePosts(uis.ui("T1")), "\n")
	if !strings.Contains(posts, "Nothing to bump") {
		t.Errorf("bump with no prior run should say so, got:\n%s", posts)
	}
}

func TestBumpBudget_BadArg(t *testing.T) {
	r, _, uis := newTestRunner(t, t.TempDir())
	ui := uis.newUI("C1", "T1")
	r.bumpBudget(t.Context(), ui, "T1", "lots")
	posts := strings.Join(fakePosts(uis.ui("T1")), "\n")
	if !strings.Contains(posts, "Usage") {
		t.Errorf("bump with bad arg should show usage, got:\n%s", posts)
	}
}
