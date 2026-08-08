// ABOUTME: Local git-exclude for the artifact repo — keeps .tracker/ (staged
// ABOUTME: inputs, incl. 0600 secrets #555; checkpoint; activity) out of commits/bundle.
package pipeline

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// writeGitExcludes sets up the artifact repo's ignore rules: a default
// .gitignore (only when absent — we don't overwrite a repo's own) plus the
// always-effective local exclude for .tracker/. Missing .gitignore is
// non-fatal; temp files would just show up in commits.
func (r *gitArtifactRepo) writeGitExcludes() {
	gitignorePath := filepath.Join(r.dir, ".gitignore")
	if _, err := os.Stat(gitignorePath); errors.Is(err, os.ErrNotExist) {
		_ = os.WriteFile(gitignorePath, []byte("*.tmp\ncheckpoint.json\n"), 0o644)
	}
	r.excludeTrackerDir()
}

// excludeTrackerDir appends ".tracker/" to .git/info/exclude (the LOCAL,
// always-effective exclude — not .gitignore, which we won't overwrite if the
// repo already ships one) when not already present. .tracker/ holds run
// metadata AND staged inputs, including 0600 secret inputs (#555), which must
// never land in a commit or the exported bundle. Idempotent; best-effort — a
// failure just means .tracker/ might be committed, which the .gitignore in Init
// also guards.
func (r *gitArtifactRepo) excludeTrackerDir() {
	excludePath := filepath.Join(r.dir, ".git", "info", "exclude")
	if data, err := os.ReadFile(excludePath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == ".tracker/" {
				return
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(".tracker/\n")
}
