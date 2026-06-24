// ABOUTME: Guard for the ApprovePlan human gate — it must surface the actual
// ABOUTME: milestone plan + requirement-coverage table so the operator can review it.
package pipeline

import (
	"strings"
	"testing"
)

// The ApprovePlan gate historically showed only the static label "Review the
// milestone plan. Approve, adjust, or reject." with no plan content, so an
// operator had nothing to review and could only rubber-stamp `approve`. The
// Decompose agent already writes the plan to .ai/decisions/milestones.md and the
// spec-coverage table to .ai/decisions/requirement-coverage.md; a ShowPlan tool
// node must cat both into ctx.tool_stdout, and ApprovePlan must interpolate it
// (same fullscreen-review pattern as the #407 EscalateMilestone gate).

// TestBuildProductShowPlanSurfacesPlanFiles pins that a ShowPlan tool node reads
// the plan and coverage files the operator needs to review.
func TestBuildProductShowPlanSurfacesPlanFiles(t *testing.T) {
	cmd := toolCmd(t, "ShowPlan")
	for _, want := range []string{
		".ai/decisions/milestones.md",
		".ai/decisions/requirement-coverage.md",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("ShowPlan command does not surface %s — the operator can't review the plan at the ApprovePlan gate", want)
		}
	}
}

// TestBuildProductApprovePlanSurfacesPlan pins that the ApprovePlan gate
// interpolates the plan content (ctx.tool_stdout from ShowPlan) instead of
// showing only a bare label, so the review modal renders the real plan.
func TestBuildProductApprovePlanSurfacesPlan(t *testing.T) {
	p := nodePrompt(t, loadBuildProduct(t), "ApprovePlan")
	if !strings.Contains(p, "${ctx.tool_stdout}") {
		t.Error("ApprovePlan prompt has no ${ctx.tool_stdout} interpolation — the operator sees only a label and cannot actually review the milestone plan")
	}
}

// TestBuildProductApprovePlanRoutedThroughShowPlan pins the wiring: Decompose's
// success path goes through ShowPlan (which populates the plan into context)
// before reaching ApprovePlan, and the three approve/adjust/reject routes off
// ApprovePlan are preserved.
func TestBuildProductApprovePlanRoutedThroughShowPlan(t *testing.T) {
	g := loadBuildProduct(t)

	if !hasEdgeWithCondition(g, "Decompose", "ShowPlan", "ctx.outcome = success") {
		t.Error("Decompose success no longer routes through ShowPlan — the plan files are never read into context for the ApprovePlan gate")
	}
	if !hasEdgeTo(g, "ShowPlan", "ApprovePlan") {
		t.Error("ShowPlan must route to ApprovePlan so the read plan is shown for review")
	}
	labels := map[string]bool{}
	for _, e := range g.OutgoingEdges("ApprovePlan") {
		if e.Label != "" {
			labels[e.Label] = true
		}
	}
	for _, lbl := range []string{"approve", "adjust", "reject"} {
		if !labels[lbl] {
			t.Errorf("ApprovePlan lost its %q labeled edge", lbl)
		}
	}
}
