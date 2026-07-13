// ABOUTME: Tests for git-backed artifact tracking (gitArtifactRepo and engine integration).
package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	// Strip git-internal repo pointers (GIT_DIR/GIT_INDEX_FILE/...) that leak
	// from a calling `git commit`'s hook environment. Without this, this helper's
	// `-C dir` is overridden by GIT_DIR/GIT_INDEX_FILE and writes to the OUTER
	// repo's index instead of the test's temp dir — clobbering it when the suite
	// runs under a pre-commit hook. Mirrors cleanGitEnv usage in git_preflight_test.go.
	cmd.Env = cleanGitEnv()
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

// TestEngine_WithGitArtifacts_CommitsRetryExhausted verifies that when a node
// exhausts its retry budget (no fallback_retry_target set), the terminal
// failure still produces a git commit recording the failure outcome.
// This exercises the handleRetryExhausted no-fallback path in engine_run.go.
func TestEngine_WithGitArtifacts_CommitsRetryExhausted(t *testing.T) {
	requireGit(t)
	artifactBase := t.TempDir()

	g := NewGraph("git_retry_exhaust_test")
	g.Attrs["default_max_retry"] = "2"
	g.Attrs["default_retry_policy"] = "none"
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "flaky", Shape: "box", Label: "Flaky"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "start", To: "flaky"})
	g.AddEdge(&Edge{From: "flaky", To: "end"})

	reg := NewHandlerRegistry()
	for _, name := range []string{"start", "exit", "codergen", "wait.human", "conditional", "parallel", "parallel.fan_in", "tool"} {
		n := name
		reg.Register(&testHandler{name: n, executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			// "flaky" always returns retry — it will exhaust the budget.
			if node.ID == "flaky" {
				return Outcome{Status: OutcomeRetry}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		}})
	}

	engine := NewEngine(g, reg,
		WithArtifactDir(artifactBase),
		WithGitArtifacts(true),
	)
	result, _ := engine.Run(context.Background())
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Status != OutcomeFail {
		t.Fatalf("expected fail after retry exhaustion, got %q", result.Status)
	}

	// Locate the run dir.
	entries, err := os.ReadDir(artifactBase)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var repoDir string
	for _, e := range entries {
		if e.IsDir() {
			repoDir = filepath.Join(artifactBase, e.Name())
			break
		}
	}
	if repoDir == "" {
		t.Fatal("no run dir created")
	}

	log := gitOutput(t, repoDir, "log", "--oneline")
	t.Logf("git log:\n%s", log)

	// There must be a commit for the flaky node showing outcome=fail (or retry,
	// depending on how the terminal entry is recorded).
	if !strings.Contains(log, "node(flaky):") {
		t.Errorf("git log missing commit for flaky node:\n%s", log)
	}
}

// commitCount returns the number of non-empty lines in a `git log --oneline` output.
func commitCount(log string) int {
	n := 0
	for _, l := range strings.Split(log, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// TestGitArtifactRepo_InitSkipsInitialCommitOnResume verifies that when Init()
// runs against an artifact directory that already has a git HEAD from a prior
// attempt, it does not append another "tracker: run <id> started" commit.
// This is the checkpoint-resume case — we don't want every restart to add a
// noise commit to git log.
func TestGitArtifactRepo_InitSkipsInitialCommitOnResume(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()

	// First Init — fresh repo, should produce the initial commit.
	r1 := newGitArtifactRepo(dir, "run-1")
	if err := r1.Init(); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	firstLog := gitOutput(t, dir, "log", "--oneline")
	if firstCount := commitCount(firstLog); firstCount != 1 || !strings.Contains(firstLog, "tracker: run run-1 started") {
		t.Fatalf("expected exactly one initial commit, got %d:\n%s", firstCount, firstLog)
	}

	// Second Init — existing HEAD, should NOT add another initial commit.
	r2 := newGitArtifactRepo(dir, "run-2")
	if err := r2.Init(); err != nil {
		t.Fatalf("second Init: %v", err)
	}
	secondLog := gitOutput(t, dir, "log", "--oneline")
	if secondLog != firstLog {
		t.Errorf("resume Init should not add commits.\nbefore:\n%s\nafter:\n%s", firstLog, secondLog)
	}
}

// TestEngine_WithGitArtifacts_CommitsFailOutcome verifies that a node that
// fails at the exit path still produces a git commit recording the failure,
// not just successes.
func TestEngine_WithGitArtifacts_CommitsFailOutcome(t *testing.T) {
	requireGit(t)
	artifactBase := t.TempDir()

	g := NewGraph("git_fail_test")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "exit", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "start", To: "exit"})

	reg := NewHandlerRegistry()
	// start succeeds, exit returns fail.
	reg.Register(&testHandler{name: "start", executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		return Outcome{Status: OutcomeSuccess}, nil
	}})
	reg.Register(&testHandler{name: "exit", executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		return Outcome{Status: OutcomeFail}, nil
	}})

	engine := NewEngine(g, reg,
		WithArtifactDir(artifactBase),
		WithGitArtifacts(true),
	)
	result, _ := engine.Run(context.Background())
	if result == nil {
		t.Fatalf("expected non-nil result on fail")
	}
	if result.Status != OutcomeFail {
		t.Fatalf("expected fail status, got %q", result.Status)
	}

	// Locate the run dir.
	entries, err := os.ReadDir(artifactBase)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var repoDir string
	for _, e := range entries {
		if e.IsDir() {
			repoDir = filepath.Join(artifactBase, e.Name())
			break
		}
	}
	if repoDir == "" {
		t.Fatalf("no run dir created")
	}

	log := gitOutput(t, repoDir, "log", "--oneline")
	t.Logf("git log:\n%s", log)
	if !strings.Contains(log, "node(exit):") {
		t.Errorf("git log missing fail commit for exit node:\n%s", log)
	}
	if !strings.Contains(log, "outcome=fail") {
		t.Errorf("git log missing outcome=fail:\n%s", log)
	}
}

// --- #302: CommitWIP recoverable-ref tests ---

// TestGitArtifactRepo_CommitWIP_DirtyTree verifies that CommitWIP commits a
// dirty working tree (including newly created/untracked files) to a named,
// recoverable tag tracker/wip/<runID>/<nodeID> and leaves the tree clean.
func TestGitArtifactRepo_CommitWIP_DirtyTree(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	runID := "wiprun1"
	repo := newGitArtifactRepo(dir, runID)
	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Simulate green-but-uncommitted agent work: a brand-new (untracked) file.
	if err := os.WriteFile(filepath.Join(dir, "feature.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ref, err := repo.CommitWIP("Implement")
	if err != nil {
		t.Fatalf("CommitWIP: %v", err)
	}
	want := "tracker/wip/" + runID + "/Implement"
	if ref != want {
		t.Fatalf("ref: got %q want %q", ref, want)
	}

	tags := gitOutput(t, dir, "tag", "-l", "tracker/wip/*")
	if !strings.Contains(tags, want) {
		t.Errorf("expected tag %q in:\n%s", want, tags)
	}

	// The untracked file must be captured at the tagged commit.
	show := gitOutput(t, dir, "show", "--stat", want)
	if !strings.Contains(show, "feature.go") {
		t.Errorf("WIP commit missing feature.go:\n%s", show)
	}

	// Tree is clean after WIP commit — the work was persisted.
	if st := gitOutput(t, dir, "status", "--porcelain"); st != "" {
		t.Errorf("expected clean tree after CommitWIP, got:\n%s", st)
	}
}

// TestGitArtifactRepo_CommitWIP_CleanTree verifies that CommitWIP is a no-op on
// a clean tree: no empty commit, no ref, empty return.
func TestGitArtifactRepo_CommitWIP_CleanTree(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	repo := newGitArtifactRepo(dir, "cleanrun")
	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	headBefore := gitOutput(t, dir, "rev-parse", "HEAD")

	ref, err := repo.CommitWIP("Implement")
	if err != nil {
		t.Fatalf("CommitWIP: %v", err)
	}
	if ref != "" {
		t.Errorf("expected empty ref on clean tree, got %q", ref)
	}
	if tags := gitOutput(t, dir, "tag", "-l", "tracker/wip/*"); tags != "" {
		t.Errorf("expected no WIP tag on clean tree, got:\n%s", tags)
	}
	if headAfter := gitOutput(t, dir, "rev-parse", "HEAD"); headAfter != headBefore {
		t.Errorf("clean-tree CommitWIP moved HEAD: %s -> %s", headBefore, headAfter)
	}
}

// TestGitArtifactRepo_CommitWIP_FailedRepoNoOp verifies that a repo marked
// failed no-ops without creating a ref.
func TestGitArtifactRepo_CommitWIP_FailedRepoNoOp(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	repo := newGitArtifactRepo(dir, "failrun")
	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	repo.failed = true

	ref, err := repo.CommitWIP("Implement")
	if err != nil {
		t.Fatalf("CommitWIP on failed repo: %v", err)
	}
	if ref != "" {
		t.Errorf("expected empty ref on failed repo, got %q", ref)
	}
}

// makeWIPRegistry returns a registry whose codergen handler dirties the
// artifact tree and/or fails based on per-node behavior, for #302 engine
// integration tests. failNodes return OutcomeFail after writing
// <nodeID>.go; other nodes write <nodeID>.go and succeed.
func makeWIPRegistry(failNodes map[string]bool, dirtyNodes map[string]bool) *HandlerRegistry {
	reg := NewHandlerRegistry()
	for _, name := range []string{"start", "exit", "codergen", "wait.human", "conditional", "parallel", "parallel.fan_in", "tool"} {
		reg.Register(&testHandler{name: name, executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if dirtyNodes[node.ID] {
				dir, _ := pctx.GetInternal(InternalKeyArtifactDir)
				if err := os.WriteFile(filepath.Join(dir, node.ID+".go"), []byte("package x\n"), 0o644); err != nil {
					return Outcome{}, err
				}
			}
			if failNodes[node.ID] {
				return Outcome{Status: OutcomeFail}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		}})
	}
	return reg
}

// TestEngine_CommitWIP_StrictFailureHaltPreservesWork drives an agent node to
// OutcomeFail with a dirty tree on the strict-failure HALT path (#302). The
// uncommitted work must be preserved to a recoverable ref recorded in both the
// trace and the checkpoint, even though the pipeline halts.
func TestEngine_CommitWIP_StrictFailureHaltPreservesWork(t *testing.T) {
	requireGit(t)
	artifactBase := t.TempDir()

	g := NewGraph("wip_halt_test")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "Implement", Shape: "box", Label: "Implement"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "start", To: "Implement"})
	g.AddEdge(&Edge{From: "Implement", To: "end"})

	reg := makeWIPRegistry(map[string]bool{"Implement": true}, map[string]bool{"Implement": true})
	engine := NewEngine(g, reg, WithArtifactDir(artifactBase), WithGitArtifacts(true))
	result, _ := engine.Run(context.Background())
	if result == nil || result.Status != OutcomeFail {
		t.Fatalf("expected fail result, got %+v", result)
	}

	repoDir := filepath.Join(artifactBase, result.RunID)
	wantRef := "tracker/wip/" + result.RunID + "/Implement"

	// 1. recoverable ref exists and captured the work.
	if tags := gitOutput(t, repoDir, "tag", "-l", "tracker/wip/*"); !strings.Contains(tags, wantRef) {
		t.Errorf("expected WIP tag %q, got:\n%s", wantRef, tags)
	}
	if show := gitOutput(t, repoDir, "show", "--stat", wantRef); !strings.Contains(show, "Implement.go") {
		t.Errorf("WIP ref missing Implement.go:\n%s", show)
	}

	// 2. trace records the ref on the failed node.
	var traceRef string
	for _, e := range result.Trace.Entries {
		if e.NodeID == "Implement" {
			traceRef = e.WIPRef
		}
	}
	if traceRef != wantRef {
		t.Errorf("trace WIPRef for Implement: got %q want %q", traceRef, wantRef)
	}

	// 3. checkpoint records the ref (durably persisted).
	cp, err := LoadCheckpoint(filepath.Join(repoDir, "checkpoint.json"))
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if cp.WIPRefs["Implement"] != wantRef {
		t.Errorf("checkpoint WIPRefs[Implement]: got %q want %q", cp.WIPRefs["Implement"], wantRef)
	}
}

// TestEngine_CommitWIP_FallbackPreservesWorkBeforeRouting proves the WIP ref is
// created BEFORE the fallback routing decision (#302): the failing node's work
// is captured in the ref, but the fallback (escalation) node's later changes
// are NOT — establishing the ordering.
func TestEngine_CommitWIP_FallbackPreservesWorkBeforeRouting(t *testing.T) {
	requireGit(t)
	artifactBase := t.TempDir()

	g := NewGraph("wip_fallback_test")
	g.Attrs["fallback_target"] = "Escalate" // graph-level catch-all (#295)
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "Implement", Shape: "box", Label: "Implement"})
	g.AddNode(&Node{ID: "Escalate", Shape: "box", Label: "Escalate"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "start", To: "Implement"})
	g.AddEdge(&Edge{From: "Implement", To: "end"}) // unconditional → strict failure → fallback
	g.AddEdge(&Edge{From: "Escalate", To: "end"})

	reg := makeWIPRegistry(
		map[string]bool{"Implement": true},
		map[string]bool{"Implement": true, "Escalate": true},
	)
	engine := NewEngine(g, reg, WithArtifactDir(artifactBase), WithGitArtifacts(true))
	result, _ := engine.Run(context.Background())
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	repoDir := filepath.Join(artifactBase, result.RunID)
	wantRef := "tracker/wip/" + result.RunID + "/Implement"

	show := gitOutput(t, repoDir, "show", "--stat", wantRef)
	if !strings.Contains(show, "Implement.go") {
		t.Errorf("WIP ref missing Implement.go:\n%s", show)
	}
	// Ordering proof: the WIP ref was captured before Escalate ran, so it must
	// NOT contain Escalate's file.
	if strings.Contains(show, "Escalate.go") {
		t.Errorf("WIP ref captured after fallback ran (contains Escalate.go):\n%s", show)
	}

	cp, err := LoadCheckpoint(filepath.Join(repoDir, "checkpoint.json"))
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if cp.WIPRefs["Implement"] != wantRef {
		t.Errorf("checkpoint WIPRefs[Implement]: got %q want %q", cp.WIPRefs["Implement"], wantRef)
	}
}

// TestEngine_CommitWIP_CleanTreeFailureNoRef verifies that a failed node with a
// CLEAN tree creates no WIP ref and records none in the trace (#302 no-op).
func TestEngine_CommitWIP_CleanTreeFailureNoRef(t *testing.T) {
	requireGit(t)
	artifactBase := t.TempDir()

	g := NewGraph("wip_clean_test")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "Implement", Shape: "box", Label: "Implement"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "start", To: "Implement"})
	g.AddEdge(&Edge{From: "Implement", To: "end"})

	// fail but DO NOT dirty the tree.
	reg := makeWIPRegistry(map[string]bool{"Implement": true}, map[string]bool{})
	engine := NewEngine(g, reg, WithArtifactDir(artifactBase), WithGitArtifacts(true))
	result, _ := engine.Run(context.Background())
	if result == nil || result.Status != OutcomeFail {
		t.Fatalf("expected fail result, got %+v", result)
	}

	repoDir := filepath.Join(artifactBase, result.RunID)
	if tags := gitOutput(t, repoDir, "tag", "-l", "tracker/wip/*"); tags != "" {
		t.Errorf("expected no WIP tag on clean-tree failure, got:\n%s", tags)
	}
	for _, e := range result.Trace.Entries {
		if e.NodeID == "Implement" && e.WIPRef != "" {
			t.Errorf("expected empty WIPRef on clean-tree failure, got %q", e.WIPRef)
		}
	}
}

// TestEngine_CommitWIP_NoGitAdapterWarns verifies that when git artifacts are
// disabled (no adapter), a failed node routes without panic and emits a warning
// that work could not be preserved (#302 graceful skip).
func TestEngine_CommitWIP_NoGitAdapterWarns(t *testing.T) {
	g := NewGraph("wip_noadapter_test")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "Implement", Shape: "box", Label: "Implement"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "start", To: "Implement"})
	g.AddEdge(&Edge{From: "Implement", To: "end"})

	var mu sync.Mutex
	var events []PipelineEvent
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		mu.Lock()
		events = append(events, evt)
		mu.Unlock()
	})

	reg := makeWIPRegistry(map[string]bool{"Implement": true}, map[string]bool{})
	engine := NewEngine(g, reg, WithPipelineEventHandler(handler)) // no artifact dir, no git artifacts
	result, _ := engine.Run(context.Background())
	if result == nil || result.Status != OutcomeFail {
		t.Fatalf("expected fail result, got %+v", result)
	}

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, e := range events {
		if e.Type == EventWarning && strings.Contains(e.Message, "cannot preserve uncommitted work") {
			found = true
		}
	}
	if !found {
		t.Error("expected EventWarning that uncommitted work could not be preserved when no git adapter is configured")
	}
}

// TestGitArtifactRepo_CommitWIP_CapturesDeletion verifies that a deletion-only
// dirty tree is fully captured in the WIP commit (#302 review, Copilot): the
// snapshot must reflect removals, not just additions/modifications.
func TestGitArtifactRepo_CommitWIP_CapturesDeletion(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	runID := "delrun"
	repo := newGitArtifactRepo(dir, runID)
	if err := repo.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Seed a tracked file, commit it, then delete it so the ONLY dirty change
	// is a deletion.
	if err := os.WriteFile(filepath.Join(dir, "doomed.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, dir, "add", "-A")
	gitOutput(t, dir, "commit", "-m", "seed doomed.go")
	if err := os.Remove(filepath.Join(dir, "doomed.go")); err != nil {
		t.Fatal(err)
	}

	ref, err := repo.CommitWIP("Implement")
	if err != nil {
		t.Fatalf("CommitWIP after delete: %v", err)
	}
	want := "tracker/wip/" + runID + "/Implement"
	if ref != want {
		t.Fatalf("ref: got %q want %q", ref, want)
	}
	// The deletion must be recorded at the tagged commit.
	show := gitOutput(t, dir, "show", "--stat", want)
	if !strings.Contains(show, "doomed.go") {
		t.Errorf("WIP ref did not capture deletion of doomed.go:\n%s", show)
	}
	if st := gitOutput(t, dir, "status", "--porcelain"); st != "" {
		t.Errorf("expected clean tree after CommitWIP, got:\n%s", st)
	}
}

// TestEngine_CommitWIP_HandlerErrorPreservesWork verifies that when a handler
// writes artifacts and then returns a Go error (terminal node death, e.g. a
// tool/agent dying mid-write or a cancellation), the dirty tree is still
// preserved to a recoverable ref recorded in the checkpoint and trace (#302
// review, Codex) — not just on the status-based fail/exhaust paths.
func TestEngine_CommitWIP_HandlerErrorPreservesWork(t *testing.T) {
	requireGit(t)
	artifactBase := t.TempDir()

	g := NewGraph("wip_handler_error_test")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "Implement", Shape: "box", Label: "Implement"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "start", To: "Implement"})
	g.AddEdge(&Edge{From: "Implement", To: "end"})

	reg := NewHandlerRegistry()
	for _, name := range []string{"start", "exit", "codergen", "wait.human", "conditional", "parallel", "parallel.fan_in", "tool"} {
		reg.Register(&testHandler{name: name, executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "Implement" {
				dir, _ := pctx.GetInternal(InternalKeyArtifactDir)
				if err := os.WriteFile(filepath.Join(dir, "Implement.go"), []byte("package x\n"), 0o644); err != nil {
					return Outcome{}, err
				}
				return Outcome{}, fmt.Errorf("boom: handler died after writing files")
			}
			return Outcome{Status: OutcomeSuccess}, nil
		}})
	}

	engine := NewEngine(g, reg, WithArtifactDir(artifactBase), WithGitArtifacts(true))
	result, err := engine.Run(context.Background())
	if err == nil {
		t.Fatal("expected handler error to propagate")
	}
	if result == nil || result.Status != OutcomeFail {
		t.Fatalf("expected fail result, got %+v", result)
	}

	repoDir := filepath.Join(artifactBase, result.RunID)
	wantRef := "tracker/wip/" + result.RunID + "/Implement"

	if tags := gitOutput(t, repoDir, "tag", "-l", "tracker/wip/*"); !strings.Contains(tags, wantRef) {
		t.Errorf("expected WIP tag %q on handler-error path, got:\n%s", wantRef, tags)
	}
	cp, e := LoadCheckpoint(filepath.Join(repoDir, "checkpoint.json"))
	if e != nil {
		t.Fatalf("LoadCheckpoint: %v", e)
	}
	if cp.WIPRefs["Implement"] != wantRef {
		t.Errorf("checkpoint WIPRefs[Implement]: got %q want %q", cp.WIPRefs["Implement"], wantRef)
	}
	var traceRef string
	for _, en := range result.Trace.Entries {
		if en.NodeID == "Implement" {
			traceRef = en.WIPRef
		}
	}
	if traceRef != wantRef {
		t.Errorf("trace WIPRef for Implement: got %q want %q", traceRef, wantRef)
	}
}

// hasEnvPrefix reports whether any env entry starts with the given "KEY=" prefix.
func hasEnvPrefix(env []string, prefix string) bool {
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

// TestGitSafeEnv_StripsRedirectVarsEvenUnderPassEnv pins the #401 review finding
// (codex P1 + Copilot ×2): git-internal repository pointers must be stripped
// from the git-subprocess env UNCONDITIONALLY — including under
// TRACKER_PASS_ENV=1, which is a credential pass-through escape hatch, not
// permission to re-anchor git at the outer repo. The earlier code returned
// os.Environ() unfiltered in pass-env mode, re-leaking GIT_DIR/GIT_INDEX_FILE.
func TestGitSafeEnv_StripsRedirectVarsEvenUnderPassEnv(t *testing.T) {
	t.Setenv("GIT_DIR", "/outer/.git")
	t.Setenv("GIT_INDEX_FILE", "/outer/.git/index")
	t.Setenv("GIT_WORK_TREE", "/outer")
	t.Setenv("GIT_OBJECT_DIRECTORY", "/outer/.git/objects")
	t.Setenv("GIT_COMMON_DIR", "/outer/.git")
	t.Setenv("EXAMPLE_API_KEY", "sekret")

	redirects := []string{
		"GIT_DIR=", "GIT_INDEX_FILE=", "GIT_WORK_TREE=",
		"GIT_OBJECT_DIRECTORY=", "GIT_COMMON_DIR=",
	}

	t.Run("pass-env off strips redirects and credentials", func(t *testing.T) {
		t.Setenv("TRACKER_PASS_ENV", "")
		env := gitSafeEnv()
		for _, r := range redirects {
			if hasEnvPrefix(env, r) {
				t.Errorf("gitSafeEnv leaked %q without TRACKER_PASS_ENV", r)
			}
		}
		if hasEnvPrefix(env, "EXAMPLE_API_KEY=") {
			t.Error("gitSafeEnv leaked EXAMPLE_API_KEY without TRACKER_PASS_ENV")
		}
	})

	t.Run("pass-env on still strips redirects but passes credentials", func(t *testing.T) {
		t.Setenv("TRACKER_PASS_ENV", "1")
		env := gitSafeEnv()
		for _, r := range redirects {
			if hasEnvPrefix(env, r) {
				t.Errorf("gitSafeEnv leaked %q under TRACKER_PASS_ENV=1 — redirect strip must be unconditional (#401)", r)
			}
		}
		if !hasEnvPrefix(env, "EXAMPLE_API_KEY=") {
			t.Error("gitSafeEnv dropped EXAMPLE_API_KEY under TRACKER_PASS_ENV=1 — credential pass-through broken")
		}
	})
}
