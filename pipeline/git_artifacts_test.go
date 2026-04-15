// ABOUTME: Tests for git-backed artifact tracking (gitArtifactRepo and engine integration).
package pipeline

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// requireGit skips the test if git is not available in PATH.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH, skipping git artifact test")
	}
}

// gitOutput runs a git command in dir and returns the trimmed stdout output.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", cmdArgs...) //nolint:gosec
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out.String())
	}
	return strings.TrimSpace(out.String())
}

// TestGitArtifactRepo_InitCreatesRepo verifies that Init() creates a .git directory
// and an initial commit.
func TestGitArtifactRepo_InitCreatesRepo(t *testing.T) {
	requireGit(t)

	dir := t.TempDir()
	repo := newGitArtifactRepo(dir, "testrunjj1")

	if err := repo.Init(); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	// .git should exist.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf(".git not found after Init(): %v", err)
	}

	// git log should show the initial commit.
	log := gitOutput(t, dir, "log", "--oneline")
	if !strings.Contains(log, "tracker: run testrunjj1 started") {
		t.Errorf("git log doesn't contain initial commit, got:\n%s", log)
	}
}

// TestGitArtifactRepo_CommitNodeRecordsHistory verifies that three sequential
// CommitNode calls produce three commits in execution order.
func TestGitArtifactRepo_CommitNodeRecordsHistory(t *testing.T) {
	requireGit(t)

	dir := t.TempDir()
	repo := newGitArtifactRepo(dir, "run123")

	if err := repo.Init(); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	nodes := []struct {
		id      string
		handler string
		status  string
	}{
		{"start", "start", "success"},
		{"middle", "codergen", "success"},
		{"end", "exit", "success"},
	}

	dur := 42 * time.Millisecond
	for _, n := range nodes {
		entry := &TraceEntry{
			NodeID:      n.id,
			HandlerName: n.handler,
			Status:      n.status,
			Duration:    dur,
			EdgeTo:      "",
		}
		if err := repo.CommitNode(n.id, n.handler, n.status, entry); err != nil {
			t.Fatalf("CommitNode(%q): %v", n.id, err)
		}
	}

	// git log --oneline should show 3 node commits + initial = 4 commits total.
	log := gitOutput(t, dir, "log", "--oneline")
	lines := strings.Split(log, "\n")
	// Count non-empty lines.
	var nonEmpty []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}
	if len(nonEmpty) < 4 {
		t.Errorf("expected at least 4 commits (initial + 3 nodes), got %d:\n%s", len(nonEmpty), log)
	}

	// Check that node commit messages are present.
	for _, n := range nodes {
		expected := "node(" + n.id + "):"
		if !strings.Contains(log, expected) {
			t.Errorf("git log does not contain %q\nFull log:\n%s", expected, log)
		}
	}
}

// TestGitArtifactRepo_TagCheckpoint verifies that TagCheckpoint creates the expected tag.
func TestGitArtifactRepo_TagCheckpoint(t *testing.T) {
	requireGit(t)

	dir := t.TempDir()
	runID := "run456"
	repo := newGitArtifactRepo(dir, runID)

	if err := repo.Init(); err != nil {
		t.Fatalf("Init() error: %v", err)
	}

	entry := &TraceEntry{
		NodeID:      "myNode",
		HandlerName: "codergen",
		Status:      "success",
		Duration:    10 * time.Millisecond,
	}
	if err := repo.CommitNode("myNode", "codergen", "success", entry); err != nil {
		t.Fatalf("CommitNode: %v", err)
	}

	if err := repo.TagCheckpoint("myNode"); err != nil {
		t.Fatalf("TagCheckpoint: %v", err)
	}

	// git tag -l 'checkpoint/*' should list our tag.
	tags := gitOutput(t, dir, "tag", "-l", "checkpoint/*")
	expectedTag := "checkpoint/" + runID + "/myNode"
	if !strings.Contains(tags, expectedTag) {
		t.Errorf("expected tag %q in output, got:\n%s", expectedTag, tags)
	}
}

// TestGitArtifactRepo_DisabledOnGitMissing verifies that Init() sets failed=true
// when git is not in PATH and that subsequent CommitNode is a no-op (returns nil).
func TestGitArtifactRepo_DisabledOnGitMissing(t *testing.T) {
	// Override PATH so git cannot be found.
	origPath := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", origPath) }) //nolint:errcheck
	os.Setenv("PATH", "/dev/null")                    //nolint:errcheck

	dir := t.TempDir()
	repo := newGitArtifactRepo(dir, "runXXX")

	err := repo.Init()
	if err == nil {
		t.Fatal("expected Init() to return error when git is missing, got nil")
	}
	if !repo.failed {
		t.Error("expected repo.failed=true after Init() failure")
	}

	// Subsequent CommitNode must be a no-op — not panic, not error.
	entry := &TraceEntry{NodeID: "n1", HandlerName: "codergen", Status: "success"}
	if err := repo.CommitNode("n1", "codergen", "success", entry); err != nil {
		t.Errorf("CommitNode after failure should return nil, got: %v", err)
	}

	// TagCheckpoint must also be a no-op.
	if err := repo.TagCheckpoint("n1"); err != nil {
		t.Errorf("TagCheckpoint after failure should return nil, got: %v", err)
	}
}

// TestEngine_WithGitArtifacts_ProducesCommitsPerNode is an integration test that
// builds a 3-node graph, runs it with WithGitArtifacts(true) and WithArtifactDir,
// then asserts that git log contains commits for all three nodes plus the initial.
func TestEngine_WithGitArtifacts_ProducesCommitsPerNode(t *testing.T) {
	requireGit(t)

	artifactBase := t.TempDir()

	// 3-node linear graph mirroring TestEngine_EmitsCostUpdatedAfterEachNode.
	g := NewGraph("git_artifacts_test")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "middle", Shape: "box", Label: "Middle"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "start", To: "middle"})
	g.AddEdge(&Edge{From: "middle", To: "end"})

	nodeStats := &SessionStats{
		InputTokens:  10,
		OutputTokens: 5,
		TotalTokens:  15,
		CostUSD:      0.001,
		Provider:     "test",
	}

	reg := NewHandlerRegistry()
	for _, name := range []string{"start", "exit", "codergen", "wait.human", "conditional", "parallel", "parallel.fan_in", "tool"} {
		n := name
		reg.Register(&testHandler{name: n, executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{Status: OutcomeSuccess, Stats: nodeStats}, nil
		}})
	}

	engine := NewEngine(g, reg,
		WithArtifactDir(artifactBase),
		WithGitArtifacts(true),
	)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine.Run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("expected success, got %q", result.Status)
	}

	// Find the run directory (the only subdirectory created in artifactBase).
	entries, err := os.ReadDir(artifactBase)
	if err != nil {
		t.Fatalf("ReadDir(artifactBase): %v", err)
	}
	var runDirs []string
	for _, e := range entries {
		if e.IsDir() {
			runDirs = append(runDirs, filepath.Join(artifactBase, e.Name()))
		}
	}
	if len(runDirs) != 1 {
		t.Fatalf("expected exactly 1 run dir, got %d: %v", len(runDirs), runDirs)
	}
	repoDir := runDirs[0]

	// .git must exist.
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		t.Fatalf(".git missing from repo dir %q: %v", repoDir, err)
	}

	// git log should have: initial + start + middle + end = 4+ commits.
	log := gitOutput(t, repoDir, "log", "--oneline")
	t.Logf("git log:\n%s", log)

	lines := strings.Split(log, "\n")
	var nonEmpty []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}
	// Expect at least 3 node commits + 1 initial.
	if len(nonEmpty) < 4 {
		t.Errorf("expected at least 4 commits, got %d:\n%s", len(nonEmpty), log)
	}

	// Check node commit messages.
	for _, nodeID := range []string{"start", "middle", "end"} {
		expected := "node(" + nodeID + "):"
		if !strings.Contains(log, expected) {
			t.Errorf("git log does not contain %q\nFull log:\n%s", expected, log)
		}
	}

	// Initial commit must be present.
	if !strings.Contains(log, "tracker: run") {
		t.Errorf("git log missing initial 'tracker: run' commit:\n%s", log)
	}
}
