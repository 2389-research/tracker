// ABOUTME: Tests for ExportBundle — round-trip, error cases, and git-missing guard.
package tracker

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// Verify pipeline.Handler interface signature.
var _ pipeline.Handler = (*alwaysSucceedHandler)(nil)

// requireGitForBundle skips the test when git is not available in PATH.
func requireGitForBundle(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH, skipping bundle test")
	}
}

// gitOutputBundle runs a git command in dir and returns trimmed stdout.
func gitOutputBundle(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(context.Background(), "git", cmdArgs...) //nolint:gosec
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v in %q: %v\n%s", args, dir, err, out.String())
	}
	return strings.TrimSpace(out.String())
}

// TestExportBundle_RoundTrip runs a 3-node graph with git artifacts enabled,
// exports a bundle, and verifies the bundle can be cloned and contains the
// expected commits.
func TestExportBundle_RoundTrip(t *testing.T) {
	requireGitForBundle(t)

	artifactBase := t.TempDir()

	// 3-node linear graph — mirrors TestEngine_WithGitArtifacts_ProducesCommitsPerNode.
	g := pipeline.NewGraph("bundle_test")
	g.AddNode(&pipeline.Node{ID: "start", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&pipeline.Node{ID: "middle", Shape: "box", Label: "Middle"})
	g.AddNode(&pipeline.Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&pipeline.Edge{From: "start", To: "middle"})
	g.AddEdge(&pipeline.Edge{From: "middle", To: "end"})

	reg := pipeline.NewHandlerRegistry()
	for _, name := range []string{"start", "exit", "codergen", "wait.human", "conditional", "parallel", "parallel.fan_in", "tool"} {
		n := name
		reg.Register(&alwaysSucceedHandler{name: n})
	}

	engine := pipeline.NewEngine(g, reg,
		pipeline.WithArtifactDir(artifactBase),
		pipeline.WithGitArtifacts(true),
	)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine.Run: %v", err)
	}
	if result.Status != pipeline.OutcomeSuccess {
		t.Fatalf("expected success, got %q", result.Status)
	}

	// Build the run dir from RunID. We can't use tracker.Result.ArtifactRunDir
	// here because this test drives pipeline.Engine directly (not tracker.Engine),
	// so only pipeline.EngineResult is available.
	if result.RunID == "" {
		t.Fatal("engine result has empty RunID")
	}
	runDir := filepath.Join(artifactBase, result.RunID)

	// Export the bundle.
	bundlePath := filepath.Join(t.TempDir(), "run.bundle")
	if err := ExportBundle(runDir, bundlePath); err != nil {
		t.Fatalf("ExportBundle: %v", err)
	}

	// Bundle file must exist and be non-empty.
	fi, err := os.Stat(bundlePath)
	if err != nil {
		t.Fatalf("bundle file stat: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatal("bundle file is empty")
	}

	// git bundle verify must exit 0. Run it in the artifact run dir which is
	// a valid git repo — `git bundle verify` requires a repo context.
	gitOutputBundle(t, runDir, "bundle", "verify", bundlePath)

	// Clone the bundle into a restore dir.
	restoreDir := filepath.Join(t.TempDir(), "restored")
	cmd := exec.CommandContext(context.Background(), "git", "clone", bundlePath, restoreDir) //nolint:gosec
	var cloneOut bytes.Buffer
	cmd.Stdout = &cloneOut
	cmd.Stderr = &cloneOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("git clone bundle: %v\n%s", err, cloneOut.String())
	}

	// git log in the restored dir must contain commits for all three nodes
	// plus the initial run-start commit.
	log := gitOutputBundle(t, restoreDir, "log", "--oneline")
	t.Logf("restored git log:\n%s", log)

	for _, nodeID := range []string{"start", "middle", "end"} {
		expected := "node(" + nodeID + "):"
		if !strings.Contains(log, expected) {
			t.Errorf("restored log missing commit for node %q\nFull log:\n%s", nodeID, log)
		}
	}
	if !strings.Contains(log, "tracker: run") {
		t.Errorf("restored log missing initial 'tracker: run' commit:\n%s", log)
	}
}

// TestExportBundle_NotAGitRepo verifies that ExportBundle returns a meaningful
// error when the target directory is not a git repository.
func TestExportBundle_NotAGitRepo(t *testing.T) {
	requireGitForBundle(t)

	notARepo := t.TempDir()
	bundlePath := filepath.Join(t.TempDir(), "out.bundle")

	err := ExportBundle(notARepo, bundlePath)
	if err == nil {
		t.Fatal("expected error for non-git directory, got nil")
	}
	// Error should mention "git bundle create" to be actionable.
	if !strings.Contains(err.Error(), "git bundle create") {
		t.Errorf("error message should mention 'git bundle create', got: %v", err)
	}
}

// TestExportBundle_GitMissing verifies that ExportBundle fails gracefully when
// git is not in PATH. This test skips if git is actually present and cannot be
// simulated (the skip guard is intentionally reversed from requireGitForBundle).
func TestExportBundle_GitMissing(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		// Git is already missing — test the real absence case.
		dir := t.TempDir()
		bundlePath := filepath.Join(t.TempDir(), "out.bundle")
		err := ExportBundle(dir, bundlePath)
		if err == nil {
			t.Fatal("expected error when git is missing, got nil")
		}
		if !strings.Contains(err.Error(), "git not found in PATH") {
			t.Errorf("expected 'git not found in PATH' in error, got: %v", err)
		}
		return
	}

	// Git is available; override PATH to simulate a missing git.
	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) }) //nolint:errcheck
	os.Setenv("PATH", t.TempDir())                    //nolint:errcheck // empty temp dir = no binaries

	dir := t.TempDir()
	bundlePath := filepath.Join(t.TempDir(), "out.bundle")
	err := ExportBundle(dir, bundlePath)
	if err == nil {
		t.Fatal("expected error when git is missing (PATH=/dev/null), got nil")
	}
	if !strings.Contains(err.Error(), "git not found in PATH") {
		t.Errorf("expected 'git not found in PATH' in error, got: %v", err)
	}
}

// alwaysSucceedHandler is a minimal handler for tests that always returns success.
type alwaysSucceedHandler struct{ name string }

func (h *alwaysSucceedHandler) Name() string { return h.name }
func (h *alwaysSucceedHandler) Execute(_ context.Context, _ *pipeline.Node, _ *pipeline.PipelineContext) (pipeline.Outcome, error) {
	return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
}
