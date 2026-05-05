package handlers

import (
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func TestFormatWritesError_TruncatesRawOutput(t *testing.T) {
	raw := strings.Repeat("x", maxWritesErrorRawLen+20)
	msg := formatWritesError("n1", []string{"a"}, "Tool stdout JSON", assertErr("bad json"), raw)
	if !strings.Contains(msg, "truncated") {
		t.Fatalf("expected truncation marker, got %q", msg)
	}
	if strings.Contains(msg, raw) {
		t.Fatalf("expected full raw output to be omitted")
	}
}

func TestFormatWritesError_LeavesShortRawOutput(t *testing.T) {
	raw := `{"a":"b"}`
	msg := formatWritesError("n1", []string{"a"}, "Tool stdout JSON", assertErr("bad json"), raw)
	if !strings.Contains(msg, raw) {
		t.Fatalf("expected short raw output to be preserved, got %q", msg)
	}
}

func TestDedupeDeclaredWrites_PreservesOrder(t *testing.T) {
	got := dedupeDeclaredWrites([]string{"a", "b", "a", "c", "b"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q (all=%v)", i, got[i], want[i], got)
		}
	}
}

func TestApplyDeclaredWrites_DedupesWritesInErrorMessage(t *testing.T) {
	// Use two distinct keys (with one duplicated) so multi-key path triggers error.
	node := &pipeline.Node{
		ID:    "n1",
		Attrs: map[string]string{"writes": "commit_sha, branch, commit_sha"},
	}
	contextUpdates := map[string]string{}
	failed := applyDeclaredWrites(node, contextUpdates, "not json", "Tool stdout JSON")
	if !failed {
		t.Fatal("expected failure")
	}
	msg := contextUpdates[contextKeyWritesError]
	if strings.Contains(msg, "commit_sha, branch, commit_sha") {
		t.Fatalf("expected deduped writes in error message, got %q", msg)
	}
	if !strings.Contains(msg, "declared writes: [commit_sha, branch]") {
		t.Fatalf("expected deduped declared writes in error message, got %q", msg)
	}
}

// --- Healing / fallback tests ---

func TestApplyDeclaredWrites_ExtractsFromFencedJSON(t *testing.T) {
	node := &pipeline.Node{
		ID:    "analyze_spec",
		Attrs: map[string]string{"writes": "spec_analysis"},
	}
	raw := "Here is the analysis:\n```json\n{\"spec_analysis\": \"the summary\"}\n```\nDone."
	ctx := map[string]string{}
	failed := applyDeclaredWrites(node, ctx, raw, "Response JSON")
	if failed {
		t.Fatalf("expected success after fenced JSON extraction, got error: %s", ctx[contextKeyWritesError])
	}
	if got := ctx["spec_analysis"]; got != "the summary" {
		t.Fatalf("spec_analysis = %q, want %q", got, "the summary")
	}
}

func TestApplyDeclaredWrites_ExtractsFromOutermostBraces(t *testing.T) {
	node := &pipeline.Node{
		ID:    "n1",
		Attrs: map[string]string{"writes": "result"},
	}
	raw := `I computed the result: {"result": "42"} and that is all.`
	ctx := map[string]string{}
	failed := applyDeclaredWrites(node, ctx, raw, "Response JSON")
	if failed {
		t.Fatalf("expected success after brace extraction, got error: %s", ctx[contextKeyWritesError])
	}
	if got := ctx["result"]; got != "42" {
		t.Fatalf("result = %q, want %q", got, "42")
	}
}

func TestApplyDeclaredWrites_SingleKeyFallbackToRaw(t *testing.T) {
	node := &pipeline.Node{
		ID:    "analyze_spec",
		Attrs: map[string]string{"writes": "spec_analysis"},
	}
	// Pure prose, no JSON anywhere — the NIFB failure case
	raw := "Done — `.ai/spec_analysis.md` has been created.\n\nSummary:\n- 104 functional requirements"
	ctx := map[string]string{}
	failed := applyDeclaredWrites(node, ctx, raw, "Response JSON")
	if failed {
		t.Fatalf("expected warning (not failure) for single-key fallback, got error: %s", ctx[contextKeyWritesError])
	}
	if got := ctx["spec_analysis"]; got != raw {
		t.Fatalf("expected raw response as fallback value, got %q", got)
	}
	if _, ok := ctx[contextKeyWritesWarning]; !ok {
		t.Fatal("expected writes_warning to be set for fallback")
	}
	if _, ok := ctx[contextKeyWritesError]; ok {
		t.Fatal("expected no writes_error for single-key fallback")
	}
}

func TestApplyDeclaredWrites_MultiKeyStillFailsOnProse(t *testing.T) {
	node := &pipeline.Node{
		ID:    "n1",
		Attrs: map[string]string{"writes": "key_a, key_b"},
	}
	raw := "Done. Everything is written."
	ctx := map[string]string{}
	failed := applyDeclaredWrites(node, ctx, raw, "Response JSON")
	if !failed {
		t.Fatal("expected failure for multi-key writes with no JSON")
	}
	if _, ok := ctx[contextKeyWritesError]; !ok {
		t.Fatal("expected writes_error to be set")
	}
}

func TestApplyDeclaredWrites_DirectJSONStillWorks(t *testing.T) {
	node := &pipeline.Node{
		ID:    "n1",
		Attrs: map[string]string{"writes": "val"},
	}
	raw := `{"val": "hello"}`
	ctx := map[string]string{}
	failed := applyDeclaredWrites(node, ctx, raw, "Response JSON")
	if failed {
		t.Fatalf("expected success for direct JSON, got error: %s", ctx[contextKeyWritesError])
	}
	if got := ctx["val"]; got != "hello" {
		t.Fatalf("val = %q, want %q", got, "hello")
	}
}

// Regression for CodeRabbit's "single-key fallback masks contract failures"
// finding. When ExtractJSONFromText surfaces valid JSON that simply lacks the
// declared key, the old code fell back to raw — silently shipping the entire
// prose response under a name it didn't fit. The fix gates the fallback on
// !foundExtractableJSON; this test pins it.
func TestApplyDeclaredWrites_ExtractedJSONMissingKeyHardFails(t *testing.T) {
	node := &pipeline.Node{
		ID:    "n1",
		Attrs: map[string]string{"writes": "spec_analysis"},
	}
	// Prose containing valid JSON, but the JSON has the wrong key.
	raw := "Result: {\"other_key\": \"x\"} done."
	ctx := map[string]string{}
	failed := applyDeclaredWrites(node, ctx, raw, "Response JSON")
	if !failed {
		t.Fatal("expected failure when extracted JSON lacks the declared key")
	}
	if _, ok := ctx[contextKeyWritesError]; !ok {
		t.Fatal("expected writes_error to be set")
	}
	if _, ok := ctx["spec_analysis"]; ok {
		t.Fatal("expected spec_analysis to NOT be set (raw fallback should be inhibited)")
	}
	// Error message should reflect the actual failure mode.
	msg := ctx[contextKeyWritesError]
	if !strings.Contains(msg, "extractable JSON but failed the writes contract") {
		t.Fatalf("expected specific error referencing extractable-but-noncontract JSON, got: %s", msg)
	}
}

// Defense against a workflow author declaring a writes key that collides
// with a reserved name. Two sets are reserved:
//
//   - Tool_command safe-key allowlist (outcome, preferred_label,
//     human_response, interview_answers) — security: shell-interpolation
//     gate must not be bypassed by an LLM landing prose under those names.
//   - Writes signal keys (writes_error, writes_warning) — integrity:
//     runtime owns these; spoofing them would mislead `tracker diagnose`
//     and downstream branch-on-error logic.
//
// The check fires before any value is written, so the rejection is
// fail-closed: the safe key never sees content even on the failing path.
func TestApplyDeclaredWrites_RejectsReservedKeyCollision(t *testing.T) {
	cases := []struct {
		key      string
		category string
	}{
		{"outcome", "tool_command safe-key"},
		{"preferred_label", "tool_command safe-key"},
		{"human_response", "tool_command safe-key"},
		{"interview_answers", "tool_command safe-key"},
		{"writes_error", "writes signal"},
		{"writes_warning", "writes signal"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			node := &pipeline.Node{
				ID:    "n1",
				Attrs: map[string]string{"writes": tc.key},
			}
			ctx := map[string]string{}
			failed := applyDeclaredWrites(node, ctx, `{"`+tc.key+`": "x"}`, "Response JSON")
			if !failed {
				t.Fatalf("expected failure for writes-key collision with %q (%s)", tc.key, tc.category)
			}
			if got, ok := ctx[tc.key]; ok && got == "x" {
				t.Fatalf("expected %q to NOT carry the LLM-supplied value; collision must be rejected before any value lands", tc.key)
			}
			msg := ctx[contextKeyWritesError]
			if !strings.Contains(msg, "reserved name") {
				t.Fatalf("expected error to mention reserved name, got: %s", msg)
			}
		})
	}
}

// Regression for the correctness reviewer's "uncapped fallback bloats
// artifacts" finding. A 50KB raw response landing in a single context key
// would propagate to status.json, activity.jsonl, checkpoints, and downstream
// prompts. Cap is enforced at maxFallbackValueBytes.
func TestApplyDeclaredWrites_FallbackValueIsCapped(t *testing.T) {
	node := &pipeline.Node{
		ID:    "n1",
		Attrs: map[string]string{"writes": "blob"},
	}
	// Build a >maxFallbackValueBytes raw response with no JSON anywhere.
	raw := strings.Repeat("a", maxFallbackValueBytes*3)
	ctx := map[string]string{}
	failed := applyDeclaredWrites(node, ctx, raw, "Response JSON")
	if failed {
		t.Fatalf("expected single-key prose fallback to succeed, got: %s", ctx[contextKeyWritesError])
	}
	got := ctx["blob"]
	if len(got) >= len(raw) {
		t.Fatalf("expected fallback to be truncated, got len=%d (raw len=%d)", len(got), len(raw))
	}
	if !strings.Contains(got, "truncated") {
		t.Fatal("expected truncation marker in capped fallback")
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
