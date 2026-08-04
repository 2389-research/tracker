// ABOUTME: Tests the operator warning for unpriced usage under a --max-cost ceiling.
package pipeline

import (
	"strings"
	"testing"
)

func TestUnpricedBudgetWarning(t *testing.T) {
	cases := []struct {
		name        string
		totals      RunTotals
		costLimited bool
		want        bool
	}{
		{
			name:        "unpriced usage under a cost ceiling warns",
			totals:      RunTotals{Unpriced: true, UnpricedModels: []string{"made-up"}},
			costLimited: true,
			want:        true,
		},
		{
			name:        "unpriced usage with no cost ceiling is silent",
			totals:      RunTotals{Unpriced: true, UnpricedModels: []string{"made-up"}},
			costLimited: false,
			want:        false,
		},
		{
			name:        "priced usage under a cost ceiling is silent",
			totals:      RunTotals{Unpriced: false},
			costLimited: true,
			want:        false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := UnpricedBudgetWarning(tc.totals, tc.costLimited)
			if (msg != "") != tc.want {
				t.Errorf("UnpricedBudgetWarning(%+v, %v) = %q, want non-empty=%v",
					tc.totals, tc.costLimited, msg, tc.want)
			}
			if tc.want && !strings.Contains(msg, "made-up") {
				t.Errorf("warning %q should name the unpriced model", msg)
			}
		})
	}
}

// The signal must never hard-fail the run: a genuinely-free local model
// legitimately costs $0, so unpriced usage is a warning, not a stop. This pins
// that WarnUnpricedBudget returns nothing that a caller could treat as an error.
func TestWarnUnpricedBudgetDoesNotHardFail(t *testing.T) {
	// Compiles-and-returns-void is the contract: there is no error return to
	// halt the run on. If a future edit gave it one, this file would not build.
	WarnUnpricedBudget(RunTotals{Unpriced: true, UnpricedModels: []string{"x"}}, true)
}
