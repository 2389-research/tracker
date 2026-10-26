// ABOUTME: Tests for #714 — a tool_command-safe ctx key whose value was restored
// ABOUTME: from a checkpoint must not expand into a shell command until re-derived.
package pipeline

import (
	"strings"
	"testing"
)

// TestExpandVariables_ToolCommandMode_RejectsResumeUntrustedSafeKey pins the
// #714 fix: a safe-allowlisted ctx key whose value came from the checkpoint
// (marked shell-untrusted on resume) is rejected in tool_command mode, so a
// tampered checkpoint cannot inject shell text through an allowlisted key.
// Prompt expansion (not tool_command mode) is unaffected, and a live
// re-derivation (Set) clears the mark and restores shell expansion.
func TestExpandVariables_ToolCommandMode_RejectsResumeUntrustedSafeKey(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set(ContextKeyHumanResponse, "; rm -rf ~ #")
	ctx.MarkShellUntrusted(ContextKeyHumanResponse)

	// Shell path: blocked.
	if _, err := ExpandVariables("echo ${ctx.human_response}", ctx, nil, nil, false, true); err == nil {
		t.Fatal("expected error expanding a checkpoint-restored safe key into a tool command")
	} else if !strings.Contains(err.Error(), "checkpoint") {
		t.Errorf("error = %q, want a message mentioning the checkpoint origin", err)
	}

	// Prompt path (toolCommandMode=false): unaffected — the value still expands.
	got, err := ExpandVariables("Prior answer: ${ctx.human_response}", ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("prompt expansion must not be gated by shell-untrust: %v", err)
	}
	if !strings.Contains(got, "; rm -rf ~ #") {
		t.Errorf("prompt expansion = %q, want it to contain the restored value", got)
	}

	// A live node re-derives the value → mark clears → shell expansion allowed.
	ctx.Set(ContextKeyHumanResponse, "yes")
	res, err := ExpandVariables("echo ${ctx.human_response}", ctx, nil, nil, false, true)
	if err != nil {
		t.Fatalf("after live re-derivation the key must expand into a tool command: %v", err)
	}
	if res != "echo yes" {
		t.Errorf("result = %q, want %q", res, "echo yes")
	}
}

// TestPipelineContext_SetClearsShellUntrusted pins that any live write to a key
// clears its shell-untrusted mark — re-derivation makes the value trusted again.
func TestPipelineContext_SetClearsShellUntrusted(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set(ContextKeyPreferredLabel, "from-checkpoint")
	ctx.MarkShellUntrusted(ContextKeyPreferredLabel)
	if !ctx.IsShellUntrusted(ContextKeyPreferredLabel) {
		t.Fatal("key should be shell-untrusted after MarkShellUntrusted")
	}
	ctx.Set(ContextKeyPreferredLabel, "live-value")
	if ctx.IsShellUntrusted(ContextKeyPreferredLabel) {
		t.Error("a live Set must clear the shell-untrusted mark")
	}
}

// TestCopyShellUntrustedTo pins that a parallel branch context, built from the
// parent's value snapshot, also inherits the parent's shell-untrust marks — so a
// checkpoint-restored safe value cannot escape the gate by riding a branch
// snapshot (#714). A key the branch re-derives (a live Set) still clears.
func TestCopyShellUntrustedTo(t *testing.T) {
	parent := NewPipelineContext()
	parent.Set(ContextKeyHumanResponse, "; rm -rf ~ #")
	parent.MarkShellUntrusted(ContextKeyHumanResponse)
	parent.Set(ContextKeyBranchID, "stale-from-checkpoint")
	parent.MarkShellUntrusted(ContextKeyBranchID)

	branch := NewPipelineContextFrom(parent.Snapshot())
	parent.CopyShellUntrustedTo(branch)

	if !branch.IsShellUntrusted(ContextKeyHumanResponse) {
		t.Error("branch must inherit the parent's shell-untrust mark for human_response")
	}
	// The parallel handler re-derives branch_id with a fresh live Set; that
	// clears the inherited mark.
	branch.Set(ContextKeyBranchID, "BuildMilestone3")
	if branch.IsShellUntrusted(ContextKeyBranchID) {
		t.Error("a fresh live Set of branch_id must clear the inherited mark")
	}
}

// TestMarkRestoredSafeKeysShellUntrusted pins that resume marks exactly the
// tool_command-safe keys the checkpoint actually carried — not a safe key the
// checkpoint omitted, and not an ordinary (non-safe) key.
func TestMarkRestoredSafeKeysShellUntrusted(t *testing.T) {
	cp := &Checkpoint{Context: map[string]string{
		ContextKeyHumanResponse:  "; touch /tmp/pwned #",
		ContextKeyPreferredLabel: "approve",
		ContextKeyLastResponse:   "llm text", // not a safe key — never marked (already blocked)
		"some_author_key":        "value",    // ordinary key
	}}
	pctx := NewPipelineContext()
	for k, v := range cp.Context {
		pctx.Set(k, v)
	}

	markRestoredSafeKeysShellUntrusted(cp, pctx)

	if !pctx.IsShellUntrusted(ContextKeyHumanResponse) {
		t.Error("human_response from checkpoint must be shell-untrusted")
	}
	if !pctx.IsShellUntrusted(ContextKeyPreferredLabel) {
		t.Error("preferred_label from checkpoint must be shell-untrusted")
	}
	// outcome was NOT in the checkpoint → not marked.
	if pctx.IsShellUntrusted(ContextKeyOutcome) {
		t.Error("outcome absent from checkpoint must not be marked")
	}
	// An ordinary key is never a tool_command-safe key, so marking is irrelevant;
	// assert the helper only touches the allowlist.
	if pctx.IsShellUntrusted("some_author_key") {
		t.Error("ordinary keys must not be marked shell-untrusted")
	}
}
