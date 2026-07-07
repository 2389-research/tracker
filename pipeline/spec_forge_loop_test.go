// ABOUTME: Regression guards for the autonomous spec-forge loop folded into
// ABOUTME: build_product.dip (SpecLint rule h + ForgeSpec/CheckSpecFidelity/
// ABOUTME: CheckSpecForgeBudget/SpecForgeFailed loop).
package pipeline

import (
	"strings"
	"testing"
)

// TestSpecLintBuildableSubstanceRule pins the new CRITICAL rule (h): a too-thin
// spec (no named component or no checkable acceptance statement) must fail
// SpecLint so it enters the forge loop rather than sailing into Decompose.
func TestSpecLintBuildableSubstanceRule(t *testing.T) {
	g := loadBuildProduct(t)
	p := g.Nodes["SpecLint"].Attrs["prompt"]
	if !strings.Contains(p, "Buildable substance") {
		t.Error("SpecLint must carry CRITICAL rule (h) Buildable substance (issue: spec-forge)")
	}
	if !strings.Contains(p, "checkable acceptance statement") {
		t.Error("rule (h) must require at least one checkable acceptance statement, evidence-backed like (a)-(g)")
	}
}

// TestSetupResetsForgeState pins the fresh-run reset: Setup must clear the
// spec-forge counter and the original-spec snapshot so an abandoned prior run
// in the same workdir can't poison the next (the PR #264 stale-counter lesson).
func TestSetupResetsForgeState(t *testing.T) {
	cmd := toolCmd(t, "Setup")
	if !strings.Contains(cmd, "rm -f .ai/build/spec_forge_attempts") {
		t.Error("Setup must reset .ai/build/spec_forge_attempts on a fresh run (spec-forge)")
	}
	if !strings.Contains(cmd, ".ai/decisions/SPEC.original.md") {
		t.Error("Setup must clear the stale SPEC.original.md snapshot on a fresh run (spec-forge)")
	}
}
