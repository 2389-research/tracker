// ABOUTME: Tests for #303 turn-limit breach classification in the codergen handler.
// ABOUTME: Covers classifyBreach unit cases + end-to-end Execute breach outcomes.
package handlers

import (
	"context"
	"os"
	"testing"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/pipeline"
)

func TestClassifyBreach_VerifiedGreenAdvancesAsSuccess(t *testing.T) {
	status, class := classifyBreach("guard", agent.SessionResult{BreachVerify: agent.BreachVerifyPassed}, true)
	if status != pipeline.OutcomeSuccess {
		t.Errorf("status = %q, want success", status)
	}
	if class != pipeline.TurnBreachClassVerifiedGreen {
		t.Errorf("class = %q, want %q", class, pipeline.TurnBreachClassVerifiedGreen)
	}
}

func TestClassifyBreach_LoopDetectedAlwaysPathological(t *testing.T) {
	// Even a green verify cannot rescue a detected loop.
	status, class := classifyBreach("guard", agent.SessionResult{LoopDetected: true, BreachVerify: agent.BreachVerifyPassed}, true)
	if status != pipeline.OutcomeFail {
		t.Errorf("status = %q, want fail", status)
	}
	if class != pipeline.TurnBreachClassPathological {
		t.Errorf("class = %q, want %q", class, pipeline.TurnBreachClassPathological)
	}
}

func TestClassifyBreach_RedAndNotRunRouteToOperator(t *testing.T) {
	for _, bv := range []agent.BreachVerifyState{agent.BreachVerifyFailed, agent.BreachVerifyNotRun} {
		status, class := classifyBreach("guard", agent.SessionResult{BreachVerify: bv}, true)
		if status != pipeline.OutcomeFail || class != pipeline.TurnBreachClassOperatorDecision {
			t.Errorf("bv=%v: got (%q,%q), want (fail,operator_decision)", bv, status, class)
		}
	}
}

func TestClassifyBreach_FailPolicyIsGuillotine(t *testing.T) {
	status, class := classifyBreach("fail", agent.SessionResult{BreachVerify: agent.BreachVerifyPassed}, true)
	if status != pipeline.OutcomeFail {
		t.Errorf("opt-out status = %q, want fail", status)
	}
	if class != "" {
		t.Errorf("opt-out class = %q, want empty (no marker)", class)
	}
}

func TestClassifyBreach_NonNativeIsGuillotine(t *testing.T) {
	status, class := classifyBreach("guard", agent.SessionResult{BreachVerify: agent.BreachVerifyPassed}, false)
	if status != pipeline.OutcomeFail || class != "" {
		t.Errorf("non-native got (%q,%q), want (fail, \"\")", status, class)
	}
}

func writeFile755(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o755)
}

func TestExecute_BreachGreen_AdvancesAsSuccessWithMarker(t *testing.T) {
	workdir := t.TempDir()
	pass := workdir + "/pass.sh"
	if err := writeFile755(pass, "#!/bin/sh\nexit 0\n"); err != nil {
		t.Fatal(err)
	}
	h := NewCodergenHandler(&alwaysToolCallCompleter{}, workdir)
	node := &pipeline.Node{
		ID: "Implement", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{
			"prompt":         "build it",
			"max_turns":      "3",
			"verify_command": pass,
			// turn_breach_policy defaults to "guard"
		},
	}
	out, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Status != pipeline.OutcomeSuccess {
		t.Errorf("status = %q, want success (verified-green breach)", out.Status)
	}
	if out.ContextUpdates[pipeline.ContextKeyTurnBreachClass] != pipeline.TurnBreachClassVerifiedGreen {
		t.Errorf("turn_breach_class = %q, want verified_green", out.ContextUpdates[pipeline.ContextKeyTurnBreachClass])
	}
}

func TestExecute_BreachRed_FailsWithOperatorMarker(t *testing.T) {
	workdir := t.TempDir()
	fail := workdir + "/fail.sh"
	if err := writeFile755(fail, "#!/bin/sh\nexit 1\n"); err != nil {
		t.Fatal(err)
	}
	h := NewCodergenHandler(&alwaysToolCallCompleter{}, workdir)
	node := &pipeline.Node{
		ID: "Implement", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{"prompt": "build it", "max_turns": "3", "verify_command": fail},
	}
	out, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Status != pipeline.OutcomeFail {
		t.Errorf("status = %q, want fail", out.Status)
	}
	if out.ContextUpdates[pipeline.ContextKeyTurnBreachClass] != pipeline.TurnBreachClassOperatorDecision {
		t.Errorf("turn_breach_class = %q, want operator_decision", out.ContextUpdates[pipeline.ContextKeyTurnBreachClass])
	}
}

func TestExecute_TurnBreachPolicyFail_PinsGuillotine(t *testing.T) {
	workdir := t.TempDir()
	pass := workdir + "/pass.sh"
	if err := writeFile755(pass, "#!/bin/sh\nexit 0\n"); err != nil {
		t.Fatal(err)
	}
	h := NewCodergenHandler(&alwaysToolCallCompleter{}, workdir)
	node := &pipeline.Node{
		ID: "Implement", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{
			"prompt": "build it", "max_turns": "3",
			"verify_command":     pass,
			"turn_breach_policy": "fail", // opt-out
		},
	}
	out, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Byte-for-byte today's behavior: fail + exact message, NO marker.
	if out.Status != pipeline.OutcomeFail {
		t.Errorf("opt-out status = %q, want fail", out.Status)
	}
	wantMsg := `node "Implement": agent exhausted turn limit (3 turns) without completing`
	if got := out.ContextUpdates[pipeline.ContextKeyTurnLimitMsg]; got != wantMsg {
		t.Errorf("turn_limit_msg = %q, want %q", got, wantMsg)
	}
	if _, present := out.ContextUpdates[pipeline.ContextKeyTurnBreachClass]; present {
		t.Error("opt-out must NOT set turn_breach_class")
	}
}

func TestExecute_BreachRed_AutoStatusCannotForceSuccess(t *testing.T) {
	workdir := t.TempDir()
	fail := workdir + "/fail.sh"
	if err := writeFile755(fail, "#!/bin/sh\nexit 1\n"); err != nil {
		t.Fatal(err)
	}
	// alwaysToolCallCompleter emits no STATUS line → parseAutoStatus would
	// default to success. The breach guard must prevent that.
	h := NewCodergenHandler(&alwaysToolCallCompleter{}, workdir)
	node := &pipeline.Node{
		ID: "Implement", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{
			"prompt": "build it", "max_turns": "3",
			"verify_command": fail,
			"auto_status":    "true",
		},
	}
	out, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Status != pipeline.OutcomeFail {
		t.Errorf("status = %q, want fail (auto_status must not rescue a red breach)", out.Status)
	}
}
