// ABOUTME: Runs Git fixture tests under the repository pointers exported by hooks.
// ABOUTME: Proves fixtures leave the caller's real Git history and index untouched.
package tracker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolateTestGit also covers shell commands spawned through library workflows.
func isolateTestGit(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if testGitRedirectVars[strings.ToUpper(key)] {
			t.Setenv(key, value) // Register restoration before removing the variable.
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestGitFixturesIgnoreHookRepository(t *testing.T) {
	requireGit(t)
	outer := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		prefix := []string{"-C", outer, "-c", "user.name=fixture", "-c", "user.email=fixture@example.com"}
		cmd := exec.Command("git", append(prefix, args...)...)
		cmd.Env = cleanGitEnv()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(outer, "README.md"), []byte("caller repository\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "README.md")
	git("commit", "-qm", "caller baseline")
	head := git("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(outer, "README.md"), []byte("caller staged work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "README.md")
	index, err := os.ReadFile(filepath.Join(outer, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^(TestCheckWorkdirWarnsWhenEnvFileIsGitTracked|TestFirstWin_QuickfixApproveCommitsToItsBranch)$")
	cmd.Env = append(cleanGitEnv(), "GIT_DIR="+filepath.Join(outer, ".git"), "GIT_WORK_TREE="+outer, "GIT_INDEX_FILE="+filepath.Join(outer, ".git", "index"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("fixture subprocess failed under hook environment: %v\n%s", err, out)
	}
	if got := git("rev-parse", "HEAD"); got != head {
		t.Errorf("fixture moved caller HEAD: %s -> %s", head, got)
	}
	after, err := os.ReadFile(filepath.Join(outer, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(index) {
		t.Error("fixture modified caller index")
	}
}
