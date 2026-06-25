// ABOUTME: Tests for artifact-repo health probe + reattach and terminal-path hard escalation (#423).
// ABOUTME: Injects an unavailable artifact repo to assert a HARD failure (not silent degradation) on the terminal commit path.
package pipeline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestEnsureHealthy_Reachable verifies a healthy repo passes the probe with NO
// state mutation and NO new commit (AC3 byte-identical happy path).
func TestEnsureHealthy_Reachable(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	r := newGitArtifactRepo(dir, "healthy")
	if err := r.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	before := gitOutput(t, dir, "rev-list", "--count", "HEAD")

	if err := r.ensureHealthy(); err != nil {
		t.Fatalf("ensureHealthy on healthy repo: %v", err)
	}
	if r.failed {
		t.Error("ensureHealthy must not mutate r.failed on a healthy repo")
	}
	after := gitOutput(t, dir, "rev-list", "--count", "HEAD")
	if before != after {
		t.Errorf("ensureHealthy added a commit on a healthy repo: before=%s after=%s", before, after)
	}
}

// TestEnsureHealthy_ReattachSucceeds verifies that a repo whose .git was lost
// (sandbox suspend) is reattached: failed-latch cleared, .git re-created.
func TestEnsureHealthy_ReattachSucceeds(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	r := newGitArtifactRepo(dir, "reattach")
	if err := r.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Simulate suspend loss of the git dir and a latched failure.
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("RemoveAll .git: %v", err)
	}
	r.failed = true

	if err := r.ensureHealthy(); err != nil {
		t.Fatalf("ensureHealthy should reattach, got %v", err)
	}
	if r.failed {
		t.Error("ensureHealthy should clear the failed latch after a successful reattach")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Errorf(".git not re-created after reattach: %v", err)
	}
}

// TestEnsureHealthy_ReattachFails verifies that when recovery cannot complete
// (dir non-writable so re-Init fails), ensureHealthy returns a wrapped
// ErrArtifactRepoUnavailable rather than silently un-failing a dead repo.
func TestEnsureHealthy_ReattachFails(t *testing.T) {
	requireGit(t)
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0500 does not prevent writes")
	}
	dir := t.TempDir()
	r := newGitArtifactRepo(dir, "deadrepo")
	if err := r.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("RemoveAll .git: %v", err)
	}
	// Make the dir non-writable so `git init` (re-Init) fails.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err := r.ensureHealthy()
	if err == nil {
		t.Fatal("expected ErrArtifactRepoUnavailable when reattach fails, got nil")
	}
	if !errors.Is(err, ErrArtifactRepoUnavailable) {
		t.Fatalf("expected ErrArtifactRepoUnavailable, got %v", err)
	}
}

// breakRepoHandler returns a registry whose target node dirties the tree, then
// destroys the artifact repo (RemoveAll .git + chmod 0500), then returns a
// handler ERROR — driving the terminal halt path (engine.go:330) into
// commitWIPBeforeRouting against an unrecoverable repo.
func breakRepoHandler(t *testing.T, breakNode string) *HandlerRegistry {
	t.Helper()
	reg := NewHandlerRegistry()
	for _, name := range []string{"start", "exit", "codergen", "wait.human", "conditional", "parallel", "parallel.fan_in", "tool"} {
		reg.Register(&testHandler{name: name, executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == breakNode {
				dir, _ := pctx.GetInternal(InternalKeyArtifactDir)
				_ = os.WriteFile(filepath.Join(dir, node.ID+".go"), []byte("package x\n"), 0o644)
				_ = os.RemoveAll(filepath.Join(dir, ".git"))
				_ = os.Chmod(dir, 0o500)
				return Outcome{}, errors.New("simulated handler crash after sandbox suspend")
			}
			return Outcome{Status: string(OutcomeSuccess)}, nil
		}})
	}
	return reg
}

// TestCommitWIPBeforeRouting_TerminalHardFail drives the handler-error terminal
// path with an unrecoverable artifact repo and asserts AC2: WorkPreserveFailed
// is set, an EventStageFailed carrying the repo-unavailable diagnostic fires,
// and Status stays the original OutcomeFail (original failure not masked).
func TestCommitWIPBeforeRouting_TerminalHardFail(t *testing.T) {
	requireGit(t)
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0500 does not prevent writes")
	}
	artifactBase := t.TempDir()

	g := NewGraph("wip_terminal_hardfail_test")
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

	reg := breakRepoHandler(t, "Implement")
	engine := NewEngine(g, reg, WithArtifactDir(artifactBase), WithGitArtifacts(true), WithPipelineEventHandler(handler))
	result, _ := engine.Run(context.Background())
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(artifactBase, result.RunID), 0o700) })

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Status != OutcomeFail {
		t.Fatalf("Status must stay original OutcomeFail (not masked), got %q", result.Status)
	}
	if !result.WorkPreserveFailed {
		t.Error("expected WorkPreserveFailed=true on terminal path when artifact repo is unrecoverable")
	}

	mu.Lock()
	defer mu.Unlock()
	foundHardSignal := false
	for _, e := range events {
		if e.Type == EventStageFailed && strings.Contains(e.Message, "artifact") &&
			strings.Contains(e.Message, "preserve") {
			foundHardSignal = true
		}
	}
	if !foundHardSignal {
		t.Errorf("expected EventStageFailed carrying the repo-unavailable preserve diagnostic; events=%v", events)
	}
}

// TestCommitWIPBeforeRouting_MidRoutingStaysWarning verifies that on a
// MID-ROUTING path (retry exhausted with a fallback_retry_target) an
// unavailable artifact repo stays a WARNING and does NOT change routing:
// WorkPreserveFailed is false and the run still routes to the fallback.
func TestCommitWIPBeforeRouting_MidRoutingStaysWarning(t *testing.T) {
	requireGit(t)
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0500 does not prevent writes")
	}
	artifactBase := t.TempDir()

	g := NewGraph("wip_midrouting_test")
	g.Attrs["default_max_retry"] = "1"
	g.Attrs["default_retry_policy"] = "none"
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "flaky", Shape: "box", Label: "Flaky", Attrs: map[string]string{"fallback_retry_target": "Escalate"}})
	g.AddNode(&Node{ID: "Escalate", Shape: "box", Label: "Escalate"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "start", To: "flaky"})
	g.AddEdge(&Edge{From: "flaky", To: "end"})
	g.AddEdge(&Edge{From: "Escalate", To: "end"})

	var mu sync.Mutex
	var events []PipelineEvent
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		mu.Lock()
		events = append(events, evt)
		mu.Unlock()
	})

	reg := NewHandlerRegistry()
	escalateRan := false
	for _, name := range []string{"start", "exit", "codergen", "wait.human", "conditional", "parallel", "parallel.fan_in", "tool"} {
		reg.Register(&testHandler{name: name, executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			switch node.ID {
			case "flaky":
				dir, _ := pctx.GetInternal(InternalKeyArtifactDir)
				_ = os.WriteFile(filepath.Join(dir, "flaky.go"), []byte("package x\n"), 0o644)
				_ = os.RemoveAll(filepath.Join(dir, ".git"))
				_ = os.Chmod(dir, 0o500)
				return Outcome{Status: string(OutcomeRetry)}, nil
			case "Escalate":
				escalateRan = true
				return Outcome{Status: string(OutcomeSuccess)}, nil
			default:
				return Outcome{Status: string(OutcomeSuccess)}, nil
			}
		}})
	}

	engine := NewEngine(g, reg, WithArtifactDir(artifactBase), WithGitArtifacts(true), WithPipelineEventHandler(handler))
	result, _ := engine.Run(context.Background())
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(artifactBase, result.RunID), 0o700) })

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.WorkPreserveFailed {
		t.Error("mid-routing path must NOT set WorkPreserveFailed (preserves routing contract)")
	}
	if !escalateRan {
		t.Error("mid-routing path must still route to the fallback target")
	}

	mu.Lock()
	defer mu.Unlock()
	for _, e := range events {
		if e.Type == EventStageFailed && strings.Contains(e.Message, "preserve") && strings.Contains(e.Message, "artifact") {
			t.Errorf("mid-routing path must not emit a hard EventStageFailed for the repo; got %q", e.Message)
		}
	}
}

// TestEngine_HealthyRepo_NoWorkPreserveFailed is the AC3 regression: a healthy
// device + healthy repo run produces WorkPreserveFailed=false and no new
// repo-unavailable EventStageFailed.
func TestEngine_HealthyRepo_NoWorkPreserveFailed(t *testing.T) {
	requireGit(t)
	artifactBase := t.TempDir()

	g := NewGraph("wip_healthy_test")
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
	if result.WorkPreserveFailed {
		t.Error("healthy repo must not set WorkPreserveFailed")
	}
	// WIP ref still preserved as before (behavior unchanged).
	repoDir := filepath.Join(artifactBase, result.RunID)
	wantRef := "tracker/wip/" + result.RunID + "/Implement"
	if tags := gitOutput(t, repoDir, "tag", "-l", "tracker/wip/*"); !strings.Contains(tags, wantRef) {
		t.Errorf("expected WIP tag %q on healthy repo, got:\n%s", wantRef, tags)
	}
}
