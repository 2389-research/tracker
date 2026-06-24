// ABOUTME: #405 guards for the build_product dogfood cascade (run 634a2527ff56):
// ABOUTME: build artifacts must never be swept into a checkpoint commit.
package pipeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitInit makes dir a committable git repo with one initial commit, so a
// CommitIfDirty runtime test exercises the real `git status`/`git add -A`
// path. Returns the dir for chaining.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available; skipping CommitIfDirty runtime test")
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "test"},
		{"config", "user.email", "test@test.local"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, b)
		}
	}
}

// gitTracked returns the newline-joined list of files tracked at HEAD.
func gitTracked(t *testing.T, dir string) string {
	t.Helper()
	c := exec.Command("git", "ls-tree", "-r", "--name-only", "HEAD")
	c.Dir = dir
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git ls-tree: %v\n%s", err, b)
	}
	return string(b)
}

// TestBuildProductCommitIfDirtySkipsBinaryArtifact pins #405 AC1: a compiled,
// arbitrary-named binary dropped into the tree by a test (Go `go build -o
// goblin .` — Go binaries have no fixed extension, so the static .gitignore
// seed can't catch them) must NOT be swept into a checkpoint commit. In run
// 634a2527ff56 `git add -A` committed `goblin`, and VerifyMilestone then FAILed
// the milestone for out-of-scope work. CommitIfDirty must gitignore the
// untracked executable binary instead of committing it.
func TestBuildProductCommitIfDirtySkipsBinaryArtifact(t *testing.T) {
	dir := setupRunDir(t)
	gitInit(t, dir)
	// A real source file (text) — must be committed.
	mustWrite(t, filepath.Join(dir, "main.go"), "package main\nfunc main() {}\n")
	// A compiled binary artifact (NUL bytes → binary, +x) — must be skipped.
	bin := filepath.Join(dir, "goblin")
	if err := os.WriteFile(bin, []byte("\x7fELF\x00\x00\x00binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, code := runToolCmd(t, toolCmd(t, "CommitIfDirty"), dir, append(os.Environ(), "HOME="+t.TempDir()))
	if code != 0 {
		t.Fatalf("CommitIfDirty exit=%d:\n%s", code, out)
	}

	tracked := gitTracked(t, dir)
	if !strings.Contains(tracked, "main.go") {
		t.Errorf("main.go (real source) was not committed:\n%s", tracked)
	}
	if strings.Contains(tracked, "goblin") {
		t.Errorf("compiled `goblin` binary was committed into the checkpoint — Verify will FAIL the milestone for out-of-scope work (issue #405 AC1):\n%s", tracked)
	}
	// PR #411 finding #2: the runtime binary-artifact exclusion must go to the
	// LOCAL, untracked .git/info/exclude — NOT the tracked .gitignore. A runtime
	// write to the tracked .gitignore is itself an out-of-scope tree change that
	// VerifyMilestone would FAIL, defeating the purpose of skipping the binary.
	excl, _ := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if !strings.Contains(string(excl), "goblin") {
		t.Errorf("CommitIfDirty did not exclude the untracked binary artifact via .git/info/exclude:\n%s", excl)
	}
	if gi, err := os.ReadFile(filepath.Join(dir, ".gitignore")); err == nil && strings.Contains(string(gi), "goblin") {
		t.Errorf("CommitIfDirty wrote the binary-artifact ignore into the TRACKED .gitignore — that runtime tree mutation is itself out-of-scope work VerifyMilestone FAILs (PR #411 finding #2):\n%s", gi)
	}
}

// TestBuildProductSetupSeedsGitignoreBuildOutputs pins #405: the scaffold step
// must seed .gitignore with the detected toolchain's build outputs, not just
// `.ai/`. In run 634a2527ff56 the scaffold seeded only `.ai/`, so a compiled
// `goblin` binary was untracked, swept into a CommitIfDirty checkpoint by
// `git add -A`, and then FAILed by VerifyMilestone as out-of-scope work.
// A seeded build-output pattern (e.g. `*.test`) makes `git add -A` skip the
// artifact by construction.
func TestBuildProductSetupSeedsGitignoreBuildOutputs(t *testing.T) {
	cmd := toolCmd(t, "Setup")
	if !strings.Contains(cmd, ".gitignore") {
		t.Fatal("Setup no longer seeds .gitignore (issue #405)")
	}
	// `node_modules` is on the issue's expected build-output list and appears
	// nowhere in Setup today (a `*.test`-style anchor collides with the unrelated
	// reference-scan exclusion globs), so it is a clean signal the seed grew
	// beyond the lone `.ai/` entry into toolchain-appropriate build outputs.
	if !strings.Contains(cmd, "node_modules") {
		t.Error("Setup .gitignore seed covers only `.ai/`, not the toolchain's build outputs — a compiled artifact gets swept into a checkpoint commit and FAILs Verify (issue #405)")
	}
}
