// ABOUTME: Tests for #346 — auto_status heading tolerance and fail-closed
// ABOUTME: missing-STATUS handling on goal-gate nodes.
package handlers

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// TestParseAutoStatus_HeadingMarkers covers #346 defect 2: LLMs emit the
// verdict as a markdown heading (`## STATUS:fail`) to make it draw the eye,
// exactly like the bold shapes #233 Gap 5.1 already tolerates. Leading
// heading markers must be stripped before the prefix check.
func TestParseAutoStatus_HeadingMarkers(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		expect pipeline.TerminalStatus
	}{
		{
			name:   "h2 heading no space after colon: ## STATUS:fail",
			input:  "## STATUS:fail",
			expect: pipeline.OutcomeFail,
		},
		{
			name:   "h1 heading: # STATUS: success",
			input:  "# STATUS: success",
			expect: pipeline.OutcomeSuccess,
		},
		{
			name:   "h3 heading wrapping bold: ### **STATUS: fail**",
			input:  "### **STATUS: fail**",
			expect: pipeline.OutcomeFail,
		},
		{
			name:   "heading without space after hashes: ##STATUS:fail",
			input:  "##STATUS:fail",
			expect: pipeline.OutcomeFail,
		},
		{
			name: "case study b68b532619c3: heading verdict at end of report",
			input: "## Verification Report\n" +
				"cmd/goblin/ does not exist. 0 of 8 criteria met.\n" +
				"## STATUS:fail",
			expect: pipeline.OutcomeFail,
		},
		{
			name: "last-wins survives heading shapes",
			input: "## STATUS:fail\n" +
				"...recovered...\n" +
				"## STATUS:success",
			expect: pipeline.OutcomeSuccess,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := parseAutoStatus(tc.input)
			if !found {
				t.Fatalf("parseAutoStatus(%q) found = false, want true", tc.input)
			}
			if got != tc.expect {
				t.Errorf("parseAutoStatus(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}

// TestParseAutoStatus_FoundFlag pins the new found return: false when no
// parseable STATUS line exists (including fence-only), true when one does.
// The status value on found=false stays OutcomeSuccess (legacy default) so
// non-gate callers keep back-compat behavior.
func TestParseAutoStatus_FoundFlag(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantFound bool
	}{
		{"no STATUS at all", "Some narrative without any marker.", false},
		{"STATUS only inside code fence", "```\nSTATUS:fail\n```\ndone", false},
		{"heading STATUS with invalid value", "## STATUS: of the project is unclear", false},
		{"plain STATUS present", "STATUS:fail", true},
		{"heading STATUS present", "## STATUS:fail", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := parseAutoStatus(tc.input)
			if found != tc.wantFound {
				t.Fatalf("parseAutoStatus(%q) found = %v, want %v", tc.input, found, tc.wantFound)
			}
			if !found && got != pipeline.OutcomeSuccess {
				t.Errorf("parseAutoStatus(%q) status on not-found = %q, want legacy default %q", tc.input, got, pipeline.OutcomeSuccess)
			}
		})
	}
}

// TestCodergenHandler_AutoStatusMissing_GoalGateFailsClosed is the #346
// defect-1 core: a goal-gate node whose agent never emitted a parseable
// STATUS line must resolve to fail, not success — an unparseable verdict on
// a gate is an anomaly, not a pass. The anomaly is surfaced via
// Outcome.MissingStatus so the engine can emit an audit event.
func TestCodergenHandler_AutoStatusMissing_GoalGateFailsClosed(t *testing.T) {
	client := &fakeCompleter{responseText: "Verification report: 0 of 8 criteria met, but no marker."}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "verify", Shape: "box", Handler: "codergen", Attrs: map[string]string{
		"prompt":      "verify the milestone",
		"auto_status": "true",
		"goal_gate":   "true",
	}}
	outcome, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("goal-gate node with no STATUS line = %q, want fail (fail-closed)", outcome.Status)
	}
	if outcome.MissingStatus == nil {
		t.Fatal("expected MissingStatus detail to be populated for observability")
	}
	if !outcome.MissingStatus.FailClosed {
		t.Error("MissingStatus.FailClosed = false, want true on a goal gate")
	}
	if outcome.MissingStatus.ResponseTail == "" {
		t.Error("MissingStatus.ResponseTail is empty, want response tail for diagnosis")
	}
}

// TestCodergenHandler_AutoStatusMissing_PlainNodeKeepsSuccessDefault pins
// back-compat: a plain auto_status node (no goal_gate) with no STATUS line
// keeps the legacy success default — but the anomaly is still observable
// via MissingStatus.
func TestCodergenHandler_AutoStatusMissing_PlainNodeKeepsSuccessDefault(t *testing.T) {
	client := &fakeCompleter{responseText: "Narrative with no marker."}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{
		"prompt":      "run tests",
		"auto_status": "true",
	}}
	outcome, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("plain auto_status node with no STATUS line = %q, want success (back-compat)", outcome.Status)
	}
	if outcome.MissingStatus == nil {
		t.Fatal("expected MissingStatus detail to be populated even when defaulting to success")
	}
	if outcome.MissingStatus.FailClosed {
		t.Error("MissingStatus.FailClosed = true, want false on a non-gate node")
	}
}

// TestCodergenHandler_AutoStatusHeading_GoalGateParsesFail is the end-to-end
// case-study shape: the gate's verdict arrives as a `## STATUS:fail` heading
// and must register as fail with no missing-status anomaly.
func TestCodergenHandler_AutoStatusHeading_GoalGateParsesFail(t *testing.T) {
	client := &fakeCompleter{responseText: "cmd/goblin/ does not exist. 0 of 8 criteria met.\n## STATUS:fail"}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "verify", Shape: "box", Handler: "codergen", Attrs: map[string]string{
		"prompt":      "verify the milestone",
		"auto_status": "true",
		"goal_gate":   "true",
	}}
	outcome, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("heading STATUS:fail on goal gate = %q, want fail", outcome.Status)
	}
	if outcome.MissingStatus != nil {
		t.Error("MissingStatus should be nil when a STATUS line parsed")
	}
}

// TestCodergenHandler_AutoStatusPresent_NoMissingDetail pins that a normal
// parsed STATUS line never produces a MissingStatus anomaly.
func TestCodergenHandler_AutoStatusPresent_NoMissingDetail(t *testing.T) {
	client := &fakeCompleter{responseText: "STATUS:success\nAll good."}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{
		"prompt":      "run tests",
		"auto_status": "true",
	}}
	outcome, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("status = %q, want success", outcome.Status)
	}
	if outcome.MissingStatus != nil {
		t.Error("MissingStatus should be nil when a STATUS line parsed")
	}
}

// TestCodergenHandler_NoAutoStatus_NoMissingDetail pins that nodes without
// auto_status are completely unaffected by #346 — no detail, no flip.
func TestCodergenHandler_NoAutoStatus_NoMissingDetail(t *testing.T) {
	client := &fakeCompleter{responseText: "Narrative with no marker."}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{
		"prompt":    "run tests",
		"goal_gate": "true", // even on a gate: without auto_status there is no STATUS contract
	}}
	outcome, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("status = %q, want success", outcome.Status)
	}
	if outcome.MissingStatus != nil {
		t.Error("MissingStatus must be nil for nodes without auto_status")
	}
}

// TestParseAutoStatus_TolerantGrammar covers #645: realistic LLM variants of
// the terminal STATUS line that the exact-match parser rejected (found=false,
// which defaulted non-goal-gate nodes such as VerifyMilestone /
// SynthesizeReviews / Decompose to success). Case IDs (G1..G14) are the
// issue's deep-hunt labels. Currently-passing shapes are pinned alongside
// so the tolerant grammar cannot regress them.
func TestParseAutoStatus_TolerantGrammar(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		expect    pipeline.TerminalStatus
		wantFound bool
	}{
		// --- #645 fail-open cases ---
		{"G1 trailing period: STATUS:fail.", "Checks ran.\nSTATUS:fail.", pipeline.OutcomeFail, true},
		{"G2 trailing prose after em dash: STATUS: fail — 2 checks failed", "STATUS: fail — 2 checks failed", pipeline.OutcomeFail, true},
		{"G3 trailing parenthesised count: STATUS:fail (2)", "COVERAGE_GAPS: 2\nSTATUS:fail (2)", pipeline.OutcomeFail, true},
		{"G4 inline code: `STATUS:fail`", "`STATUS:fail`", pipeline.OutcomeFail, true},
		{"G7 trailing emoji: STATUS:fail ❌", "STATUS:fail ❌", pipeline.OutcomeFail, true},
		{"G8 trailing emoji on success: STATUS:success ✅", "STATUS:success ✅", pipeline.OutcomeSuccess, true},
		{
			"G14 unclosed fence before the verdict",
			"Pasting grep output:\n```\n$ grep -n foo bar.go\nbar.go:12: foo()\nSTATUS:fail",
			pipeline.OutcomeFail, true,
		},
		{
			"G14b unclosed fence after a properly closed one",
			"```\nSTATUS:success\n```\nthen more output\n```\ngo test ./... (truncated)\nSTATUS:fail",
			pipeline.OutcomeFail, true,
		},
		{"trailing colon-free prose: STATUS: success, all 8 criteria met", "STATUS: success, all 8 criteria met", pipeline.OutcomeSuccess, true},
		{"bold with trailing punctuation: **STATUS: fail**.", "**STATUS: fail**.", pipeline.OutcomeFail, true},
		{"inline code with bold: **`STATUS:success`**", "**`STATUS:success`**", pipeline.OutcomeSuccess, true},
		{"space before colon: STATUS : fail", "STATUS : fail", pipeline.OutcomeFail, true},
		{"strikethrough markers: ~~STATUS:fail~~", "~~STATUS:fail~~", pipeline.OutcomeFail, true},
		{"lowercase keyword: status: fail", "status: fail", pipeline.OutcomeFail, true},
		{"retry with trailing prose", "STATUS: retry — need the spec re-read", pipeline.OutcomeRetry, true},

		// --- ambiguity: a STATUS line with an unparseable value is not a verdict ---
		{"unparseable value: STATUS: maybe", "STATUS: maybe", pipeline.OutcomeSuccess, false},
		{"value glued to a longer word is not a verdict: STATUS: failure", "STATUS: failure", pipeline.OutcomeSuccess, false},
		{"value glued to a longer word: STATUS: successful", "STATUS: successful", pipeline.OutcomeSuccess, false},
		{"malformed final line does not erase an explicit early fail", "STATUS:fail\nran out of context\nSTATUS: maybe", pipeline.OutcomeFail, true},

		// --- pinned: currently-passing shapes ---
		{"exact: STATUS:fail", "STATUS:fail", pipeline.OutcomeFail, true},
		{"exact with space: STATUS: success", "STATUS: success", pipeline.OutcomeSuccess, true},
		{"heading: ## STATUS:fail", "## STATUS:fail", pipeline.OutcomeFail, true},
		{"bold: **STATUS: fail**", "**STATUS: fail**", pipeline.OutcomeFail, true},
		{"no STATUS at all", "Some narrative without any marker.", pipeline.OutcomeSuccess, false},
		{"STATUS inside a properly closed fence is ignored", "```\nSTATUS:fail\n```\ndone", pipeline.OutcomeSuccess, false},
		{"closed fence then real verdict", "```\nSTATUS:fail\n```\nSTATUS:success", pipeline.OutcomeSuccess, true},
		{"prose that merely mentions STATUS is not a verdict", "Per the rule: if fixes are non-empty, STATUS:fail.", pipeline.OutcomeSuccess, false},

		// --- last-line-wins contract (SpecLint / ForgeSpec) ---
		{
			"SpecLint shape: early fail on line 1, final success overrides",
			"STATUS:fail\n## Findings\nnone critical\nSTATUS:success",
			pipeline.OutcomeSuccess, true,
		},
		{
			"SpecLint shape truncated: early fail stands",
			"STATUS:fail\n## Findings\n... response cut off",
			pipeline.OutcomeFail, true,
		},
		{
			"SpecLint shape with tolerant final line: early fail, `STATUS:success` ✅",
			"STATUS:fail\nall checks pass\n`STATUS:success` ✅",
			pipeline.OutcomeSuccess, true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := parseAutoStatus(tc.input)
			if found != tc.wantFound {
				t.Fatalf("parseAutoStatus(%q) found = %v, want %v (status %q)", tc.input, found, tc.wantFound, got)
			}
			if got != tc.expect {
				t.Errorf("parseAutoStatus(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}
