// ABOUTME: First-win end to end: the built tracker binary runs explain and quickfix on a fake
// ABOUTME: `claude` (no API key), as a new user would; quickfix --auto-approve leaves nothing behind.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClaudeScript answers --version, and otherwise emits one stream-json
// turn. In the quickfix worktree it also makes the change and writes the
// summary, as MakeChange's prompt asks.
const fakeClaudeScript = `#!/bin/sh
if [ "${1:-}" = "--version" ]; then echo "2.1.999 (Claude Code)"; exit 0; fi
case "$PWD" in
  */.tracker/quickfix/worktree)
    echo "fixed" >> README.md
    mkdir -p .tracker/quickfix
    echo "Fixed the README." > .tracker/quickfix/summary.md
    TEXT="Changed README.md." ;;
  *) TEXT="This repo is a tiny fixture with one README." ;;
esac
printf '{"type":"system","subtype":"init"}\n'
printf '{"type":"assistant","message":{"content":[{"type":"text","text":"%s"}]}}\n' "$TEXT"
printf '{"type":"result","subtype":"success","num_turns":1,"total_cost_usd":0.04,"result":"%s","usage":{"input_tokens":100,"output_tokens":50}}\n' "$TEXT"
`

type firstWinEnv struct {
	bin, repo string
	env       []string
}

func newFirstWinEnv(t *testing.T) *firstWinEnv {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the tracker binary")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "tracker")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	fakeBin := filepath.Join(root, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "claude"), []byte(fakeClaudeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "my repo")
	for _, d := range []string{home, repo} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e := &firstWinEnv{bin: bin, repo: repo, env: []string{
		"PATH=" + fakeBin + string(os.PathListSeparator) + filepath.Dir(gitPath) + string(os.PathListSeparator) + "/usr/bin:/bin",
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".state"),
		"TRACKER_NO_UPDATE_CHECK=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"TERM=dumb",
	}}
	e.git(t, "init", "-q", "-b", "main")
	e.git(t, "config", "user.name", "tester")
	e.git(t, "config", "user.email", "tester@example.com")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.git(t, "add", "README.md")
	e.git(t, "commit", "-q", "-m", "base")
	return e
}

func (e *firstWinEnv) git(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = e.repo, e.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (e *firstWinEnv) tracker(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(e.bin, args...)
	cmd.Dir, cmd.Env = e.repo, e.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestFirstWin_ExplainOnClaudeWithNoKey(t *testing.T) {
	e := newFirstWinEnv(t)
	out, err := e.tracker(t, "explain", "--no-tui", "--param", "question=what does this repo do?")
	if err != nil {
		t.Fatalf("tracker explain: %v\n%s", err, out)
	}
	if !strings.Contains(out, "backend: auto → claude-code") {
		t.Errorf("the backend choice must be announced; output:\n%s", out)
	}
	matches, _ := filepath.Glob(filepath.Join(e.repo, ".tracker", "runs", "*", "explain.md"))
	if len(matches) != 1 {
		t.Fatalf("explain.md not written; output:\n%s", out)
	}
	if b, _ := os.ReadFile(matches[0]); !strings.Contains(string(b), "tiny fixture") {
		t.Errorf("explain.md = %q, want the answer", b)
	}
	if !strings.Contains(out, `tracker quickfix --param change="<one sentence>"`) {
		t.Errorf("the next step must be printed; output:\n%s", out)
	}
	if !strings.Contains(out, "included in your Claude plan") {
		t.Errorf("a claude-code run's cost must be labeled as plan usage; output:\n%s", out)
	}
	// Tracker's own state lives under .tracker/; no tracked file may change.
	if st := e.git(t, "status", "--porcelain", "--untracked-files=no"); st != "" {
		t.Errorf("explain must not change tracked files: %s", st)
	}
}

func TestFirstWin_QuickfixAutoApproveLeavesNothing(t *testing.T) {
	e := newFirstWinEnv(t)
	head := e.git(t, "rev-parse", "HEAD")
	out, err := e.tracker(t, "quickfix", "--no-tui", "--auto-approve", "--param", "change=fix the README")
	if err != nil {
		t.Fatalf("tracker quickfix: %v\n%s", err, out)
	}
	if br := e.git(t, "branch", "--list", "tracker/quickfix/*"); br != "" {
		t.Errorf("--auto-approve rejects: no quickfix branch may remain, got %q\n%s", br, out)
	}
	if _, err := os.Stat(filepath.Join(e.repo, ".tracker", "quickfix", "worktree")); !os.IsNotExist(err) {
		t.Errorf("the worktree must be removed; stat err = %v", err)
	}
	if got := e.git(t, "rev-parse", "HEAD"); got != head {
		t.Errorf("the user's HEAD moved: %s → %s", head, got)
	}
	if b, _ := os.ReadFile(filepath.Join(e.repo, "README.md")); string(b) != "base\n" {
		t.Errorf("the user's README changed: %q", b)
	}
	if st := e.git(t, "status", "--porcelain"); st != "" {
		t.Errorf("the user's checkout must be untouched: %s", st)
	}
}
