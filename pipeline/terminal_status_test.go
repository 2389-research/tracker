// ABOUTME: Tests for TerminalStatus and IsSuccess() classification.
// ABOUTME: Pins the {success, validation_overridden} = success / others = fail rule.
package pipeline

import "testing"

func TestTerminalStatus_IsSuccess(t *testing.T) {
	cases := []struct {
		name string
		in   TerminalStatus
		want bool
	}{
		{"success", OutcomeSuccess, true},
		{"validation_overridden", OutcomeValidationOverridden, true},
		{"fail", OutcomeFail, false},
		{"budget_exceeded", OutcomeBudgetExceeded, false},
		{"retry", OutcomeRetry, false},
		{"unknown_future_value", TerminalStatus("future_status"), false},
		{"empty", TerminalStatus(""), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.IsSuccess(); got != tc.want {
				t.Errorf("%s.IsSuccess() = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestTerminalStatus_StringCompat(t *testing.T) {
	var s TerminalStatus = OutcomeSuccess
	if s != "success" {
		t.Errorf("TerminalStatus(success) != literal \"success\"")
	}
	if string(s) != "success" {
		t.Errorf("string(TerminalStatus(success)) = %q, want \"success\"", string(s))
	}
}
