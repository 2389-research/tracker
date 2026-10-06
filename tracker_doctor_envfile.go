// ABOUTME: Doctor check for a git-tracked <workdir>/.env (#659): a committed .env is readable by every clone
// ABOUTME: and is loaded by `tracker run`, so it is warned about — never refused.
package tracker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// gitTrackedProbeTimeout bounds the `git ls-files` probe so a wedged git
// cannot stall doctor.
const gitTrackedProbeTimeout = 5 * time.Second

// trackedEnvFileWarning returns a warning when <workdir>/.env exists and git
// tracks it, else "". Missing git, a non-repo workdir and probe errors all
// read as "not tracked" — this is an advisory, not a gate.
func trackedEnvFileWarning(workdir string) string {
	if _, err := os.Lstat(filepath.Join(workdir, ".env")); err != nil {
		return ""
	}
	if !envFileIsGitTracked(workdir) {
		return ""
	}
	return ".env is tracked by git — every clone can read it and `tracker run` loads it; `git rm --cached .env`, add .env to .gitignore, and rotate any keys it holds"
}

// envFileIsGitTracked runs `git ls-files --error-unmatch -- .env` in workdir
// with the credential-scrubbed probe environment used by the other doctor
// git probes.
func envFileIsGitTracked(workdir string) bool {
	if _, err := exec.LookPath("git"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTrackedProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", workdir, "ls-files", "--error-unmatch", "--", ".env")
	cmd.Env = pipeline.GitProbeEnv()
	return cmd.Run() == nil
}
