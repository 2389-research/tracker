// ABOUTME: PR #414 finding (Codex P1) — human gates must display their `prompt:`
// ABOUTME: attribute body, not just the one-line Label, so interpolated context shows.
package handlers

import (
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// resolveHumanPrompt historically built the gate prompt from node.Label only and
// ignored node.Attrs["prompt"] for freeform/choice/yes_no modes. Every human gate
// that authored a multi-line `prompt:` (ApprovePlan, EscalateMilestone,
// EscalateReview, OperatorDecision, and ~30 more across the example pipelines)
// silently dropped that body at runtime — including any ${ctx.tool_stdout}
// interpolation the gate relied on to surface live state. These tests pin that
// the prompt body is shown and its variables expanded.

func TestResolveHumanPromptIncludesPromptAttr(t *testing.T) {
	h := NewHumanHandler(&AutoApproveInterviewer{}, nil)
	node := &pipeline.Node{
		ID:    "gate",
		Shape: "hexagon",
		Label: "Review the plan",
		Attrs: map[string]string{
			"prompt": "Skim the plan below, then choose.\n\n---\n## Plan\n${ctx.tool_stdout}",
		},
	}
	pctx := pipeline.NewPipelineContext()
	pctx.Set(pipeline.ContextKeyToolStdout, "MILESTONE 1: scaffold\nMILESTONE 2: api")

	got := h.resolveHumanPrompt(node, pctx)

	if !strings.Contains(got, "Skim the plan below") {
		t.Errorf("resolved prompt dropped the node's prompt body — operator sees only the label:\n%s", got)
	}
	if !strings.Contains(got, "MILESTONE 1: scaffold") {
		t.Errorf("resolved prompt did not interpolate ${ctx.tool_stdout} — live state is never surfaced (PR #414 Codex P1):\n%s", got)
	}
}

// Regression guard: a gate with no prompt attr still shows its Label exactly as
// before (no behavioral change for label-only gates).
func TestResolveHumanPromptLabelOnlyUnchanged(t *testing.T) {
	h := NewHumanHandler(&AutoApproveInterviewer{}, nil)
	node := &pipeline.Node{ID: "gate", Shape: "hexagon", Label: "Approve?"}
	got := h.resolveHumanPrompt(node, pipeline.NewPipelineContext())
	if strings.TrimSpace(got) != "Approve?" {
		t.Errorf("label-only gate prompt changed; want \"Approve?\", got %q", got)
	}
}
