// ABOUTME: Tests for #318 warm continue+N MaxTurns override plumbing in codergen.
// ABOUTME: A disk-driven, node-scoped override lets the operator gate re-enter an
// ABOUTME: agent node with a bumped turn budget while episode summaries carry warm.
package handlers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/pipeline"
)

// writeTurnOverride writes a node-scoped warm-continue MaxTurns override file
// under workingDir, matching the path codergen.buildConfig consults.
func writeTurnOverride(t *testing.T, workingDir, nodeID, value string) {
	t.Helper()
	dir := filepath.Join(workingDir, ".tracker", "turn_overrides")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir override dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, nodeID), []byte(value), 0o644); err != nil {
		t.Fatalf("write override file: %v", err)
	}
}

// TestBuildConfig_HonorsMaxTurnsOverrideFromDisk pins #318 hazard 2: MaxTurns is
// read statically today; warm continue+N needs a disk-driven override consulted
// in buildConfig. A node-scoped override file bumps the agent's turn budget.
func TestBuildConfig_HonorsMaxTurnsOverrideFromDisk(t *testing.T) {
	workdir := t.TempDir()
	writeTurnOverride(t, workdir, "Implement", "130")

	h := &CodergenHandler{workingDir: workdir}
	node := &pipeline.Node{
		ID:    "Implement",
		Attrs: map[string]string{"max_turns": "50"},
	}
	config := h.buildConfig(node)
	if config.MaxTurns != 130 {
		t.Errorf("MaxTurns = %d, want 130 (disk override should bump the static node value)", config.MaxTurns)
	}
}

// TestBuildConfig_OverrideIsNodeScoped pins that the override only applies to the
// node it names — a sibling agent node sharing the working dir must NOT inherit
// another node's bumped budget (prevents cross-node turn-budget bleed).
func TestBuildConfig_OverrideIsNodeScoped(t *testing.T) {
	workdir := t.TempDir()
	writeTurnOverride(t, workdir, "Implement", "130")

	h := &CodergenHandler{workingDir: workdir}
	other := &pipeline.Node{
		ID:    "FixMilestone",
		Attrs: map[string]string{"max_turns": "50"},
	}
	config := h.buildConfig(other)
	if config.MaxTurns != 50 {
		t.Errorf("FixMilestone MaxTurns = %d, want 50 (override for Implement must not bleed)", config.MaxTurns)
	}
}

// TestBuildConfig_NoOverrideKeepsNodeMaxTurns pins the no-override default: an
// agent node with no override file keeps its statically-configured max_turns.
func TestBuildConfig_NoOverrideKeepsNodeMaxTurns(t *testing.T) {
	workdir := t.TempDir()
	h := &CodergenHandler{workingDir: workdir}
	node := &pipeline.Node{
		ID:    "Implement",
		Attrs: map[string]string{"max_turns": "50"},
	}
	config := h.buildConfig(node)
	if config.MaxTurns != 50 {
		t.Errorf("MaxTurns = %d, want 50 (no override file present)", config.MaxTurns)
	}
}

// TestBuildConfig_EmptyWorkingDirNoOverride pins that an unset working dir is a
// safe no-op (no panic, no stray relative-path read), keeping the node value.
func TestBuildConfig_EmptyWorkingDirNoOverride(t *testing.T) {
	h := &CodergenHandler{}
	node := &pipeline.Node{
		ID:    "Implement",
		Attrs: map[string]string{"max_turns": "50"},
	}
	config := h.buildConfig(node)
	if config.MaxTurns != 50 {
		t.Errorf("MaxTurns = %d, want 50 (empty working dir must not read an override)", config.MaxTurns)
	}
}

// TestReadMaxTurnsOverride_RejectsPathTraversal pins that a node ID is never
// used to traverse out of the override directory or read an arbitrary file: a
// nodeID containing separators or ".." resolves to no override (0), even when a
// readable integer file sits at the traversed-to location. Defense-in-depth —
// the helper is a general working-dir file read, so it fails closed.
func TestReadMaxTurnsOverride_RejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	workdir := filepath.Join(root, "work")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A valid integer file exactly where "../../../secret" would resolve from
	// <workdir>/.tracker/turn_overrides — i.e. an escape the guard must block.
	if err := os.WriteFile(filepath.Join(root, "secret"), []byte("999"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, nodeID := range []string{
		"../../../secret",
		"..",
		"sub/Implement",
	} {
		if got := readMaxTurnsOverride(workdir, nodeID); got != 0 {
			t.Errorf("readMaxTurnsOverride(workdir, %q) = %d, want 0 (must not traverse/read outside the override dir)", nodeID, got)
		}
	}
}

// TestReadMaxTurnsOverride_SkipsNonRegularFile pins that a symlink planted where
// the override file is expected is not followed — only a regular file counts.
func TestReadMaxTurnsOverride_SkipsNonRegularFile(t *testing.T) {
	workdir := t.TempDir()
	dir := filepath.Join(workdir, ".tracker", "turn_overrides")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(workdir, "elsewhere")
	if err := os.WriteFile(target, []byte("999"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "Implement")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if got := readMaxTurnsOverride(workdir, "Implement"); got != 0 {
		t.Errorf("readMaxTurnsOverride followed a symlink and returned %d, want 0", got)
	}
}

// TestWarmContinue_CarriesBumpedTurnsAndEpisodes pins the #318 acceptance
// criterion for continue+N: the resumed agent run config carries BOTH a bumped
// MaxTurns (from the disk override) AND non-empty PriorEpisodeSummaries (warm,
// carried via ContextKeyEpisodeSummaries) — i.e. it resumes warm, not cold.
func TestWarmContinue_CarriesBumpedTurnsAndEpisodes(t *testing.T) {
	workdir := t.TempDir()
	writeTurnOverride(t, workdir, "Implement", "130")

	h := NewCodergenHandler(&alwaysToolCallCompleter{}, workdir)
	node := &pipeline.Node{
		ID: "Implement", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{"prompt": "build it", "max_turns": "50"},
	}

	backend, err := h.selectBackend(node)
	if err != nil {
		t.Fatalf("selectBackend: %v", err)
	}
	runCfg, err := h.buildRunConfig(node, "build it", backend)
	if err != nil {
		t.Fatalf("buildRunConfig: %v", err)
	}

	pctx := pipeline.NewPipelineContext()
	pctx.Set(pipeline.ContextKeyEpisodeSummaries, agent.SerializeEpisodeSummaries([]string{
		"episode 1: scaffolded the package",
		"episode 2: wrote the parser",
	}))
	episodes := h.injectPriorEpisodes(runCfg, pctx)

	if runCfg.MaxTurns != 130 {
		t.Errorf("resumed MaxTurns = %d, want 130 (warm continue must bump the budget)", runCfg.MaxTurns)
	}
	if len(episodes) == 0 {
		t.Error("PriorEpisodeSummaries is empty — continue+N must resume warm, not cold")
	}
	sc, ok := runCfg.Extra.(*agent.SessionConfig)
	if !ok || sc == nil {
		t.Fatalf("runCfg.Extra is not *agent.SessionConfig: %T", runCfg.Extra)
	}
	if len(sc.PriorEpisodeSummaries) == 0 {
		t.Error("SessionConfig.PriorEpisodeSummaries is empty — warm episodes not injected")
	}
}
