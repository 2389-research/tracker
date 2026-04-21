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
	node := &pipeline.Node{
		ID:    "n1",
		Attrs: map[string]string{"writes": "commit_sha, commit_sha"},
	}
	contextUpdates := map[string]string{}
	failed := applyDeclaredWrites(node, contextUpdates, "not json", "Tool stdout JSON")
	if !failed {
		t.Fatal("expected failure")
	}
	msg := contextUpdates[contextKeyWritesError]
	if strings.Contains(msg, "commit_sha, commit_sha") {
		t.Fatalf("expected deduped writes in error message, got %q", msg)
	}
	if !strings.Contains(msg, "declared writes: [commit_sha]") {
		t.Fatalf("expected deduped declared writes in error message, got %q", msg)
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
