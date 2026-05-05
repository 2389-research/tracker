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

type assertErr string

func (e assertErr) Error() string { return string(e) }
