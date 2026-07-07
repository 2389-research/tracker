// ABOUTME: Regression guards for the autonomous spec-forge loop folded into
// ABOUTME: build_product.dip (SpecLint rule h + ForgeSpec/CheckSpecFidelity/
// ABOUTME: CheckSpecForgeBudget/SpecForgeFailed loop).
package pipeline

import (
	"os"
	"path/filepath"
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

// TestSpecForgeLoopEdges pins the rewired routing: SpecLint's fail no longer
// dead-ends at a human, the forge loop is wired with a restart back-edge, and
// ReadSpec's infra-fail path is UNCHANGED (never enters the loop).
func TestSpecForgeLoopEdges(t *testing.T) {
	g := loadBuildProduct(t)
	// SpecLint fail now drives the forge budget gate, not EscalateReview.
	if !hasEdgeWithCondition(g, "SpecLint", "CheckSpecForgeBudget", "ctx.outcome = fail") {
		t.Error("SpecLint fail must route to CheckSpecForgeBudget (spec-forge)")
	}
	if hasEdgeTo(g, "SpecLint", "EscalateReview") {
		t.Error("SpecLint must no longer route to EscalateReview (spec-forge rewire)")
	}
	// Budget gate: success -> ForgeSpec, fail -> hard stop.
	if !hasEdgeWithCondition(g, "CheckSpecForgeBudget", "ForgeSpec", "ctx.outcome = success") {
		t.Error("CheckSpecForgeBudget success must route to ForgeSpec")
	}
	if !hasUnconditionalEdgeTo(g, "CheckSpecForgeBudget", "SpecForgeFailed") {
		t.Error("CheckSpecForgeBudget must fall back unconditionally to SpecForgeFailed on budget exhaustion (also keeps the tool node dippin-covered)")
	}
	// ForgeSpec: success -> fidelity, unconditional fallback -> hard stop.
	if !hasEdgeWithCondition(g, "ForgeSpec", "CheckSpecFidelity", "ctx.outcome = success") {
		t.Error("ForgeSpec success must route to CheckSpecFidelity")
	}
	if !hasUnconditionalEdgeTo(g, "ForgeSpec", "SpecForgeFailed") {
		t.Error("ForgeSpec must have an unconditional fallback to SpecForgeFailed (no dead-end on unexpected outcome)")
	}
	// Fidelity oracle: success loops back to SpecLint with restart, else hard stop.
	if !hasEdgeAttr(g, "CheckSpecFidelity", "SpecLint", "ctx.outcome = success", "restart", "true") {
		t.Error("CheckSpecFidelity success must restart SpecLint (re-lint the hardened spec)")
	}
	if !hasUnconditionalEdgeTo(g, "CheckSpecFidelity", "SpecForgeFailed") {
		t.Error("CheckSpecFidelity must fall back to SpecForgeFailed on a fidelity violation / unexpected outcome")
	}
	// Hard-stop is fail-closed: single unconditional edge to Done => strict-fail halt.
	if !hasUnconditionalEdgeTo(g, "SpecForgeFailed", "Done") {
		t.Error("SpecForgeFailed needs a single unconditional edge to Done so exit 1 strict-fail-halts (dippin-valid + fail-closed)")
	}
	// ReadSpec's infra-fail path is UNCHANGED — it must NOT enter the forge loop.
	if !hasEdgeWithCondition(g, "ReadSpec", "EscalateReview", "ctx.outcome = fail") {
		t.Error("ReadSpec fail (infra fault) must still route to EscalateReview, NOT the forge loop (spec-forge)")
	}
	// SpecLint remains an unavoidable gate.
	if reachesNodeAvoiding(g, "Setup", "Decompose", "SpecLint") {
		t.Error("Decompose reachable without SpecLint — the preflight must still gate decomposition")
	}
}

// TestCheckSpecForgeBudgetCounter behaviorally drives the budget tool's shell
// body: OK for attempts 1-3, hard-stop on 4; it snapshots SPEC.original.md on
// first entry; a stale counter escalates immediately; a corrupted counter must
// NOT abort under set -eu (numeric guard). String-only asserts on `-gt` would
// pass for -ge/-gt/in-node variants alike (the #443 lesson), so this executes.
func TestCheckSpecForgeBudgetCounter(t *testing.T) {
	cmd := toolCmd(t, "CheckSpecForgeBudget")

	// Fresh dir with a SPEC.md so the snapshot copy succeeds.
	dir := setupRunDir(t)
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte("# spec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := stackEnv(t, "")

	for i := 1; i <= 3; i++ {
		_, code := runToolCmd(t, cmd, dir, env)
		if code != 0 {
			t.Fatalf("attempt %d: exit %d, want 0 (budget OK)", i, code)
		}
	}
	if _, code := runToolCmd(t, cmd, dir, env); code == 0 {
		t.Error("attempt 4: exit 0, want non-zero (budget exhausted → hard stop)")
	}
	// Snapshot taken on first entry.
	if _, err := os.Stat(filepath.Join(dir, ".ai/decisions/SPEC.original.md")); err != nil {
		t.Errorf("SPEC.original.md snapshot missing after forge budget ran: %v", err)
	}

	// Stale counter poisons a fresh loop → immediate escalate.
	dir2 := setupRunDir(t)
	_ = os.WriteFile(filepath.Join(dir2, "SPEC.md"), []byte("# spec\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir2, ".ai/build"), 0o755)
	_ = os.WriteFile(filepath.Join(dir2, ".ai/build/spec_forge_attempts"), []byte("3\n"), 0o644)
	if _, code := runToolCmd(t, cmd, dir2, env); code == 0 {
		t.Error("pre-seeded counter=3 must escalate on first call (documents the Setup-reset requirement)")
	}

	// Corrupted counter must not abort under set -eu (numeric guard).
	dir3 := setupRunDir(t)
	_ = os.WriteFile(filepath.Join(dir3, "SPEC.md"), []byte("# spec\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir3, ".ai/build"), 0o755)
	_ = os.WriteFile(filepath.Join(dir3, ".ai/build/spec_forge_attempts"), []byte("garbage\n"), 0o644)
	if _, code := runToolCmd(t, cmd, dir3, env); code != 0 {
		t.Errorf("corrupted counter: exit %d, want 0 (numeric guard resets to 0, treats as attempt 1)", code)
	}
}
