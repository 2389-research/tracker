// ABOUTME: Tests non-destructive working-tree WIP preservation on node failure (#488).
package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func gitOrFail(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGitDir(dir, gitSafeEnv(), args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(out)
}

func TestWorkingTreeWIP_PreservesUncommittedCode(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	gitOrFail(t, dir, "init")
	gitOrFail(t, dir, "config", "user.email", "t@t")
	gitOrFail(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOrFail(t, dir, "add", "-A")
	gitOrFail(t, dir, "commit", "-m", "base")

	// In-flight work: a tracked modification AND an untracked new file (the case
	// `git stash create` would miss).
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v2-inflight\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package x // brand new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := NewGraph("wip_worktree_test")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "Implement", Shape: "box", Label: "Implement"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "start", To: "Implement"})
	g.AddEdge(&Edge{From: "Implement", To: "end"})

	var mu sync.Mutex
	var msgs []string
	reg := makeWIPRegistry(map[string]bool{"Implement": true}, map[string]bool{})
	engine := NewEngine(g, reg, WithWorkDir(dir), WithPipelineEventHandler(PipelineEventHandlerFunc(func(e PipelineEvent) {
		mu.Lock()
		msgs = append(msgs, e.Message)
		mu.Unlock()
	})))
	result, _ := engine.Run(context.Background())
	if result == nil || result.Status != OutcomeFail {
		t.Fatalf("expected fail, got %+v", result)
	}

	// The snapshot ref exists and captures BOTH the tracked mod and the untracked file.
	ref := "refs/tracker/wip/" + result.RunID + "/Implement"
	if _, err := runGitDir(dir, gitSafeEnv(), "rev-parse", "--verify", ref); err != nil {
		t.Fatalf("expected WIP ref %s to exist", ref)
	}
	if got := gitOrFail(t, dir, "show", ref+":a.txt"); got != "v2-inflight" {
		t.Errorf("snapshot a.txt = %q, want the in-flight version", got)
	}
	if got := gitOrFail(t, dir, "show", ref+":new.go"); !strings.Contains(got, "brand new") {
		t.Errorf("snapshot missing the untracked new file, got %q", got)
	}

	// NON-DESTRUCTIVE: the working tree is untouched — a.txt still in-flight, new.go still present, HEAD unchanged.
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); strings.TrimSpace(string(b)) != "v2-inflight" {
		t.Errorf("working tree a.txt was modified by preservation: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.go")); err != nil {
		t.Error("working tree untracked file was removed by preservation")
	}

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(strings.Join(msgs, "\n"), "preserved in-flight changes") {
		t.Errorf("expected a preservation message, got:\n%s", strings.Join(msgs, "\n"))
	}
}

func TestWorkingTreeWIP_CleanTreeNoRef(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	gitOrFail(t, dir, "init")
	gitOrFail(t, dir, "config", "user.email", "t@t")
	gitOrFail(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOrFail(t, dir, "add", "-A")
	gitOrFail(t, dir, "commit", "-m", "base")
	// Clean tree — nothing uncommitted.

	g := NewGraph("wip_clean_test")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "Implement", Shape: "box"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "start", To: "Implement"})
	g.AddEdge(&Edge{From: "Implement", To: "end"})

	reg := makeWIPRegistry(map[string]bool{"Implement": true}, map[string]bool{})
	engine := NewEngine(g, reg, WithWorkDir(dir))
	result, _ := engine.Run(context.Background())
	if result == nil {
		t.Fatal("nil result")
	}
	ref := "refs/tracker/wip/" + result.RunID + "/Implement"
	if _, err := runGitDir(dir, gitSafeEnv(), "rev-parse", "--verify", ref); err == nil {
		t.Error("clean tree should not create a WIP ref")
	}
}
