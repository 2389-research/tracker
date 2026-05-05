// ABOUTME: Tests for the SR-block matcher used by the audit pass and dispatch_sprints.
// ABOUTME: Covers the four match strategies (exact, indent, whitespace, fuzzy), partial-apply, and the tolerant audit verdict parser.
package tools

import (
	"strings"
	"testing"
)

// applyOK is a test helper: applies blocks, fails the test if no blocks
// applied, and returns the patched text. Most match-strategy tests want this.
func applyOK(t *testing.T, draft string, blocks []srBlock) string {
	t.Helper()
	patched, applied, skipped := applySRBlocks(draft, blocks)
	if applied == 0 {
		t.Fatalf("expected at least one block to apply, got 0; skipped=%v", skipped)
	}
	return patched
}

func TestApplySRBlocks_Exact(t *testing.T) {
	draft := "line 1\nline 2\nline 3\n"
	blocks := []srBlock{{Search: "line 2", Replace: "REPLACED"}}
	got := applyOK(t, draft, blocks)
	want := "line 1\nREPLACED\nline 3\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestApplySRBlocks_Whitespace(t *testing.T) {
	// Draft has tabs; SEARCH text uses spaces. Whitespace strategy should match.
	draft := "func foo() {\n\treturn 1\n}\n"
	blocks := []srBlock{{
		Search:  "func foo() {\n    return 1\n}",
		Replace: "func foo() {\n\treturn 42\n}",
	}}
	got := applyOK(t, draft, blocks)
	if !strings.Contains(got, "return 42") {
		t.Errorf("expected replace applied, got %q", got)
	}
}

func TestApplySRBlocks_IndentPreserving(t *testing.T) {
	// Draft has the same block but at 8 spaces of outer indent; SEARCH has it
	// at 4 spaces (uniform within the search). Indent strategy: dedent search
	// by its uniform 4-space indent, find the dedented body in a same-indented
	// chunk, then re-indent replace by the chunk's outer indent.
	draft := "if condition:\n        x = 1\n        y = 2\n"
	blocks := []srBlock{{
		Search:  "    x = 1\n    y = 2",
		Replace: "    x = 99\n    y = 2",
	}}
	got := applyOK(t, draft, blocks)
	if !strings.Contains(got, "        x = 99") {
		t.Errorf("expected re-indented replace, got %q", got)
	}
	if strings.Contains(got, "if condition:\n    x = 99") {
		t.Errorf("indent stripping leaked outside the dedent/reindent pair: %q", got)
	}
}

func TestApplySRBlocks_WhitespaceMatchesIndentDriftFallback(t *testing.T) {
	draft := "class C:\n    def foo(self):\n        return 1\n"
	blocks := []srBlock{{
		Search:  "def foo(self):\n    return 1",
		Replace: "    def foo(self):\n        return 42",
	}}
	got := applyOK(t, draft, blocks)
	if !strings.Contains(got, "return 42") {
		t.Errorf("expected replace applied via whitespace fallback, got %q", got)
	}
}

func TestApplySRBlocks_Fuzzy(t *testing.T) {
	draft := "The quick brown fox jumps over the lazy dog.\nNext line stays.\n"
	blocks := []srBlock{{
		Search:  "The quick brawn fox jumps over the lazy dog.",
		Replace: "REPLACED",
	}}
	got := applyOK(t, draft, blocks)
	if !strings.Contains(got, "REPLACED") {
		t.Errorf("fuzzy match should have applied, got %q", got)
	}
}

func TestApplySRBlocks_FuzzyRejectsLowSimilarity(t *testing.T) {
	// SEARCH too different from anything in the draft → block is skipped, not errored.
	draft := "Lorem ipsum dolor sit amet, consectetur adipiscing elit.\n"
	blocks := []srBlock{{
		Search:  "Completely unrelated text that has nothing to do with the draft above.",
		Replace: "X",
	}}
	patched, applied, skipped := applySRBlocks(draft, blocks)
	if applied != 0 {
		t.Errorf("expected 0 blocks applied, got %d", applied)
	}
	if len(skipped) != 1 {
		t.Errorf("expected 1 skipped reason, got %d: %v", len(skipped), skipped)
	}
	if patched != draft {
		t.Errorf("draft should be unchanged when no blocks apply, got %q", patched)
	}
}

func TestApplySRBlocks_ExactWinsOverFuzzy(t *testing.T) {
	draft := "alpha beta gamma\n"
	blocks := []srBlock{{Search: "alpha beta gamma", Replace: "X"}}
	got := applyOK(t, draft, blocks)
	if got != "X\n" {
		t.Errorf("got %q want %q", got, "X\n")
	}
}

func TestApplySRBlocks_MultipleBlocksApplyInOrder(t *testing.T) {
	draft := "A\nB\nC\nD\n"
	blocks := []srBlock{
		{Search: "A", Replace: "1"},
		{Search: "C", Replace: "3"},
	}
	patched, applied, skipped := applySRBlocks(draft, blocks)
	if applied != 2 || len(skipped) != 0 {
		t.Errorf("expected 2 applied / 0 skipped, got %d / %v", applied, skipped)
	}
	want := "1\nB\n3\nD\n"
	if patched != want {
		t.Errorf("got %q want %q", patched, want)
	}
}

func TestApplySRBlocks_EmptySearchSkipped(t *testing.T) {
	draft := "anything\n"
	blocks := []srBlock{{Search: "", Replace: "X"}}
	patched, applied, skipped := applySRBlocks(draft, blocks)
	if applied != 0 {
		t.Errorf("expected 0 applied, got %d", applied)
	}
	if len(skipped) != 1 {
		t.Errorf("expected 1 skipped reason, got %d", len(skipped))
	}
	if patched != draft {
		t.Errorf("draft should be unchanged, got %q", patched)
	}
}

// TestApplySRBlocks_PartialApply covers the case where some blocks succeed
// and others fail (e.g., later block's SEARCH was modified by an earlier
// block's REPLACE). Partial-apply ships whatever succeeded and reports the
// failures rather than rolling back everything.
func TestApplySRBlocks_PartialApply(t *testing.T) {
	draft := "alpha\nbeta\ngamma\n"
	blocks := []srBlock{
		{Search: "alpha", Replace: "ALPHA"},                       // applies
		{Search: "this text is not in the draft", Replace: "ZZZ"}, // skipped
		{Search: "gamma", Replace: "GAMMA"},                       // applies
	}
	patched, applied, skipped := applySRBlocks(draft, blocks)
	if applied != 2 {
		t.Errorf("expected 2 blocks applied, got %d", applied)
	}
	if len(skipped) != 1 {
		t.Errorf("expected 1 skipped reason, got %d: %v", len(skipped), skipped)
	}
	if !strings.Contains(skipped[0], "block 1") {
		t.Errorf("skipped reason should name block 1, got %q", skipped[0])
	}
	if !strings.Contains(patched, "ALPHA") || !strings.Contains(patched, "GAMMA") {
		t.Errorf("expected ALPHA + GAMMA in patched, got %q", patched)
	}
	if !strings.Contains(patched, "beta") {
		t.Errorf("expected unchanged middle line, got %q", patched)
	}
}

func TestSimilarityRatio_KnownPairs(t *testing.T) {
	cases := []struct {
		a, b   string
		minVal float64
		maxVal float64
	}{
		{"abc", "abc", 1.0, 1.0},
		{"abc", "abd", 0.6, 0.7},
		{"hello world", "hello worlds", 0.91, 0.92},
		{"foo", "completely different", 0.0, 0.2},
		{"", "", 1.0, 1.0},
		{"", "x", 0.0, 0.0},
	}
	for _, tc := range cases {
		got := similarityRatio(tc.a, tc.b)
		if got < tc.minVal || got > tc.maxVal {
			t.Errorf("similarityRatio(%q,%q) = %.3f, want in [%.3f, %.3f]",
				tc.a, tc.b, got, tc.minVal, tc.maxVal)
		}
	}
}

func TestCommonLeadingIndent(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"    a\n    b\n    c", "    "},
		{"  a\n    b", "  "},
		{"\tx\n\ty", "\t"},
		{"a\nb", ""},
		{"    a\n\nb\n    c", ""},
		{"    a\n  \n    b", "    "},
		{"", ""},
	}
	for _, tc := range cases {
		got := commonLeadingIndent(tc.in)
		if got != tc.want {
			t.Errorf("commonLeadingIndent(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseAuditResponse_Pass(t *testing.T) {
	v, blocks, err := parseAuditResponse("AUDIT-VERDICT: PASS")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "PASS" {
		t.Errorf("got verdict %q want PASS", v)
	}
	if len(blocks) != 0 {
		t.Errorf("expected no blocks for PASS verdict, got %d", len(blocks))
	}
}

func TestParseAuditResponse_Patched(t *testing.T) {
	body := `AUDIT-VERDICT: PATCHED
<<<<<<< SEARCH
foo
=======
bar
>>>>>>> REPLACE`
	v, blocks, err := parseAuditResponse(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "PATCHED" {
		t.Errorf("got verdict %q want PATCHED", v)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Search != "foo" || blocks[0].Replace != "bar" {
		t.Errorf("block parsed wrong: %+v", blocks[0])
	}
}

// TestParseAuditResponse_TolerantFormats covers the cases that previously
// produced PASS-FALLBACK-MALFORMED: leading preambles, markdown decorations,
// blank lines, fence wrappers.
func TestParseAuditResponse_TolerantFormats(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"leading preamble before verdict",
			"Looking at the draft carefully against the checklist:\n\nAUDIT-VERDICT: PASS",
			"PASS",
		},
		{
			"markdown fence wrapping",
			"```\nAUDIT-VERDICT: PASS\n```",
			"PASS",
		},
		{
			"backtick decoration on verdict line",
			"`AUDIT-VERDICT: PASS`",
			"PASS",
		},
		{
			"bold decoration on verdict line",
			"**AUDIT-VERDICT: PASS**",
			"PASS",
		},
		{
			"trailing punctuation after verdict",
			"AUDIT-VERDICT: PASS.",
			"PASS",
		},
		{
			"multiple blank lines before verdict",
			"\n\n\nAUDIT-VERDICT: PASS",
			"PASS",
		},
		{
			"prose on lines 1-3, verdict on line 4",
			"After review, I found the spec mostly clean.\n\nMy assessment:\nAUDIT-VERDICT: PASS",
			"PASS",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, _, err := parseAuditResponse(tc.in)
			if err != nil {
				t.Fatalf("unexpected error parsing %q: %v", tc.in, err)
			}
			if v != tc.want {
				t.Errorf("got verdict %q want %q", v, tc.want)
			}
		})
	}
}

func TestParseAuditResponse_NoVerdictLine(t *testing.T) {
	// No verdict prefix anywhere → reports MALFORMED-class error.
	_, _, err := parseAuditResponse("Just some prose with no verdict marker.")
	if err == nil {
		t.Fatal("expected error for missing verdict line")
	}
	if !strings.Contains(err.Error(), "AUDIT-VERDICT") {
		t.Errorf("error should mention the missing prefix, got %v", err)
	}
}

func TestParseAuditResponse_VerdictBeyondTenLines(t *testing.T) {
	// If the verdict appears after 10 non-empty lines of preamble, we give up
	// and treat the response as malformed (avoid scanning indefinitely).
	var b strings.Builder
	for i := 0; i < 11; i++ {
		b.WriteString("preamble line\n")
	}
	b.WriteString("AUDIT-VERDICT: PASS\n")
	_, _, err := parseAuditResponse(b.String())
	if err == nil {
		t.Fatal("expected error: verdict beyond 10-line scan window")
	}
}
