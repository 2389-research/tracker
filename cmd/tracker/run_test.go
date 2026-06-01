// ABOUTME: Tests for interpretRunResult sentinel-error wiring and --fail-on-override
// ABOUTME: flag/env-var parsing (Gap 5.2, Tasks 19+20).
package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// --- Task 19: --fail-on-override flag + TRACKER_FAIL_ON_OVERRIDE env var --------------------

func TestParseFlags_FailOnOverride(t *testing.T) {
	args := []string{"tracker", "run.dip", "--fail-on-override"}
	cfg, err := parseFlags(args)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !cfg.failOnOverride {
		t.Errorf("failOnOverride: want true, got false")
	}
}

func TestParseFlags_FailOnOverrideDefaultFalse(t *testing.T) {
	args := []string{"tracker", "run.dip"}
	cfg, err := parseFlags(args)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.failOnOverride {
		t.Errorf("failOnOverride: want false (default), got true")
	}
}

// TestFailOnOverride_EnvParsing checks strict =1 parsing matching the
// TRACKER_PASS_API_KEYS convention. Only "1" enables the flag; other
// truthy-looking values ("true", "yes", "TRUE") are rejected.
func TestFailOnOverride_EnvParsing(t *testing.T) {
	cases := []struct {
		envVal string
		want   bool
	}{
		{"1", true},
		{"true", false},
		{"yes", false},
		{"TRUE", false},
		{"", false},
		{"0", false},
		{"2", false},
	}
	for _, tc := range cases {
		t.Run("env="+tc.envVal, func(t *testing.T) {
			t.Setenv("TRACKER_FAIL_ON_OVERRIDE", tc.envVal)
			cfg := runConfig{}
			applyFailOnOverrideEnv(&cfg)
			if cfg.failOnOverride != tc.want {
				t.Errorf("env=%q -> failOnOverride=%v, want %v",
					tc.envVal, cfg.failOnOverride, tc.want)
			}
		})
	}
}

// TestFailOnOverride_FlagWinsOverEnv ensures that an explicit --fail-on-override
// flag is not clobbered by absent/zero env var, and a "1" env var doesn't unset
// the flag (no inverse direction). The env var only fills in when the flag is
// not already set.
func TestFailOnOverride_FlagWinsOverEnv(t *testing.T) {
	t.Setenv("TRACKER_FAIL_ON_OVERRIDE", "")
	cfg := runConfig{failOnOverride: true}
	applyFailOnOverrideEnv(&cfg)
	if !cfg.failOnOverride {
		t.Error("flag-set failOnOverride was cleared by empty env var")
	}
}

// --- Task 20: interpretRunResult sentinel return + IsSuccess + fail dominates ----------------

func TestInterpretRunResult_FailOnOverride_ReturnsSentinel(t *testing.T) {
	res := &pipeline.EngineResult{
		Status: pipeline.OutcomeValidationOverridden,
		ValidationOverrides: []pipeline.OverrideDetail{
			{GateNodeID: "Gate", Label: "accept", Actor: pipeline.ActorHuman},
		},
	}
	cfg := &runConfig{failOnOverride: true}
	err := interpretRunResult(res, nil, cfg)
	if !errors.Is(err, pipeline.ErrValidationOverridden) {
		t.Errorf("err = %v, want ErrValidationOverridden", err)
	}
}

func TestInterpretRunResult_OverrideDefaultExitZero(t *testing.T) {
	res := &pipeline.EngineResult{Status: pipeline.OutcomeValidationOverridden}
	cfg := &runConfig{failOnOverride: false}
	err := interpretRunResult(res, nil, cfg)
	if err != nil {
		t.Errorf("err = %v, want nil (default exit 0 — IsSuccess covers validation_overridden)", err)
	}
}

// TestInterpretRunResult_FailDominates verifies that --fail-on-override + an
// actual fail still returns a generic fail error (exit 1), not the override
// sentinel (which would be exit 2). Spec: failure dominates.
func TestInterpretRunResult_FailDominates(t *testing.T) {
	res := &pipeline.EngineResult{Status: pipeline.OutcomeFail}
	cfg := &runConfig{failOnOverride: true}
	err := interpretRunResult(res, nil, cfg)
	if errors.Is(err, pipeline.ErrValidationOverridden) {
		t.Error("err is ErrValidationOverridden, want generic fail")
	}
	if err == nil {
		t.Error("err is nil, want generic fail error")
	}
}

func TestInterpretRunResult_BudgetDominates(t *testing.T) {
	res := &pipeline.EngineResult{Status: pipeline.OutcomeBudgetExceeded}
	cfg := &runConfig{failOnOverride: true}
	err := interpretRunResult(res, nil, cfg)
	if errors.Is(err, pipeline.ErrValidationOverridden) {
		t.Error("err is ErrValidationOverridden, want budget fail")
	}
	if err == nil {
		t.Error("err is nil, want budget fail error")
	}
}

// TestInterpretRunResult_SuccessNoError verifies the happy path: a vanilla
// success run returns nil error regardless of --fail-on-override.
func TestInterpretRunResult_SuccessNoError(t *testing.T) {
	res := &pipeline.EngineResult{Status: pipeline.OutcomeSuccess}
	for _, foo := range []bool{false, true} {
		cfg := &runConfig{failOnOverride: foo}
		if err := interpretRunResult(res, nil, cfg); err != nil {
			t.Errorf("failOnOverride=%v: err = %v, want nil", foo, err)
		}
	}
}

// TestInterpretRunResult_RunErrPropagates verifies that a raw engine runErr is
// surfaced first (not classified as an override).
func TestInterpretRunResult_RunErrPropagates(t *testing.T) {
	rawErr := errors.New("engine boom")
	res := &pipeline.EngineResult{Status: pipeline.OutcomeValidationOverridden}
	cfg := &runConfig{failOnOverride: true}
	err := interpretRunResult(res, rawErr, cfg)
	if errors.Is(err, pipeline.ErrValidationOverridden) {
		t.Error("err is ErrValidationOverridden, want wrapped engine error")
	}
	if err == nil || !strings.Contains(err.Error(), "engine boom") {
		t.Errorf("err = %v, want wrapping of %q", err, rawErr)
	}
}

// TestHeadlineOverride picks the latest entry from the list per spec D5a.
func TestHeadlineOverride(t *testing.T) {
	ds := []pipeline.OverrideDetail{
		{GateNodeID: "First", Label: "early"},
		{GateNodeID: "Second", Label: "middle"},
		{GateNodeID: "Last", Label: "headline"},
	}
	got := headlineOverride(ds)
	if got.GateNodeID != "Last" || got.Label != "headline" {
		t.Errorf("got %+v, want last entry", got)
	}
}

func TestHeadlineOverride_Empty(t *testing.T) {
	got := headlineOverride(nil)
	if got.GateNodeID != "" || got.Label != "" {
		t.Errorf("expected zero value for empty input, got %+v", got)
	}
}
