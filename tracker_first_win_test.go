// ABOUTME: Library end to end for the quickfix built-in: a scripted interviewer approves, and the
// ABOUTME: change lands on tracker/quickfix/<run-id> with the user's checkout and branch untouched.
package tracker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const firstWinFakeClaude = `#!/bin/sh
if [ "${1:-}" = "--version" ]; then echo "2.1.999 (Claude Code)"; exit 0; fi
case "$PWD" in
  */.tracker/quickfix/worktree)
    echo "fixed" >> README.md
    mkdir -p .tracker/quickfix
    echo "Fixed the README." > .tracker/quickfix/summary.md ;;
esac
printf '{"type":"system","subtype":"init"}\n'
printf '{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}\n'
printf '{"type":"result","subtype":"success","num_turns":1,"total_cost_usd":0.04,"result":"done","usage":{"input_tokens":100,"output_tokens":50}}\n'
`

// approvingGate answers every gate "approve".
type approvingGate struct{ prompts []string }

func (g *approvingGate) Ask(p string, _ []string, _ string) (string, error) { return g.answer(p) }
func (g *approvingGate) AskFreeform(p string) (string, error)               { return g.answer(p) }
func (g *approvingGate) AskFreeformWithLabels(p string, _ []string, _ string) (string, error) {
	return g.answer(p)
}
func (g *approvingGate) answer(p string) (string, error) {
	g.prompts = append(g.prompts, p)
	return "approve", nil
}

func TestFirstWin_QuickfixApproveCommitsToItsBranch(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	fakeBin := filepath.Join(root, "bin")
	repo := filepath.Join(root, "my repo")
	for _, d := range []string{fakeBin, repo, filepath.Join(root, "home")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "claude"), []byte(firstWinFakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+filepath.Dir(gitPath)+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "home", ".state"))
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENAI_COMPAT_API_KEY", "TRACKER_OPENAI_AUTH"} {
		t.Setenv(k, "")
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "tester")
	git("config", "user.email", "tester@example.com")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "README.md")
	git("commit", "-q", "-m", "base")
	base := git("rev-parse", "HEAD")

	src, info, err := OpenWorkflow("quickfix")
	if err != nil {
		t.Fatal(err)
	}
	gate := &approvingGate{}
	res, err := Run(context.Background(), string(src), Config{
		Source:      info.Ref(),
		WorkingDir:  repo,
		Params:      map[string]string{"change": "fix the README"},
		Interviewer: gate,
		// quickfix keeps its state in the run dir (TRACKER_RUN_DIR), so a
		// library caller sets an artifact dir, as the CLI always does.
		ArtifactDir: filepath.Join(repo, ".tracker", "runs"),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "success" {
		t.Fatalf("status = %q, want success", res.Status)
	}
	branch := "tracker/quickfix/" + res.RunID
	if got := git("show", branch+":README.md"); got != "base\nfixed" {
		t.Errorf("%s README = %q, want the change", branch, got)
	}
	if got := git("log", "-1", "--format=%s%n%b", branch); !strings.Contains(got, "quickfix: change from tracker run "+res.RunID) || !strings.Contains(got, "Fixed the README.") {
		t.Errorf("commit message = %q", got)
	}
	if got := git("rev-parse", "main"); got != base {
		t.Errorf("the user's branch moved: %s → %s", base, got)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "README.md")); string(b) != "base\n" {
		t.Errorf("the user's README changed: %q", b)
	}
	if len(gate.prompts) == 0 || !strings.Contains(strings.Join(gate.prompts, "\n"), "+fixed") {
		t.Errorf("the review gate must show the diff; prompts = %q", gate.prompts)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, ".tracker", "runs", res.RunID, "quickfix.json")); !strings.Contains(string(b), branch) {
		t.Errorf("quickfix.json = %q, want the branch", b)
	}
}
